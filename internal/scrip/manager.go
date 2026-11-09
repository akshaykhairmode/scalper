package scrip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// defaultSegmentPause spaces Scrip Master segment downloads apart so a refresh
// on a memory-constrained host does not compete with request handling.
const defaultSegmentPause = 2 * time.Second

// ManagerConfig wires the Scrip Master manager.
type ManagerConfig struct {
	Segments    []string
	Underlyings []Underlying
	Client      *Client
	Repository  Repository
	CacheDir    string
	Logger      *slog.Logger
	Now         func() time.Time
}

// Status is a snapshot of the manager's health for the UI and logs.
type Status struct {
	Loaded    bool      `json:"loaded"`
	Source    string    `json:"source"`
	Options   int       `json:"options"`
	Spots     int       `json:"spots"`
	SyncedAt  time.Time `json:"synced_at"`
	LastError string    `json:"last_error,omitempty"`
}

type snapshot struct {
	options             []Option
	spots               []Spot
	byKey               map[string]Option
	byPSymbol           map[string]Option
	byTrdSymbol         map[string]Option
	byUnderlyingExpiry  map[string][]Option
	optionsByUnderlying map[string][]Option
	expiryDates         map[string][]string
	spotByKey           map[string]Spot
	spotByFeed          map[string]Spot
	syncedAt            time.Time
	source              string
}

// Manager owns the parsed snapshot and performs refresh/cache/persist.
type Manager struct {
	segments     []string
	underlyings  []Underlying
	client       *Client
	repo         Repository
	cacheDir     string
	log          *slog.Logger
	now          func() time.Time
	segmentPause time.Duration

	mu      sync.RWMutex
	snap    *snapshot
	lastErr error
}

// NewManager validates the configuration and returns an empty manager.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	segments, err := normalizeSegments(cfg.Segments)
	if err != nil {
		return nil, err
	}
	if len(cfg.Underlyings) == 0 {
		return nil, errors.New("scrip: at least one underlying is required")
	}
	if _, err := indexUnderlyings(cfg.Underlyings); err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		segments:     segments,
		underlyings:  append([]Underlying(nil), cfg.Underlyings...),
		client:       cfg.Client,
		repo:         cfg.Repository,
		cacheDir:     strings.TrimSpace(cfg.CacheDir),
		log:          log,
		now:          now,
		segmentPause: defaultSegmentPause,
	}, nil
}

func normalizeSegments(list []string) ([]string, error) {
	allowed := map[string]bool{
		SegmentNSEFO: true, SegmentBSEFO: true, SegmentNSECM: true, SegmentBSECM: true,
		SegmentMCXFO: true,
	}
	seen := make(map[string]bool, len(list))
	var out []string
	for _, s := range list {
		s = strings.ToLower(strings.TrimSpace(s))
		if !allowed[s] {
			return nil, fmt.Errorf("scrip: unsupported segment %q", s)
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errors.New("scrip: at least one segment is required")
	}
	sort.Strings(out)
	return out, nil
}

// Load restores the persisted snapshot, falling back to the CSV cache when the
// database is empty. It never discards a loaded snapshot on a partial failure.
func (m *Manager) Load(ctx context.Context) error {
	var loadErrs []error
	if m.repo != nil {
		options, spots, err := m.repo.LoadScripSnapshot(ctx)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("scrip: load from database: %w", err))
		} else if len(options) > 0 || len(spots) > 0 {
			m.apply(options, spots, time.Time{}, "database")
			m.log.Info("scrip snapshot loaded",
				"event", "scrip_loaded", "source", "database",
				"options", len(options), "spots", len(spots))
			return nil
		}
	}

	if m.cacheDir == "" {
		m.setLastErr(errors.Join(loadErrs...))
		return errors.Join(loadErrs...)
	}

	options, spots, cacheErrs := m.loadCache()
	loadErrs = append(loadErrs, cacheErrs...)
	if len(options) > 0 || len(spots) > 0 {
		m.apply(options, spots, time.Time{}, "cache")
		m.log.Info("scrip snapshot loaded",
			"event", "scrip_loaded", "source", "cache",
			"options", len(options), "spots", len(spots))
	} else if len(loadErrs) == 0 {
		m.log.Info("scrip snapshot is empty", "event", "scrip_empty")
	}
	m.setLastErr(errors.Join(loadErrs...))
	return errors.Join(loadErrs...)
}

