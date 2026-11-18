package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/store"
)

// marketOpenNow is a Wednesday 10:00 IST inside the default trading session.
var marketOpenNow = time.Date(2026, 11, 18, 10, 0, 0, 0, chainISTZone)

func testChainRows() []market.ChainRow {
	return []market.ChainRow{
		{
			Strike: 24900,
			Call:   &market.OptionQuote{LTP: 210.5, OI: 1200, OIChange: 300, Volume: 5000},
			Put:    &market.OptionQuote{LTP: 12.4, OI: 800, OIChange: -100, Volume: 2500},
		},
		{
			Strike: 25000,
			Call:   &market.OptionQuote{LTP: 120.25, OI: 5000, OIChange: 1200, Volume: 9000},
			Put:    &market.OptionQuote{LTP: 40.1, OI: 3000, OIChange: 500, Volume: 7000},
		},
		{
			Strike: 25100,
			Call:   &market.OptionQuote{LTP: 60.5, OI: 900, OIChange: -50, Volume: 3000},
			Put:    &market.OptionQuote{LTP: 95.2, OI: 4200, OIChange: 900, Volume: 8800},
		},
	}
}

type recordingMarketAPI struct {
	fakeMarketAPI
	fullCalls   int
	windowCalls int
}

func (f *recordingMarketAPI) Chain(context.Context, string, string, string) ([]market.ChainRow, error) {
	f.fullCalls++
	return testChainRows(), nil
}

func (f *recordingMarketAPI) ChainWindow(context.Context, string, string, string, float64, int, string) (market.ChainPage, error) {
	f.windowCalls++
	return market.ChainPage{Rows: testChainRows()}, nil
}

// fakeChainSnapshotStore captures recorder writes and signals each save.
type fakeChainSnapshotStore struct {
	fakeDB
	savedAt chan struct{}

	mu        sync.Mutex
	saved     []store.ChainSnapshot
	readCalls []readCall
	prunes    []int64
	pruneRows int64
}

type readCall struct {
	underlying string
	expiry     string
	from       int64
	to         int64
}

func newFakeChainSnapshotStore() *fakeChainSnapshotStore {
	return &fakeChainSnapshotStore{savedAt: make(chan struct{}, 64)}
}

func (f *fakeChainSnapshotStore) SaveChainSnapshot(_ context.Context, snap store.ChainSnapshot) (int64, error) {
	f.mu.Lock()
	f.saved = append(f.saved, snap)
	count := int64(len(f.saved))
	f.mu.Unlock()
	f.savedAt <- struct{}{}
	return count, nil
}

func (f *fakeChainSnapshotStore) ReadChainSnapshots(_ context.Context, underlying, expiry string, from, to int64) ([]store.ChainSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readCalls = append(f.readCalls, readCall{underlying: underlying, expiry: expiry, from: from, to: to})
	var out []store.ChainSnapshot
	for _, snap := range f.saved {
		if snap.Underlying == underlying && snap.Expiry == expiry && snap.CapturedAt >= from && snap.CapturedAt <= to {
			out = append(out, snap)
		}
	}
	return out, nil
}

func (f *fakeChainSnapshotStore) PruneChainSnapshots(_ context.Context, before int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prunes = append(f.prunes, before)
	var kept []store.ChainSnapshot
	for _, snap := range f.saved {
		if snap.CapturedAt >= before {
			kept = append(kept, snap)
		} else {
			f.pruneRows++
		}
	}
	f.saved = kept
	return f.pruneRows, nil
}

func (f *fakeChainSnapshotStore) snapshotCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

func (f *fakeChainSnapshotStore) lastSnapshot() store.ChainSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saved) == 0 {
		return store.ChainSnapshot{}
	}
	return f.saved[len(f.saved)-1]
}

func (f *fakeChainSnapshotStore) lastRead() readCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.readCalls) == 0 {
		return readCall{}
	}
	return f.readCalls[len(f.readCalls)-1]
}

// expectSave waits for one recorder write and returns its position.
func expectSave(t *testing.T, st *fakeChainSnapshotStore) {
	t.Helper()
	select {
	case <-st.savedAt:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a chain snapshot save")
	}
}

