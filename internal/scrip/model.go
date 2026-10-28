// Package scrip downloads, parses, caches and searches the Kotak Scrip Master.
//
// The Scrip Master is the only reliable source of option tokens: the REST API
// has no option-chain endpoint. CSV headers are matched by normalised name so a
// broker-side column reorder does not break ingestion.
package scrip

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrSchema marks a Scrip Master file whose columns or values do not match the
// documented contract. The previous cache is kept when this happens.
var ErrSchema = errors.New("scrip master schema mismatch")

// Exchange segments understood by the parser.
const (
	SegmentNSEFO = "nse_fo"
	SegmentBSEFO = "bse_fo"
	SegmentNSECM = "nse_cm"
	SegmentBSECM = "bse_cm"
)

// OptionType is a CE or PE contract.
type OptionType string

const (
	// Call identifies a call option contract.
	Call OptionType = "CE"
	// Put identifies a put option contract.
	Put OptionType = "PE"
)

// ist is the market timezone. A fixed zone avoids depending on tzdata for a
// date-only value.
var ist = time.FixedZone("IST", 5*60*60+30*60)

// Option is one index option contract from the Scrip Master.
type Option struct {
	InstrumentKey   string     `json:"instrument_key"`
	ExchangeSegment string     `json:"exchange_segment"`
	PSymbol         string     `json:"p_symbol"`
	PTrdSymbol      string     `json:"p_trd_symbol"`
	Underlying      string     `json:"underlying"`
	Expiry          string     `json:"expiry"`
	StrikePaise     int64      `json:"strike_paise"`
	OptionType      OptionType `json:"option_type"`
	LotSize         int64      `json:"lot_size"`
}

// Spot is an index or volatility instrument in the cash segment.
type Spot struct {
	InstrumentKey   string `json:"instrument_key"`
	ExchangeSegment string `json:"exchange_segment"`
	Symbol          string `json:"symbol"`
	DisplayName     string `json:"display_name"`
	PSymbol         string `json:"p_symbol"`
}

// Underlying maps a configured index to its Scrip Master and feed names.
type Underlying struct {
	Key             string
	DisplayName     string
	ScripName       string
	SpotSymbol      string
	ExchangeSegment string
	SpotSegment     string
}

// ScripNameOrKey returns the pSymbolName to match.
func (u Underlying) ScripNameOrKey() string {
	if strings.TrimSpace(u.ScripName) != "" {
		return u.ScripName
	}
	return u.Key
}

// FeedSymbol returns the Quotes/WS name for the spot row.
func (u Underlying) FeedSymbol() string {
	if strings.TrimSpace(u.SpotSymbol) != "" {
		return u.SpotSymbol
	}
	return u.ScripNameOrKey()
}

// ParseConfig controls one segment parse.
type ParseConfig struct {
	Underlyings []Underlying
	// ExpiryOffset compensates for the broker's F&O epoch offset. It is only
	// used when the pTrdSymbol month does not resolve the ambiguity.
	ExpiryOffset time.Duration
	// Now is used only for validation diagnostics.
	Now time.Time
}

// SegmentData is the parsed result of one CSV file.
type SegmentData struct {
	Segment string
	Options []Option
	Spots   []Spot
	Rows    int
	Skipped int
}

// SyncRecord is one audited Scrip Master download.
type SyncRecord struct {
	Segment  string
	FileURL  string
	Rows     int
	SyncedAt time.Time
	Status   string
}

// Repository persists the parsed snapshot atomically. Implementations must
// replace the previous snapshot in a single transaction.
type Repository interface {
	ReplaceScripSnapshot(ctx context.Context, options []Option, spots []Spot, syncs []SyncRecord) error
	LoadScripSnapshot(ctx context.Context) ([]Option, []Spot, error)
}

// ExpiryInfo groups one expiry for the UI selector.
type ExpiryInfo struct {
	Date    string `json:"date"`
	Weekly  bool   `json:"weekly"`
	Monthly bool   `json:"monthly"`
	Expired bool   `json:"expired"`
}

// InstrumentKey builds the canonical option key, e.g.
// "nse_fo|NIFTY|2026-10-29|22750|CE".
func InstrumentKey(segment, underlying, expiry string, strikePaise int64, optionType OptionType) string {
	return segment + "|" + underlying + "|" + expiry + "|" + StrikeKey(strikePaise) + "|" + string(optionType)
}

