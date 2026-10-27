package scrip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

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
	options     []Option
	spots       []Spot
	byKey       map[string]Option
	byPSymbol   map[string]Option
	byTrdSymbol map[string]Option
	spotByKey   map[string]Spot
	spotByFeed  map[string]Spot
	syncedAt    time.Time
	source      string
}

// Manager owns the parsed snapshot and performs refresh/cache/persist.
type Manager struct {
	segments    []string
	underlyings []Underlying
	client      *Client
	repo        Repository
	cacheDir    string
	log         *slog.Logger
	now         func() time.Time

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
		segments:    segments,
		underlyings: append([]Underlying(nil), cfg.Underlyings...),
		client:      cfg.Client,
		repo:        cfg.Repository,
		cacheDir:    strings.TrimSpace(cfg.CacheDir),
		log:         log,
		now:         now,
	}, nil
}

func normalizeSegments(list []string) ([]string, error) {
	allowed := map[string]bool{
		SegmentNSEFO: true, SegmentBSEFO: true, SegmentNSECM: true, SegmentBSECM: true,
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

	var options []Option
	var spots []Spot
	syncs := make([]SyncRecord, 0, len(m.segments))
	for _, segment := range m.segments {
		fileURL := files[segment]
		data, err := m.client.Download(ctx, fileURL)
		if err != nil {
			wrapped := fmt.Errorf("scrip: download %s: %w", segment, err)
			m.setLastErr(wrapped)
			m.log.Error("scrip refresh failed",
				"event", "scrip_refresh_failed", "stage", "download",
				"segment", segment, "error", err)
			return wrapped
		}
		parsed, err := ParseSegment(segment, bytes.NewReader(data), ParseConfig{
			Underlyings:  m.underlyings,
			ExpiryOffset: ExpiryOffsetFor(segment),
			Now:          m.now(),
		})
		if err != nil {
			wrapped := fmt.Errorf("scrip: parse %s: %w", segment, err)
			m.setLastErr(wrapped)
			m.log.Error("scrip refresh failed",
				"event", "scrip_refresh_failed", "stage", "parse",
				"segment", segment, "error", err)
			return wrapped
		}
		if m.cacheDir != "" {
			if err := writeCacheFile(m.cachePath(segment), data); err != nil {
				// The in-memory snapshot and database remain correct; a cache
				// write failure only weakens offline startup, so it is logged
				// and does not fail the refresh.
				m.log.Warn("scrip cache write failed",
					"event", "scrip_cache_write_failed", "segment", segment, "error", err)
			}
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

func (m *Manager) apply(options []Option, spots []Spot, syncedAt time.Time, source string) {
	snap := &snapshot{
		options:     append([]Option(nil), options...),
		spots:       append([]Spot(nil), spots...),
		byKey:       make(map[string]Option, len(options)),
		byPSymbol:   make(map[string]Option, len(options)),
		byTrdSymbol: make(map[string]Option, len(options)),
		spotByKey:   make(map[string]Spot, len(spots)),
		spotByFeed:  make(map[string]Spot, len(spots)),
		syncedAt:    syncedAt,
		source:      source,
	}
	for _, opt := range snap.options {
		snap.byKey[opt.InstrumentKey] = opt
		snap.byPSymbol[opt.ExchangeSegment+"|"+opt.PSymbol] = opt
		snap.byTrdSymbol[opt.ExchangeSegment+"|"+opt.PTrdSymbol] = opt
	}
	for _, spot := range snap.spots {
		snap.spotByKey[spot.InstrumentKey] = spot
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
	out := make([]Option, 0, 64)
	for _, opt := range m.snap.options {
		if opt.Underlying == underlying && opt.Expiry == expiry {
			out = append(out, opt)
		}
	}
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
	return BuildExpiries(m.AllOptions(), underlying, m.now())
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

func (m *Manager) cachePath(segment string) string {
	return filepath.Join(m.cacheDir, segment+".csv")
}

// writeCacheFile writes atomically so a crash never leaves a truncated CSV in
// place of a previously good cache.
func writeCacheFile(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".scrip-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			if rerr := os.Remove(tmpName); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove temp cache %s: %w", tmpName, rerr))
			}
		}
	}()
	if err = tmp.Chmod(0o640); err != nil {
		cerr := tmp.Close()
		return errors.Join(fmt.Errorf("chmod temp cache: %w", err), cerr)
	}
	if _, err = tmp.Write(data); err != nil {
		cerr := tmp.Close()
		return errors.Join(fmt.Errorf("write temp cache: %w", err), cerr)
	}
	if err = tmp.Sync(); err != nil {
		cerr := tmp.Close()
		return errors.Join(fmt.Errorf("sync temp cache: %w", err), cerr)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp cache: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp cache: %w", err)
	}
	return nil
}