func (m *Manager) loadCache() ([]Option, []Spot, []error) {
	var options []Option
	var spots []Spot
	var errsOut []error
	for _, segment := range m.segments {
		data, err := os.ReadFile(m.cachePath(segment))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errsOut = append(errsOut, fmt.Errorf("scrip: read cache %s: %w", segment, err))
			}
			continue
		}
		parsed, err := ParseSegment(segment, bytes.NewReader(data), ParseConfig{
			Underlyings:  m.underlyings,
			ExpiryOffset: ExpiryOffsetFor(segment),
			Now:          m.now(),
		})
		if err != nil {
			errsOut = append(errsOut, fmt.Errorf("scrip: parse cache %s: %w", segment, err))
			continue
		}
		options = append(options, parsed.Options...)
		spots = append(spots, parsed.Spots...)
	}
	SortOptions(options)
	SortSpots(spots)
	return options, spots, errsOut
}

// Refresh downloads every configured segment and atomically swaps the
// snapshot. Any download or parse error leaves the previous snapshot intact.
func (m *Manager) Refresh(ctx context.Context, baseURL string) error {
	if m.client == nil {
		err := errors.New("scrip: refresh requires an HTTP client")
		m.setLastErr(err)
		return err
	}
	started := m.now()
	listing, err := m.client.ListFiles(ctx, baseURL)
	if err != nil {
		wrapped := fmt.Errorf("scrip: list files: %w", err)
		m.setLastErr(wrapped)
		m.log.Error("scrip refresh failed",
			"event", "scrip_refresh_failed", "stage", "list", "error", err)
		return wrapped
	}
	files, err := SelectFiles(listing.Paths, m.segments)
	if err != nil {
		m.setLastErr(err)
		m.log.Error("scrip refresh failed",
			"event", "scrip_refresh_failed", "stage", "select", "error", err)
		return err
	}

	// Release allocations retained by the startup snapshot load before the
	// streamed refresh starts building new maps.
	m.freeMemory()

	var options []Option
	var spots []Spot
	syncs := make([]SyncRecord, 0, len(m.segments))
	for i, segment := range m.segments {
		if i > 0 {
			if err := m.stagger(ctx); err != nil {
				m.setLastErr(err)
				return fmt.Errorf("scrip: refresh interrupted: %w", err)
			}
		}
		fileURL := files[segment]
		parsed, err := m.parseSegmentStream(ctx, segment, fileURL)
		if err != nil {
			wrapped := fmt.Errorf("scrip: refresh %s: %w", segment, err)
			m.setLastErr(wrapped)
			m.log.Error("scrip refresh failed",
				"event", "scrip_refresh_failed", "segment", segment, "error", err)
			return wrapped
		}
		options = append(options, parsed.Options...)
		spots = append(spots, parsed.Spots...)
		syncs = append(syncs, SyncRecord{
			Segment:  segment,
			FileURL:  fileURL,
			Rows:     parsed.Rows,
			SyncedAt: started,
			Status:   "ok",
		})
		m.log.Info("scrip segment parsed",
			"event", "scrip_segment_parsed", "segment", segment,
			"rows", parsed.Rows, "skipped", parsed.Skipped,
			"options", len(parsed.Options), "spots", len(parsed.Spots))
		m.freeMemory()
	}
	SortOptions(options)
	SortSpots(spots)
	if m.repo != nil {
		if err := m.repo.ReplaceScripSnapshot(ctx, options, spots, syncs); err != nil {
			wrapped := fmt.Errorf("scrip: persist snapshot: %w", err)
			m.setLastErr(wrapped)
			m.log.Error("scrip refresh failed",
				"event", "scrip_refresh_failed", "stage", "persist", "error", err)
			return wrapped
		}
	}
	m.apply(options, spots, started, "live")
	m.setLastErr(nil)
	m.log.Info("scrip refresh complete",
		"event", "scrip_refreshed", "options", len(options), "spots", len(spots),
		"segments", len(m.segments), "duration_ms", m.now().Sub(started).Milliseconds())
	return nil
}