// expectNoSave asserts no write lands within a short bound.
func expectNoSave(t *testing.T, st *fakeChainSnapshotStore) {
	t.Helper()
	select {
	case <-st.savedAt:
		t.Fatal("unexpected chain snapshot save")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestChainRecorderDedupeAndPrecedence(t *testing.T) {
	st := newFakeChainSnapshotStore()
	clock := marketOpenNow
	r := newChainRecorder(st, testLogger(), func() time.Time { return clock }, 55, 90)

	rows := testChainRows()
	stats := chainStatsFromRows(rows, clock.UnixMilli())
	capture := chainCapture{
		Underlying: " nifty ",
		Expiry:     " 2026-11-19 ",
		Spot:       25_012.5,
		Rows:       rows,
		Stats:      stats,
	}

	r.record(context.Background(), capture)
	expectSave(t, st)
	snap := st.lastSnapshot()
	if snap.Underlying != "NIFTY" || snap.Expiry != "2026-11-19" {
		t.Fatalf("normalized keys = %q/%q, want NIFTY/2026-11-19", snap.Underlying, snap.Expiry)
	}
	if snap.TradingDay != "2026-11-18" || snap.CapturedAt != clock.Unix() {
		t.Fatalf("trading day/captured at = %q/%d, want 2026-11-18/%d", snap.TradingDay, snap.CapturedAt, clock.Unix())
	}
	if snap.Spot != 25_012.5 || snap.PCR != stats.PCR || snap.MaxPain != stats.MaxPain ||
		snap.CallOIWall != stats.CallOIWall || snap.PutOIWall != stats.PutOIWall {
		t.Fatalf("summary round trip = %+v, want spot/pcr/maxpain/walls from %+v", snap, stats)
	}
	if snap.Strikes != len(rows) || len(snap.Rows) != len(rows) {
		t.Fatalf("rows = %d (strikes %d), want %d", len(snap.Rows), snap.Strikes, len(rows))
	}
	if snap.Rows[1].CallOI != 5000 || snap.Rows[1].CallOIChange != 1200 || snap.Rows[1].PutVolume != 7000 {
		t.Fatalf("row mapping = %+v, want OI/ΔOI/volume preserved", snap.Rows[1])
	}

	// A second windowed capture inside the interval is a duplicate.
	r.record(context.Background(), capture)
	expectNoSave(t, st)

	// A full capture right after a windowed one still records: the complete
	// ladder supersedes the window, and the stats poll must not be starved.
	capture.Full = true
	r.record(context.Background(), capture)
	expectSave(t, st)

	// A second full capture inside the interval is a duplicate.
	r.record(context.Background(), capture)
	expectNoSave(t, st)

	// The windowed bucket is independent: past the interval it records again.
	clock = marketOpenNow.Add(56 * time.Second)
	capture.Full = false
	r.record(context.Background(), capture)
	expectSave(t, st)
	if st.snapshotCount() != 3 {
		t.Fatalf("saved = %d snapshots, want 3", st.snapshotCount())
	}
}

func TestChainRecorderSkipsEmptySidedRows(t *testing.T) {
	st := newFakeChainSnapshotStore()
	clock := marketOpenNow
	r := newChainRecorder(st, testLogger(), func() time.Time { return clock }, 55, 90)

	rows := testChainRows()
	rows = append(rows, market.ChainRow{Strike: 999}) // no CE and no PE contract
	stats := chainStatsFromRows(rows, clock.UnixMilli())
	r.record(context.Background(), chainCapture{
		Underlying: "NIFTY", Expiry: "2026-11-19", Rows: rows, Stats: stats, Full: true,
	})
	expectSave(t, st)

	snap := st.lastSnapshot()
	if snap.Strikes != 3 || len(snap.Rows) != 3 {
		t.Fatalf("rows = %d (strikes %d), want the 3 contract-bearing strikes", len(snap.Rows), snap.Strikes)
	}
}

func TestChainRecorderPruneUsesRetention(t *testing.T) {
	st := newFakeChainSnapshotStore()
	// A capture from 91 days ago must fall outside the 90-day retention.
	old := marketOpenNow.AddDate(0, 0, -91)
	clock := old
	r := newChainRecorder(st, testLogger(), func() time.Time { return clock }, 55, 90)

	r.record(context.Background(), chainCapture{
		Underlying: "NIFTY", Expiry: "2026-08-20", Rows: testChainRows(),
		Stats: chainStatsFromRows(testChainRows(), old.UnixMilli()), Full: true,
	})
	expectSave(t, st)

	clock = marketOpenNow
	r.prune(context.Background())
	wantBefore := marketOpenNow.AddDate(0, 0, -90).Unix()
	if len(st.prunes) != 1 || st.prunes[0] != wantBefore {
		t.Fatalf("prunes = %v, want one pass before %d", st.prunes, wantBefore)
	}
	if st.snapshotCount() != 0 {
		t.Fatalf("snapshots after prune = %d, want 0", st.snapshotCount())
	}
}

func TestHandleChainRecordsOnlyFreshFetches(t *testing.T) {
	api := &recordingMarketAPI{}
	st := newFakeChainSnapshotStore()
	s := newTestServer(t, serverOpts{marketAPI: api, db: st, now: func() time.Time { return marketOpenNow }})

	// Seed the in-memory indices cache so the snapshot can reuse its spot
	// without any upstream call.
	s.storeIndicesPayload([]byte(`{"indices":[{"key":"NIFTY","ltp":25100.25}]}`))

	req := httptest.NewRequest(http.MethodGet, "/api/chain?index=NIFTY&expiry=2026-11-19", nil)
	rec := httptest.NewRecorder()
	s.handleChain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first chain request code = %d: %s", rec.Code, rec.Body.String())
	}
	if api.fullCalls != 1 {
		t.Fatalf("upstream chain calls = %d, want 1", api.fullCalls)
	}
	expectSave(t, st)
	snap := st.lastSnapshot()
	if snap.Underlying != "NIFTY" || snap.Expiry != "2026-11-19" {
		t.Fatalf("snapshot keys = %q/%q", snap.Underlying, snap.Expiry)
	}
	if snap.Spot != 25100.25 {
		t.Fatalf("spot = %v, want the cached indices LTP 25100.25", snap.Spot)
	}
	if snap.Strikes != 3 || snap.CallOIWall != 25000 || snap.PutOIWall != 25100 {
		t.Fatalf("snapshot = %+v, want full-ladder summary (call wall 25000, put wall 25100)", snap)
	}

	// The next request is served from the chain cache: no fetch, no record.
	rec = httptest.NewRecorder()
	s.handleChain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached chain request code = %d", rec.Code)
	}
	if api.fullCalls != 1 {
		t.Fatalf("upstream chain calls after cache hit = %d, want 1", api.fullCalls)
	}
	expectNoSave(t, st)
}