// StrikeKey formats paise without trailing zeroes: 2275000 -> "22750",
// 2275050 -> "22750.5".
func StrikeKey(paise int64) string {
	if paise%100 == 0 {
		return strconv.FormatInt(paise/100, 10)
	}
	rupees := float64(paise) / 100
	return strconv.FormatFloat(rupees, 'f', -1, 64)
}

// SpotKey builds the canonical cash key, e.g. "nse_cm|NIFTY".
func SpotKey(segment, scripName string) string {
	return segment + "|" + NormalizeName(scripName)
}

// NormalizeName compares pSymbolName values case- and space-insensitively.
func NormalizeName(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), ""))
}

// NormalizeStrike converts a CSV strike to paise. Values may be written in
// scientific notation (1.75e+06).
func NormalizeStrike(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("%w: empty strike price", ErrSchema)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: strike price %q: %w", ErrSchema, raw, err)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%w: strike price %q is not finite", ErrSchema, raw)
	}
	paise := int64(math.Round(f))
	if paise <= 0 {
		return 0, fmt.Errorf("%w: strike price %q must be positive", ErrSchema, raw)
	}
	return paise, nil
}

// ParseExpiry converts the epoch-seconds column to an IST trading date and
// resolves the broker's ten-year offset using the month encoded in
// pTrdSymbol. NSE F&O timestamps are consistently ten years behind; BSE is
// not (verified against the official SDK).
func ParseExpiry(rawSeconds int64, trdSymbol string, offset time.Duration) (string, error) {
	if rawSeconds <= 0 {
		return "", fmt.Errorf("%w: expiry timestamp %d is not positive", ErrSchema, rawSeconds)
	}
	raw := time.Unix(rawSeconds, 0).In(ist)
	yy, month, ok := symbolExpiry(trdSymbol)
	candidates := []time.Time{raw, raw.Add(offset), raw.AddDate(10, 0, 0)}
	if ok {
		for _, candidate := range candidates {
			if candidate.Year() == 2000+yy && candidate.Month() == month {
				return candidate.Format("2006-01-02"), nil
			}
		}
	}
	return raw.Add(offset).Format("2006-01-02"), nil
}

var monthNames = map[string]time.Month{
	"JAN": time.January, "FEB": time.February, "MAR": time.March,
	"APR": time.April, "MAY": time.May, "JUN": time.June,
	"JUL": time.July, "AUG": time.August, "SEP": time.September,
	"OCT": time.October, "NOV": time.November, "DEC": time.December,
}

var symbolExpiryPattern = regexp.MustCompile(`(?i)^[A-Z]+(\d{2})(JAN|FEB|MAR|APR|MAY|JUN|JUL|AUG|SEP|OCT|NOV|DEC)`)

func symbolExpiry(trdSymbol string) (int, time.Month, bool) {
	m := symbolExpiryPattern.FindStringSubmatch(strings.TrimSpace(trdSymbol))
	if m == nil {
		return 0, 0, false
	}
	yy, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	month, ok := monthNames[strings.ToUpper(m[2])]
	if !ok {
		return 0, 0, false
	}
	return yy, month, true
}

// BuildExpiries returns every expiry for one underlying, ascending. The last
// expiry in a calendar month is monthly; all expiries also appear in the weekly
// list, matching the plan's de-duplication rule.
func BuildExpiries(options []Option, underlying string, now time.Time) []ExpiryInfo {
	today := now.In(ist).Format("2006-01-02")
	seen := make(map[string]bool)
	var dates []string
	for _, opt := range options {
		if opt.Underlying != underlying || seen[opt.Expiry] {
			continue
		}
		seen[opt.Expiry] = true
		dates = append(dates, opt.Expiry)
	}
	sort.Strings(dates)

	lastInMonth := make(map[string]string, len(dates))
	for _, date := range dates {
		if len(date) < 7 {
			continue
		}
		lastInMonth[date[:7]] = date
	}

	out := make([]ExpiryInfo, 0, len(dates))
	for _, date := range dates {
		out = append(out, ExpiryInfo{
			Date:    date,
			Weekly:  true,
			Monthly: len(date) >= 7 && lastInMonth[date[:7]] == date,
			Expired: date < today,
		})
	}
	return out
}