// parseSegmentStream downloads and parses one CSV without buffering the whole
// file. A configured cache is filled best-effort through a tee reader; cache
// failures never fail the refresh.
func (m *Manager) parseSegmentStream(ctx context.Context, segment, fileURL string) (SegmentData, error) {
	body, err := m.client.OpenCSV(ctx, fileURL)
	if err != nil {
		return SegmentData{}, fmt.Errorf("download: %w", err)
	}
	defer func() {
		if cerr := body.Close(); cerr != nil {
			m.log.Debug("failed to close scrip CSV stream",
				"event", "scrip_csv_close_failed", "segment", segment, "error", cerr)
		}
	}()

	var reader io.Reader = body
	var tee *bestEffortTee
	var tmp *os.File
	var tmpName string
	if m.cacheDir != "" {
		if err := os.MkdirAll(m.cacheDir, 0o750); err != nil {
			m.log.Warn("scrip cache directory unavailable",
				"event", "scrip_cache_dir_failed", "segment", segment, "error", err)
		} else if f, ferr := os.CreateTemp(m.cacheDir, ".scrip-*.tmp"); ferr != nil {
			m.log.Warn("scrip cache temp file failed",
				"event", "scrip_cache_temp_failed", "segment", segment, "error", ferr)
		} else {
			tmp, tmpName = f, f.Name()
			if cerr := tmp.Chmod(0o640); cerr != nil {
				m.log.Warn("scrip cache chmod failed",
					"event", "scrip_cache_chmod_failed", "segment", segment, "error", cerr)
				_ = tmp.Close()
				_ = os.Remove(tmpName)
				tmp, tmpName = nil, ""
			} else {
				tee = &bestEffortTee{r: body, w: tmp}
				reader = tee
			}
		}
	}

	parsed, perr := ParseSegment(segment, reader, ParseConfig{
		Underlyings:  m.underlyings,
		ExpiryOffset: ExpiryOffsetFor(segment),
		Now:          m.now(),
	})

	if tmp != nil {
		cacheOK := perr == nil && tee != nil && !tee.failed
		if cacheOK {
			if err := tmp.Sync(); err != nil {
				m.log.Warn("scrip cache sync failed",
					"event", "scrip_cache_write_failed", "segment", segment, "error", err)
				cacheOK = false
			}
		}
		if err := tmp.Close(); err != nil && cacheOK {
			m.log.Warn("scrip cache close failed",
				"event", "scrip_cache_write_failed", "segment", segment, "error", err)
			cacheOK = false
		}
		if cacheOK {
			if err := os.Rename(tmpName, m.cachePath(segment)); err != nil {
				m.log.Warn("scrip cache rename failed",
					"event", "scrip_cache_write_failed", "segment", segment, "error", err)
				cacheOK = false
			}
		}
		if !cacheOK {
			if err := os.Remove(tmpName); err != nil && !errors.Is(err, fs.ErrNotExist) {
				m.log.Debug("failed to remove scrip cache temp file",
					"event", "scrip_cache_temp_remove_failed", "error", err)
			}
		}
	}

	if perr != nil {
		return SegmentData{}, fmt.Errorf("parse: %w", perr)
	}
	return parsed, nil
}

// bestEffortTee copies CSV bytes to the cache file but swallows write errors so
// a cache problem cannot abort the in-memory refresh.
type bestEffortTee struct {
	r      io.Reader
	w      io.Writer
	failed bool
}

func (t *bestEffortTee) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 && !t.failed {
		if _, werr := t.w.Write(p[:n]); werr != nil {
			t.failed = true
		}
	}
	return n, err
}