func TestHandleChainWindowedRecordsAndStatsRecordsFull(t *testing.T) {
	api := &recordingMarketAPI{}
	st := newFakeChainSnapshotStore()
	s := newTestServer(t, serverOpts{marketAPI: api, db: st, now: func() time.Time { return marketOpenNow }})

	windowed := httptest.NewRequest(http.MethodGet,
		"/api/chain?index=NIFTY&expiry=2026-11-19&count=3&center=25000&direction=around", nil)
	rec := httptest.NewRecorder()
	s.handleChain(rec, windowed)
	if rec.Code != http.StatusOK {
		t.Fatalf("windowed chain request code = %d: %s", rec.Code, rec.Body.String())
	}
	expectSave(t, st)
	if st.lastSnapshot().Strikes != 3 {
		t.Fatalf("windowed snapshot strikes = %d, want 3", st.lastSnapshot().Strikes)
	}

	// The stats poll fetches the full ladder and records even though a
	// windowed snapshot was just written: the fuller capture wins.
	statsReq := httptest.NewRequest(http.MethodGet, "/api/chain/stats?index=NIFTY&expiry=2026-11-19", nil)
	rec = httptest.NewRecorder()
	s.handleChainStats(rec, statsReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats request code = %d: %s", rec.Code, rec.Body.String())
	}
	expectSave(t, st)

	// A cached stats response must not record again.
	rec = httptest.NewRecorder()
	s.handleChainStats(rec, statsReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached stats request code = %d", rec.Code)
	}
	expectNoSave(t, st)
	if got := st.snapshotCount(); got != 2 {
		t.Fatalf("saved snapshots = %d, want 2 (one windowed, one full)", got)
	}
}

func TestHandleChainSkipsRecordingWhenMarketClosed(t *testing.T) {
	api := &recordingMarketAPI{}
	st := newFakeChainSnapshotStore()
	// Wednesday 18:00 IST, after the 15:40 close.
	closed := time.Date(2026, 11, 18, 18, 0, 0, 0, chainISTZone)
	s := newTestServer(t, serverOpts{marketAPI: api, db: st, now: func() time.Time { return closed }})

	req := httptest.NewRequest(http.MethodGet, "/api/chain?index=NIFTY&expiry=2026-11-19", nil)
	rec := httptest.NewRecorder()
	s.handleChain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("closed-market chain request code = %d: %s", rec.Code, rec.Body.String())
	}
	if api.fullCalls != 1 {
		t.Fatalf("closed-market fetches = %d, want 1 (the request is still served)", api.fullCalls)
	}
	expectNoSave(t, st)

	rec = httptest.NewRecorder()
	s.handleChainStats(rec, httptest.NewRequest(http.MethodGet, "/api/chain/stats?index=NIFTY&expiry=2026-11-19", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("closed-market stats request code = %d", rec.Code)
	}
	expectNoSave(t, st)
}

