package scrip

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// csvColumns holds the indices of the columns we consume. Header names vary in
// whitespace and punctuation between broker releases, so they are normalised.
type csvColumns struct {
	pSymbol     int
	pExchSeg    int
	pInstType   int
	pSymbolName int
	pTrdSymbol  int
	pOptionType int
	lotSize     int
	expiry      int
	strike      int
}

var requiredColumns = []string{
	"psymbol", "pexchseg", "pinsttype", "psymbolname", "ptrdsymbol",
	"poptiontype", "llotsize", "dstrikeprice",
}

func normalizeHeader(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, ";")
	return strings.Join(strings.Fields(s), "")
}

func buildColumns(header []string) (csvColumns, error) {
	normalized := make([]string, len(header))
	index := make(map[string]int, len(header))
	for i, h := range header {
		name := normalizeHeader(h)
		normalized[i] = name
		if name == "" {
			continue
		}
		if prev, ok := index[name]; ok {
			return csvColumns{}, fmt.Errorf("%w: duplicate column %q at positions %d and %d",
				ErrSchema, name, prev, i)
		}
		index[name] = i
	}
	for _, name := range requiredColumns {
		if _, ok := index[name]; !ok {
			return csvColumns{}, fmt.Errorf("%w: missing required column %q", ErrSchema, name)
		}
	}
	expiry, ok := index["pexpirydate"]
	if !ok {
		expiry, ok = index["lexpirydate"]
	}
	if !ok {
		return csvColumns{}, fmt.Errorf("%w: missing required column %q (or %q)",
			ErrSchema, "pExpiryDate", "lExpiryDate")
	}
	return csvColumns{
		pSymbol:     index["psymbol"],
		pExchSeg:    index["pexchseg"],
		pInstType:   index["pinsttype"],
		pSymbolName: index["psymbolname"],
		pTrdSymbol:  index["ptrdsymbol"],
		pOptionType: index["poptiontype"],
		lotSize:     index["llotsize"],
		expiry:      expiry,
		strike:      index["dstrikeprice"],
	}, nil
}

func field(record []string, i int) string {
	if i < 0 || i >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[i])
}

// ParseSegment parses one Scrip Master CSV for the given exchange segment.
// Rows for other underlyings, stocks, futures and non-configured segments are
// counted as skipped, not treated as errors.
func ParseSegment(segment string, r io.Reader, cfg ParseConfig) (SegmentData, error) {
	if strings.TrimSpace(segment) == "" {
		return SegmentData{}, fmt.Errorf("%w: empty segment", ErrSchema)
	}
	underlyings, err := indexUnderlyings(cfg.Underlyings)
	if err != nil {
		return SegmentData{}, err
	}

	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = false

	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return SegmentData{}, fmt.Errorf("%w: %s: file is empty", ErrSchema, segment)
		}
		return SegmentData{}, fmt.Errorf("%w: %s: read header: %v", ErrSchema, segment, err)
	}
	cols, err := buildColumns(header)
	if err != nil {
		return SegmentData{}, fmt.Errorf("%s: %w", segment, err)
	}

	data := SegmentData{Segment: segment}
	options := make(map[string]Option)
	spots := make(map[string]Spot)
	line := 1
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			return SegmentData{}, fmt.Errorf("%w: %s line %d: %v", ErrSchema, segment, line, err)
		}
		if len(record) < len(header) {
			return SegmentData{}, fmt.Errorf("%w: %s line %d: %d fields, want %d",
				ErrSchema, segment, line, len(record), len(header))
		}
		if isBlank(record) {
			continue
		}
		data.Rows++

		rowSeg := field(record, cols.pExchSeg)
		if !strings.EqualFold(rowSeg, segment) {
			data.Skipped++
			continue
		}
		instType := strings.ToUpper(field(record, cols.pInstType))
		name := field(record, cols.pSymbolName)

		if instType == "OPTIDX" {
			opt, matched, err := parseOptionRow(segment, record, cols, name, underlyings, cfg)
			if err != nil {
				return SegmentData{}, fmt.Errorf("%w: %s line %d: %v", ErrSchema, segment, line, err)
			}
			if !matched {
				data.Skipped++
				continue
			}
			if prev, ok := options[opt.InstrumentKey]; ok {
				if prev != opt {
					return SegmentData{}, fmt.Errorf("%w: %s line %d: conflicting duplicate contract %s",
						ErrSchema, segment, line, opt.InstrumentKey)
				}
				continue
			}
			options[opt.InstrumentKey] = opt
			continue
		}

		// Cash index rows have an empty pInstType. Equity rows also do, so the
		// configured scrip-name match is what keeps them out.
		if instType == "" {
			spot, matched, err := parseSpotRow(segment, record, cols, name, underlyings)
			if err != nil {
				return SegmentData{}, fmt.Errorf("%w: %s line %d: %v", ErrSchema, segment, line, err)
			}
			if !matched {
				data.Skipped++
				continue
			}
			if prev, ok := spots[spot.InstrumentKey]; ok {
				if prev != spot {
					return SegmentData{}, fmt.Errorf("%w: %s line %d: conflicting duplicate spot %s",
						ErrSchema, segment, line, spot.InstrumentKey)
				}
				continue
			}
			spots[spot.InstrumentKey] = spot
			continue
		}

		data.Skipped++
	}

	for _, opt := range options {
		if err := opt.Validate(); err != nil {
			return SegmentData{}, err
		}
		data.Options = append(data.Options, opt)
	}
	for _, spot := range spots {
		if err := spot.Validate(); err != nil {
			return SegmentData{}, err
		}
		data.Spots = append(data.Spots, spot)
	}
	SortOptions(data.Options)
	SortSpots(data.Spots)
	return data, nil
}

