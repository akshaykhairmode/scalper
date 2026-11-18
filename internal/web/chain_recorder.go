package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/store"
)

// chainSnapshotStore is the narrow persistence contract behind chain
// recording; it is satisfied by *store.Store.
type chainSnapshotStore interface {
	SaveChainSnapshot(ctx context.Context, snap store.ChainSnapshot) (int64, error)
	ReadChainSnapshots(ctx context.Context, underlying, expiry string, from, to int64) ([]store.ChainSnapshot, error)
	PruneChainSnapshots(ctx context.Context, before int64) (int64, error)
}

const (
	// chainRecordWriteTimeout bounds the detached snapshot write. The browser
	// request may be cancelled as soon as its response is written, so the write
	// cannot ride the request context directly.
	chainRecordWriteTimeout = 5 * time.Second

	// chainRecordKeyCap bounds the dedupe maps. Entries older than the min
	// interval are evicted once the cap is exceeded; the live key set is
	// underlyings x expiries, so this only guards a long-running process
	// against accumulation across many expiries.
	chainRecordKeyCap = 512

	// chainPruneTimeout bounds one prune pass, mirroring candle retention.
	chainPruneTimeout = 30 * time.Second

	// Defaults used when a hand-edited config leaves a value unset; Load
	// normally fills them from config.Default.
	defaultChainRecordMinIntervalSec = 55
	defaultChainRecordRetentionDays  = 90
)

// chainISTZone is the exchange timezone. India has no DST, so a fixed offset
// is exact for the trading-day boundary.
var chainISTZone = time.FixedZone("IST", istOffsetSeconds)

// chainCapture is one fresh strike ladder the server has already fetched,
// ready to be considered for recording.
type chainCapture struct {
	Underlying string
	Expiry     string
	Spot       float64
	Rows       []market.ChainRow
	Stats      chainStatsPayload
	Full       bool
}

// chainRecorder persists option-chain snapshots from ladders the server has
// ALREADY fetched while serving /api/chain and /api/chain/stats. It never
// triggers an upstream call itself.
//
// Dedupe: each (underlying, expiry) records at most one snapshot per
// minInterval. A full-ladder capture (handleChain without a strike window, or
// handleChainStats) supersedes a recent windowed one: a windowed capture is
// skipped when any capture is recent, while a full capture only dedupes
// against earlier full captures. When both endpoints are polled in the same
// minute the richer full-chain snapshot therefore wins, and multiple browser
// tabs still collapse into one write. Windowed snapshots carry summary values
// (PCR, max pain, walls) computed over the visible window only; readers can
// tell the two apart by the header's strikes count.
type chainRecorder struct {
	store         chainSnapshotStore
	log           *slog.Logger
	now           func() time.Time
	minInterval   time.Duration
	retentionDays int

	mu         sync.Mutex
	lastWindow map[string]time.Time
	lastFull   map[string]time.Time
}

func newChainRecorder(st chainSnapshotStore, log *slog.Logger, now func() time.Time, minIntervalSec, retentionDays int) *chainRecorder {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	if minIntervalSec < 1 {
		minIntervalSec = defaultChainRecordMinIntervalSec
	}
	if retentionDays < 1 {
		retentionDays = defaultChainRecordRetentionDays
	}
	return &chainRecorder{
		store:         st,
		log:           log,
		now:           now,
		minInterval:   time.Duration(minIntervalSec) * time.Second,
		retentionDays: retentionDays,
		lastWindow:    make(map[string]time.Time),
		lastFull:      make(map[string]time.Time),
	}
}