// stagger yields briefly between segment downloads, and aborts promptly when
// the application is shutting down.
func (m *Manager) stagger(ctx context.Context) error {
	if m.segmentPause <= 0 {
		return nil
	}
	timer := time.NewTimer(m.segmentPause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// freeMemory returns unused heap pages to the OS, which matters on the small
// single-user hosts this terminal targets.
func (m *Manager) freeMemory() {
	debug.FreeOSMemory()
}

func (m *Manager) apply(options []Option, spots []Spot, syncedAt time.Time, source string) {
	snap := &snapshot{
		options:             append([]Option(nil), options...),
		spots:               append([]Spot(nil), spots...),
		byKey:               make(map[string]Option, len(options)),
		byPSymbol:           make(map[string]Option, len(options)),
		byTrdSymbol:         make(map[string]Option, len(options)),
		byUnderlyingExpiry:  make(map[string][]Option),
		optionsByUnderlying: make(map[string][]Option),
		expiryDates:         make(map[string][]string),
		spotByKey:           make(map[string]Spot, len(spots)),
		spotByFeed:          make(map[string]Spot, len(spots)),
		syncedAt:            syncedAt,
		source:              source,
	}
	for _, opt := range snap.options {
		snap.byKey[opt.InstrumentKey] = opt
		snap.byPSymbol[opt.ExchangeSegment+"|"+opt.PSymbol] = opt
		snap.byTrdSymbol[opt.ExchangeSegment+"|"+opt.PTrdSymbol] = opt
		ueKey := opt.Underlying + "|" + opt.Expiry
		snap.byUnderlyingExpiry[ueKey] = append(snap.byUnderlyingExpiry[ueKey], opt)
		snap.optionsByUnderlying[opt.Underlying] = append(snap.optionsByUnderlying[opt.Underlying], opt)
	}
	expirySets := make(map[string]map[string]struct{})
	for _, opt := range snap.options {
		set := expirySets[opt.Underlying]
		if set == nil {
			set = make(map[string]struct{})
			expirySets[opt.Underlying] = set
		}
		set[opt.Expiry] = struct{}{}
	}
	for underlying, set := range expirySets {
		dates := make([]string, 0, len(set))
		for date := range set {
			dates = append(dates, date)
		}
		sort.Strings(dates)
		snap.expiryDates[underlying] = dates
	}
	for _, spot := range snap.spots {
		snap.spotByKey[spot.InstrumentKey] = spot
		snap.spotByFeed[spot.ExchangeSegment+"|"+NormalizeName(spot.PSymbol)] = spot
		scripName := spot.InstrumentKey
		if i := strings.LastIndex(scripName, "|"); i >= 0 {
			scripName = scripName[i+1:]
		}
		snap.spotByFeed[spot.ExchangeSegment+"|"+NormalizeName(spot.Symbol)] = spot
		snap.spotByFeed[spot.ExchangeSegment+"|"+NormalizeName(scripName)] = spot
	}
	m.mu.Lock()
	m.snap = snap
	m.mu.Unlock()
}

func (m *Manager) setLastErr(err error) {
	m.mu.Lock()
	m.lastErr = err
	m.mu.Unlock()
}

// Status returns a copy of the manager's health.
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := Status{Source: "none"}
	if m.lastErr != nil {
		status.LastError = m.lastErr.Error()
	}
	if m.snap != nil {
		status.Loaded = true
		status.Source = m.snap.source
		status.Options = len(m.snap.options)
		status.Spots = len(m.snap.spots)
		status.SyncedAt = m.snap.syncedAt
	}
	return status
}

// Option returns a contract by canonical key.
func (m *Manager) Option(key string) (Option, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return Option{}, false
	}
	opt, ok := m.snap.byKey[key]
	return opt, ok
}

// FindOption resolves a contract by pSymbol or pTrdSymbol within a segment.
func (m *Manager) FindOption(segment, symbol string) (Option, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return Option{}, false
	}
	key := segment + "|" + strings.TrimSpace(symbol)
	if opt, ok := m.snap.byPSymbol[key]; ok {
		return opt, true
	}
	opt, ok := m.snap.byTrdSymbol[key]
	return opt, ok
}

// Options returns every contract for one underlying and expiry, ordered by
// strike and option type.
func (m *Manager) Options(underlying, expiry string) []Option {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return nil
	}
	opts := m.snap.byUnderlyingExpiry[underlying+"|"+expiry]
	if len(opts) == 0 {
		return nil
	}
	out := make([]Option, len(opts))
	copy(out, opts)
	return out
}

// AllOptions returns a defensive copy of the whole option snapshot.
func (m *Manager) AllOptions() []Option {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return nil
	}
	return append([]Option(nil), m.snap.options...)
}

// Expiries returns the grouped expiry list for one underlying.
func (m *Manager) Expiries(underlying string) []ExpiryInfo {
	m.mu.RLock()
	if m.snap == nil {
		m.mu.RUnlock()
		return nil
	}
	dates := m.snap.expiryDates[underlying]
	opts := m.snap.optionsByUnderlying[underlying]
	m.mu.RUnlock()
	if len(dates) == 0 {
		// Fallback for a snapshot without the precomputed date index.
		if len(opts) == 0 {
			return nil
		}
		return BuildExpiries(opts, underlying, m.now())
	}
	return buildExpiryInfos(dates, m.now())
}

// NearestExpiry returns the first non-expired expiry for an underlying.
func (m *Manager) NearestExpiry(underlying string) (ExpiryInfo, bool) {
	return NearestExpiry(m.Expiries(underlying))
}

// Spot returns a spot instrument by canonical key.
func (m *Manager) Spot(key string) (Spot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return Spot{}, false
	}
	s, ok := m.snap.spotByKey[key]
	return s, ok
}