func TestHandleChainHistory(t *testing.T) {
	api := &recordingMarketAPI{}
	st := newFakeChainSnapshotStore()
	s := newTestServer(t, serverOpts{marketAPI: api, db: st, now: func() time.Time { return marketOpenNow }})

	// Seed the store through the same path the recorder uses, one minute apart
	// so the min-interval dedupe lets both writes through.
	clock := marketOpenNow
	r := newChainRecorder(st, testLogger(), func() time.Time { return clock }, 55, 90)
	rows := testChainRows()
	for i := range 2 {
		clock = marketOpenNow.Add(time.Duration(i) * time.Minute)
		r.record(context.Background(), chainCapture{
			Underlying: "NIFTY", Expiry: "2026-11-19", Spot: 25_050, Rows: rows,
			Stats: chainStatsFromRows(rows, clock.UnixMilli()), Full: true,
		})
		expectSave(t, st)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/chain/history?index=nifty&expiry=2026-11-19&from=1&to="+strconv.FormatInt(marketOpenNow.Add(time.Minute).Unix(), 10), nil)
	rec := httptest.NewRecorder()
	s.handleChainHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("history request code = %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Snapshots []store.ChainSnapshot `json:"snapshots"`
		Truncated bool                  `json:"truncated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode history response: %v", err)
	}
	if len(resp.Snapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(resp.Snapshots))
	}
	if resp.Truncated {
		t.Fatal("truncated = true, want false below the read cap")
	}
	if resp.Snapshots[0].CapturedAt > resp.Snapshots[1].CapturedAt {
		t.Fatal("snapshots are not ascending by captured_at")
	}
	first := resp.Snapshots[0]
	if first.Spot != 25_050 || first.Strikes != 3 || len(first.Rows) != 3 {
		t.Fatalf("first snapshot = %+v, want seeded spot/strikes/rows", first)
	}
	if first.Rows[0].CallOI != 1200 || first.Rows[0].CallOIChange != 300 || first.Rows[0].PutVolume != 2500 {
		t.Fatalf("first row = %+v, want per-strike OI/ΔOI/volume", first.Rows[0])
	}
	if got := st.lastRead(); got.underlying != "NIFTY" {
		t.Fatalf("history read underlying = %q, want upper-cased NIFTY", got.underlying)
	}

	// Default range: from is the start of today IST, to is now.
	req = httptest.NewRequest(http.MethodGet, "/api/chain/history?index=NIFTY&expiry=2026-11-19", nil)
	rec = httptest.NewRecorder()
	s.handleChainHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("default-range history code = %d: %s", rec.Code, rec.Body.String())
	}
	got := st.lastRead()
	if got.from != istDayStart(marketOpenNow.Unix()) || got.to != marketOpenNow.Unix() {
		t.Fatalf("default range = %d..%d, want %d..%d",
			got.from, got.to, istDayStart(marketOpenNow.Unix()), marketOpenNow.Unix())
	}
}

func TestHandleChainHistoryValidation(t *testing.T) {
	api := &recordingMarketAPI{}
	st := newFakeChainSnapshotStore()
	s := newTestServer(t, serverOpts{marketAPI: api, db: st, now: func() time.Time { return marketOpenNow }})

	cases := []struct {
		name   string
		target string
		code   int
	}{
		{"missing index", "/api/chain/history?expiry=2026-11-19", http.StatusBadRequest},
		{"missing expiry", "/api/chain/history?index=NIFTY", http.StatusBadRequest},
		{"bad from", "/api/chain/history?index=NIFTY&expiry=2026-11-19&from=abc", http.StatusBadRequest},
		{"negative to", "/api/chain/history?index=NIFTY&expiry=2026-11-19&to=-5", http.StatusBadRequest},
		{"from after to", "/api/chain/history?index=NIFTY&expiry=2026-11-19&from=200&to=100", http.StatusBadRequest},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		s.handleChainHistory(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))
		if rec.Code != tc.code {
			t.Fatalf("%s: code = %d, want %d", tc.name, rec.Code, tc.code)
		}
	}

	// Without a chain-snapshot store the endpoint reports itself unavailable.
	sWithoutStore := newTestServer(t, serverOpts{db: fakeDB{}, now: func() time.Time { return marketOpenNow }})
	rec := httptest.NewRecorder()
	sWithoutStore.handleChainHistory(rec, httptest.NewRequest(http.MethodGet,
		"/api/chain/history?index=NIFTY&expiry=2026-11-19", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no-store history code = %d, want 503", rec.Code)
	}
}