// record persists one snapshot when recording is due for the (underlying,
// expiry) pair. The mutex guards only the dedupe bookkeeping, never the
// database: the write runs on a detached context in a background goroutine,
// and a failure is logged without failing the request it piggybacks on.
func (r *chainRecorder) record(ctx context.Context, capture chainCapture) {
	if r == nil || r.store == nil || len(capture.Rows) == 0 {
		return
	}
	now := r.now()
	key := strings.ToUpper(strings.TrimSpace(capture.Underlying)) + "\x00" + strings.TrimSpace(capture.Expiry)

	r.mu.Lock()
	if capture.Full {
		if now.Sub(r.lastFull[key]) < r.minInterval {
			r.mu.Unlock()
			return
		}
		r.lastFull[key] = now
	} else {
		if now.Sub(r.lastFull[key]) < r.minInterval || now.Sub(r.lastWindow[key]) < r.minInterval {
			r.mu.Unlock()
			return
		}
		r.lastWindow[key] = now
	}
	if len(r.lastFull)+len(r.lastWindow) > chainRecordKeyCap {
		r.evictStaleLocked(now)
	}
	r.mu.Unlock()

	snap := r.buildSnapshot(now, capture)
	go func() {
		defer func() {
			// An auxiliary snapshot write must never take down the trading
			// process from a store-layer panic: log it and drop the snapshot.
			if rec := recover(); rec != nil {
				r.log.Error("chain snapshot write panicked",
					"event", "chain_snapshot_save_panicked", "underlying", snap.Underlying, "recover", rec)
			}
		}()
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), chainRecordWriteTimeout)
		defer cancel()
		if _, err := r.store.SaveChainSnapshot(writeCtx, snap); err != nil {
			r.log.Warn("failed to save chain snapshot", "event", "chain_snapshot_save_failed",
				"underlying", snap.Underlying, "expiry", snap.Expiry, "error", err)
		}
	}()
}

// buildSnapshot maps a captured ladder onto the persisted shape. Rows without
// a call or a put contract are still recorded; the missing side stays zero.
func (r *chainRecorder) buildSnapshot(now time.Time, capture chainCapture) store.ChainSnapshot {
	rows := make([]store.ChainSnapshotRow, 0, len(capture.Rows))
	for _, row := range capture.Rows {
		if row.Call == nil && row.Put == nil {
			continue
		}
		out := store.ChainSnapshotRow{Strike: row.Strike}
		if row.Call != nil {
			out.CallLTP = row.Call.LTP
			out.CallOI = row.Call.OI
			out.CallOIChange = row.Call.OIChange
			out.CallVolume = row.Call.Volume
		}
		if row.Put != nil {
			out.PutLTP = row.Put.LTP
			out.PutOI = row.Put.OI
			out.PutOIChange = row.Put.OIChange
			out.PutVolume = row.Put.Volume
		}
		rows = append(rows, out)
	}
	return store.ChainSnapshot{
		TradingDay: now.In(chainISTZone).Format("2006-01-02"),
		CapturedAt: now.Unix(),
		Underlying: strings.ToUpper(strings.TrimSpace(capture.Underlying)),
		Expiry:     strings.TrimSpace(capture.Expiry),
		Spot:       capture.Spot,
		PCR:        capture.Stats.PCR,
		MaxPain:    capture.Stats.MaxPain,
		CallOIWall: capture.Stats.CallOIWall,
		PutOIWall:  capture.Stats.PutOIWall,
		Strikes:    len(rows),
		Rows:       rows,
	}
}

func (r *chainRecorder) evictStaleLocked(now time.Time) {
	for key, at := range r.lastFull {
		if now.Sub(at) > r.minInterval {
			delete(r.lastFull, key)
		}
	}
	for key, at := range r.lastWindow {
		if now.Sub(at) > r.minInterval {
			delete(r.lastWindow, key)
		}
	}
}

// read returns stored snapshots for the history endpoint.
func (r *chainRecorder) read(ctx context.Context, underlying, expiry string, from, to int64) ([]store.ChainSnapshot, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("web: chain snapshot store is not configured")
	}
	return r.store.ReadChainSnapshots(ctx, underlying, expiry, from, to)
}

// Run prunes expired snapshots hourly, on the same cadence as candle
// retention but with the chain recorder's own retention days.
func (r *chainRecorder) Run(ctx context.Context) {
	if r == nil || r.store == nil || r.retentionDays <= 0 {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruneCtx, cancel := context.WithTimeout(ctx, chainPruneTimeout)
			r.prune(pruneCtx)
			cancel()
		}
	}
}