// FindSpot resolves a cash instrument by its Quotes/WS name or scrip name.
func (m *Manager) FindSpot(segment, symbol string) (Spot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return Spot{}, false
	}
	s, ok := m.snap.spotByFeed[segment+"|"+NormalizeName(symbol)]
	return s, ok
}

// Spots returns a defensive copy of the spot snapshot.
func (m *Manager) Spots() []Spot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return nil
	}
	return append([]Spot(nil), m.snap.spots...)
}

// Underlyings returns the configured mapping.
func (m *Manager) Underlyings() []Underlying {
	return append([]Underlying(nil), m.underlyings...)
}

// SearchResult is returned by Search.
type SearchResult struct {
	InstrumentKey   string  `json:"instrument_key"`
	ExchangeSegment string  `json:"exchange_segment"`
	PSymbol         string  `json:"p_symbol"`
	Symbol          string  `json:"symbol"`
	PTrdSymbol      string  `json:"p_trd_symbol,omitempty"`
	Underlying      string  `json:"underlying,omitempty"`
	Expiry          string  `json:"expiry,omitempty"`
	Strike          float64 `json:"strike,omitempty"`
	OptionType      string  `json:"option_type,omitempty"`
	LotSize         int64   `json:"lot_size,omitempty"`
}

// Query specifies search filters for scrip search.
type Query struct {
	Segment    string
	Symbol     string
	Expiry     string
	OptionType string
	Strike     float64
	Limit      int
}

// Search searches cached instruments in memory by segment, symbol prefix/substring, expiry, and option type.
func (m *Manager) Search(q Query) []SearchResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return nil
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}

	symUpper := strings.ToUpper(strings.TrimSpace(q.Symbol))
	segLower := strings.ToLower(strings.TrimSpace(q.Segment))
	expTrim := strings.TrimSpace(q.Expiry)
	optUpper := strings.ToUpper(strings.TrimSpace(q.OptionType))
	wantPaise := int64(0)
	if q.Strike > 0 {
		wantPaise = int64(math.Round(q.Strike * 100))
	}

	var results []SearchResult

	// Check spots if option type is not explicitly requested
	if optUpper == "" {
		for _, sp := range m.snap.spots {
			if segLower != "" && strings.ToLower(sp.ExchangeSegment) != segLower {
				continue
			}
			if symUpper != "" {
				s1 := strings.ToUpper(sp.Symbol)
				s2 := strings.ToUpper(sp.DisplayName)
				s3 := strings.ToUpper(sp.PSymbol)
				if !strings.Contains(s1, symUpper) && !strings.Contains(s2, symUpper) && !strings.Contains(s3, symUpper) {
					continue
				}
			}
			results = append(results, SearchResult{
				InstrumentKey:   sp.InstrumentKey,
				ExchangeSegment: sp.ExchangeSegment,
				PSymbol:         sp.PSymbol,
				Symbol:          sp.DisplayName,
				PTrdSymbol:      sp.Symbol,
			})
			if len(results) >= limit {
				return results
			}
		}
	}

	for _, opt := range m.snap.options {
		if segLower != "" && strings.ToLower(opt.ExchangeSegment) != segLower {
			continue
		}
		if expTrim != "" && opt.Expiry != expTrim {
			continue
		}
		if optUpper != "" && strings.ToUpper(string(opt.OptionType)) != optUpper {
			continue
		}
		if wantPaise > 0 && opt.StrikePaise != wantPaise {
			continue
		}
		if symUpper != "" {
			u := strings.ToUpper(opt.Underlying)
			trd := strings.ToUpper(opt.PTrdSymbol)
			ps := strings.ToUpper(opt.PSymbol)
			if !strings.Contains(u, symUpper) && !strings.Contains(trd, symUpper) && !strings.Contains(ps, symUpper) {
				continue
			}
		}

		results = append(results, SearchResult{
			InstrumentKey:   opt.InstrumentKey,
			ExchangeSegment: opt.ExchangeSegment,
			PSymbol:         opt.PSymbol,
			Symbol:          opt.PTrdSymbol,
			PTrdSymbol:      opt.PTrdSymbol,
			Underlying:      opt.Underlying,
			Expiry:          opt.Expiry,
			Strike:          float64(opt.StrikePaise) / 100.0,
			OptionType:      string(opt.OptionType),
			LotSize:         opt.LotSize,
		})
		if len(results) >= limit {
			return results
		}
	}

	return results
}

func (m *Manager) cachePath(segment string) string {
	return filepath.Join(m.cacheDir, segment+".csv")
}