func parseOptionRow(
	segment string,
	record []string,
	cols csvColumns,
	name string,
	underlyings map[string]Underlying,
	cfg ParseConfig,
) (Option, bool, error) {
	u, ok := underlyings[NormalizeName(name)]
	if !ok || u.ExchangeSegment != segment {
		return Option{}, false, nil
	}

	optType := OptionType(strings.ToUpper(field(record, cols.pOptionType)))
	if optType != Call && optType != Put {
		return Option{}, false, fmt.Errorf("option type %q is not CE or PE", optType)
	}

	lot, err := strconv.ParseInt(field(record, cols.lotSize), 10, 64)
	if err != nil || lot <= 0 {
		return Option{}, false, fmt.Errorf("lot size %q is invalid", field(record, cols.lotSize))
	}

	expirySeconds, err := strconv.ParseInt(field(record, cols.expiry), 10, 64)
	if err != nil {
		return Option{}, false, fmt.Errorf("expiry %q is not epoch seconds", field(record, cols.expiry))
	}
	trdSymbol := field(record, cols.pTrdSymbol)
	expiry, err := ParseExpiry(expirySeconds, trdSymbol, cfg.ExpiryOffset)
	if err != nil {
		return Option{}, false, err
	}

	strike, err := NormalizeStrike(field(record, cols.strike))
	if err != nil {
		return Option{}, false, err
	}

	underlying := u.Key
	opt := Option{
		InstrumentKey:   InstrumentKey(segment, underlying, expiry, strike, optType),
		ExchangeSegment: segment,
		PSymbol:         field(record, cols.pSymbol),
		PTrdSymbol:      trdSymbol,
		Underlying:      underlying,
		Expiry:          expiry,
		StrikePaise:     strike,
		OptionType:      optType,
		LotSize:         lot,
	}
	if err := opt.Validate(); err != nil {
		return Option{}, false, err
	}
	return opt, true, nil
}

func parseSpotRow(
	segment string,
	record []string,
	cols csvColumns,
	name string,
	underlyings map[string]Underlying,
) (Spot, bool, error) {
	u, ok := underlyings[NormalizeName(name)]
	if !ok || u.SpotSegment != segment {
		return Spot{}, false, nil
	}
	scripName := u.ScripNameOrKey()
	spot := Spot{
		InstrumentKey:   SpotKey(segment, scripName),
		ExchangeSegment: segment,
		Symbol:          u.FeedSymbol(),
		DisplayName:     u.DisplayName,
		PSymbol:         field(record, cols.pSymbol),
	}
	if err := spot.Validate(); err != nil {
		return Spot{}, false, err
	}
	return spot, true, nil
}

func indexUnderlyings(list []Underlying) (map[string]Underlying, error) {
	out := make(map[string]Underlying, len(list)*2)
	for _, u := range list {
		for _, name := range []string{u.Key, u.ScripNameOrKey()} {
			key := NormalizeName(name)
			if key == "" {
				continue
			}
			if prev, ok := out[key]; ok && prev.Key != u.Key {
				return nil, fmt.Errorf("%w: scrip name %q maps to both %s and %s",
					ErrSchema, name, prev.Key, u.Key)
			}
			out[key] = u
		}
	}
	return out, nil
}

func isBlank(record []string) bool {
	for _, v := range record {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// ExpiryOffsetFor returns the documented epoch offset per segment. NSE F&O is
// ten years behind in the Scrip Master; BSE is not. ParseExpiry additionally
// cross-checks pTrdSymbol, so a broker fix will not shift dates.
func ExpiryOffsetFor(segment string) time.Duration {
	if segment == SegmentNSEFO {
		return 315_511_200 * time.Second
	}
	return 0
}