func (r *chainRecorder) prune(ctx context.Context) {
	if r == nil || r.store == nil || r.retentionDays <= 0 {
		return
	}
	before := r.now().AddDate(0, 0, -r.retentionDays).Unix()
	rows, err := r.store.PruneChainSnapshots(ctx, before)
	if err != nil {
		r.log.Warn("failed to prune old chain snapshots", "event", "chain_snapshot_prune_failed", "error", err)
		return
	}
	if rows > 0 {
		r.log.Info("pruned old chain snapshots", "event", "chain_snapshot_pruned",
			"rows", rows, "retention_days", r.retentionDays)
	}
}

// recordChainSnapshot persists a chain ladder the server has already fetched
// while serving an API request. It makes no upstream call, records only while
// the market is open, and never fails the request it piggybacks on.
func (s *Server) recordChainSnapshot(ctx context.Context, underlying, expiry string, rows []market.ChainRow, full bool) {
	if s.chainRecorder == nil || len(rows) == 0 {
		return
	}
	if !s.isMarketOpen(s.now()) {
		return
	}
	now := s.now()
	s.chainRecorder.record(ctx, chainCapture{
		Underlying: underlying,
		Expiry:     expiry,
		Spot:       s.cachedSpot(underlying),
		Rows:       rows,
		Stats:      chainStatsFromRows(rows, now.UnixMilli()),
		Full:       full,
	})
}

// cachedSpot returns the latest in-memory index LTP for an underlying from the
// indices payload cache the server already maintains. It never calls the
// broker; a cache miss records spot 0.
func (s *Server) cachedSpot(underlying string) float64 {
	s.indicesMu.RLock()
	raw := s.indicesRaw
	s.indicesMu.RUnlock()
	if len(raw) == 0 {
		return 0
	}
	var cached struct {
		Indices []market.IndexQuote `json:"indices"`
	}
	if err := json.Unmarshal(raw, &cached); err != nil {
		return 0
	}
	for _, idx := range cached.Indices {
		if idx.LTP > 0 &&
			(strings.EqualFold(idx.Key, underlying) || strings.EqualFold(idx.DisplayName, underlying)) {
			return idx.LTP
		}
	}
	return 0
}

// istDayStart returns the Unix timestamp of midnight IST on the calendar day
// that contains ts. India has no DST, so the fixed offset is exact.
func istDayStart(ts int64) int64 {
	shifted := ts + istOffsetSeconds
	remainder := shifted % 86400
	if remainder < 0 {
		remainder += 86400
	}
	return shifted - remainder - istOffsetSeconds
}

// handleChainHistory serves recorded chain snapshots for a backtester UI:
// GET /api/chain/history?index=&expiry=&from=&to= (unix seconds). from and to
// are optional and default to the start of today IST and now.
func (s *Server) handleChainHistory(w http.ResponseWriter, r *http.Request) {
	if s.chainRecorder == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "chain snapshot history is not configured", nil)
		return
	}
	index := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("index")))
	expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))
	if index == "" || expiry == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "index and expiry are required", nil)
		return
	}

	now := s.now()
	from := istDayStart(now.Unix())
	to := now.Unix()
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			s.writeRouteError(w, r, http.StatusBadRequest, "from must be a unix timestamp in seconds", nil)
			return
		}
		from = v
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			s.writeRouteError(w, r, http.StatusBadRequest, "to must be a unix timestamp in seconds", nil)
			return
		}
		to = v
	}
	if from > to {
		s.writeRouteError(w, r, http.StatusBadRequest, "from must not be after to", nil)
		return
	}

	snapshots, err := s.chainRecorder.read(r.Context(), index, expiry, from, to)
	if err != nil {
		s.writeRouteError(w, r, http.StatusInternalServerError, "read chain snapshot history failed", err)
		return
	}
	if snapshots == nil {
		snapshots = []store.ChainSnapshot{}
	}
	s.writeRouteJSON(w, r, http.StatusOK, struct {
		Snapshots []store.ChainSnapshot `json:"snapshots"`
		Truncated bool                  `json:"truncated,omitempty"`
	}{
		Snapshots: snapshots,
		Truncated: len(snapshots) >= store.MaxReadChainSnapshots,
	})
}