// NearestExpiry returns the first non-expired expiry.
func NearestExpiry(expiries []ExpiryInfo) (ExpiryInfo, bool) {
	for _, e := range expiries {
		if !e.Expired {
			return e, true
		}
	}
	return ExpiryInfo{}, false
}

// SortOptions sorts deterministically by underlying, expiry, strike then type.
func SortOptions(options []Option) {
	sort.Slice(options, func(i, j int) bool {
		a, b := options[i], options[j]
		if a.Underlying != b.Underlying {
			return a.Underlying < b.Underlying
		}
		if a.Expiry != b.Expiry {
			return a.Expiry < b.Expiry
		}
		if a.StrikePaise != b.StrikePaise {
			return a.StrikePaise < b.StrikePaise
		}
		return a.OptionType < b.OptionType
	})
}

// SortSpots sorts deterministically by segment then scrip name.
func SortSpots(spots []Spot) {
	sort.Slice(spots, func(i, j int) bool {
		if spots[i].ExchangeSegment != spots[j].ExchangeSegment {
			return spots[i].ExchangeSegment < spots[j].ExchangeSegment
		}
		return spots[i].InstrumentKey < spots[j].InstrumentKey
	})
}

// Validate checks an option contract before it enters the cache.
func (o Option) Validate() error {
	switch {
	case o.InstrumentKey == "":
		return fmt.Errorf("%w: option has no instrument key", ErrSchema)
	case o.ExchangeSegment == "":
		return fmt.Errorf("%w: option %s has no exchange segment", ErrSchema, o.InstrumentKey)
	case o.PSymbol == "":
		return fmt.Errorf("%w: option %s has no pSymbol", ErrSchema, o.InstrumentKey)
	case o.PTrdSymbol == "":
		return fmt.Errorf("%w: option %s has no pTrdSymbol", ErrSchema, o.InstrumentKey)
	case o.Underlying == "":
		return fmt.Errorf("%w: option %s has no underlying", ErrSchema, o.InstrumentKey)
	case o.Expiry == "":
		return fmt.Errorf("%w: option %s has no expiry", ErrSchema, o.InstrumentKey)
	case o.StrikePaise <= 0:
		return fmt.Errorf("%w: option %s has invalid strike", ErrSchema, o.InstrumentKey)
	case o.OptionType != Call && o.OptionType != Put:
		return fmt.Errorf("%w: option %s has invalid type %q", ErrSchema, o.InstrumentKey, o.OptionType)
	case o.LotSize <= 0:
		return fmt.Errorf("%w: option %s has invalid lot size", ErrSchema, o.InstrumentKey)
	}
	return nil
}

// Validate checks a spot instrument before it enters the cache.
func (s Spot) Validate() error {
	switch {
	case s.InstrumentKey == "":
		return fmt.Errorf("%w: spot has no instrument key", ErrSchema)
	case s.ExchangeSegment == "":
		return fmt.Errorf("%w: spot %s has no exchange segment", ErrSchema, s.InstrumentKey)
	case s.Symbol == "":
		return fmt.Errorf("%w: spot %s has no symbol", ErrSchema, s.InstrumentKey)
	case s.PSymbol == "":
		return fmt.Errorf("%w: spot %s has no pSymbol", ErrSchema, s.InstrumentKey)
	}
	return nil
}

// MemoryRepository is a small Repository used by tests and by cache-only mode.
type MemoryRepository struct {
	mu      sync.RWMutex
	options []Option
	spots   []Spot
	syncs   []SyncRecord
}

// ReplaceScripSnapshot implements Repository.
func (m *MemoryRepository) ReplaceScripSnapshot(_ context.Context, options []Option, spots []Spot, syncs []SyncRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.options = append([]Option(nil), options...)
	m.spots = append([]Spot(nil), spots...)
	m.syncs = append([]SyncRecord(nil), syncs...)
	return nil
}

// LoadScripSnapshot implements Repository.
func (m *MemoryRepository) LoadScripSnapshot(context.Context) ([]Option, []Spot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Option(nil), m.options...), append([]Spot(nil), m.spots...), nil
}

// Syncs returns the last recorded sync audit rows.
func (m *MemoryRepository) Syncs() []SyncRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]SyncRecord(nil), m.syncs...)
}
