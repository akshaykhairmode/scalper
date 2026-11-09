package web

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/market"
)

type fakeCandleWriter struct {
	mu     sync.Mutex
	writes map[string][]market.Candle
}

func (f *fakeCandleWriter) WriteCandles(_ context.Context, instrumentKey, timeframe string, candles []market.Candle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes == nil {
		f.writes = make(map[string][]market.Candle)
	}
	key := instrumentKey + "|" + timeframe
	f.writes[key] = append(f.writes[key], candles...)
	return nil
}

func TestCandleAggregatorBuildsAndPersistsBars(t *testing.T) {
	writer := &fakeCandleWriter{}
	agg := newCandleAggregator(writer, testLogger(), 0)
	ist := time.FixedZone("IST", 5*60*60+30*60)

	t0 := time.Date(2026, 11, 9, 9, 15, 5, 0, ist)
	t1 := time.Date(2026, 11, 9, 9, 15, 30, 0, ist)
	t2 := time.Date(2026, 11, 9, 9, 16, 5, 0, ist)

	agg.OnTick("mcx_fo|CRUDEOIL|2026-11-18|8200|CE", 100, 1000, 500, t0)
	agg.OnTick("mcx_fo|CRUDEOIL|2026-11-18|8200|CE", 102, 1100, 510, t1)
	agg.OnTick("mcx_fo|CRUDEOIL|2026-11-18|8200|CE", 99, 1200, 520, t2)
	agg.Flush(context.Background())

	oneMinute := writer.writes["mcx_fo|CRUDEOIL|2026-11-18|8200|CE|1m"]
	if len(oneMinute) != 2 {
		t.Fatalf("1m candles = %d, want 2: %+v", len(oneMinute), oneMinute)
	}
	first := oneMinute[0]
	if first.Open != 100 || first.High != 102 || first.Low != 100 || first.Close != 102 {
		t.Errorf("first 1m bar = %+v", first)
	}
	if first.Volume != 100 {
		t.Errorf("first 1m volume = %d, want 100", first.Volume)
	}
	wantBucket := time.Date(2026, 11, 9, 9, 15, 0, 0, ist).Unix()
	if first.Time != wantBucket {
		t.Errorf("first bucket = %d, want %d", first.Time, wantBucket)
	}
	second := oneMinute[1]
	if second.Open != 99 || second.Close != 99 {
		t.Errorf("second 1m bar = %+v", second)
	}

	fiveMinute := writer.writes["mcx_fo|CRUDEOIL|2026-11-18|8200|CE|5m"]
	if len(fiveMinute) != 1 {
		t.Fatalf("5m candles = %d, want 1", len(fiveMinute))
	}
	if fiveMinute[0].Open != 100 || fiveMinute[0].High != 102 || fiveMinute[0].Low != 99 || fiveMinute[0].Close != 99 {
		t.Errorf("5m bar = %+v", fiveMinute[0])
	}
}

func TestSessionBucketUsesCommodityOpen(t *testing.T) {
	ist := time.FixedZone("IST", 5*60*60+30*60)
	at := time.Date(2026, 11, 9, 9, 7, 0, 0, ist)

	mcx := sessionBucket(at.Unix(), 60, true)
	wantMCX := time.Date(2026, 11, 9, 9, 7, 0, 0, ist).Unix()
	if mcx != wantMCX {
		t.Errorf("mcx bucket = %d, want %d", mcx, wantMCX)
	}
	equity := sessionBucket(at.Unix(), 60, false)
	wantEquity := time.Date(2026, 11, 9, 9, 15, 0, 0, ist).Unix() - 60
	if equity != wantEquity {
		t.Errorf("equity pre-open bucket = %d, want %d", equity, wantEquity)
	}
}

type fakeCandleStore struct {
	read       map[string][]market.Candle
	aggregated map[string][]market.Candle
	written    map[string][]market.Candle
	syncAt     int64
	synced     bool
}

func (f *fakeCandleStore) ReadCandles(_ context.Context, instrumentKey, timeframe string) ([]market.Candle, error) {
	return f.read[instrumentKey+"|"+timeframe], nil
}

func (f *fakeCandleStore) WriteCandles(_ context.Context, instrumentKey, timeframe string, candles []market.Candle) error {
	if f.written == nil {
		f.written = make(map[string][]market.Candle)
	}
	key := instrumentKey + "|" + timeframe
	f.written[key] = append(f.written[key], candles...)
	return nil
}

func (f *fakeCandleStore) CandleSyncAt(_ context.Context, _, _, _ string) (int64, bool, error) {
	return f.syncAt, f.synced, nil
}

func (f *fakeCandleStore) MarkCandlesSynced(_ context.Context, _, _, _ string, _ int64) error {
	return nil
}

func (f *fakeCandleStore) AggregateCandlesFrom1m(_ context.Context, instrumentKey, timeframe string) ([]market.Candle, error) {
	return f.aggregated[instrumentKey+"|"+timeframe], nil
}

func TestFetchInstrumentCandlesServesStoredMCX(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()

	inst := "mcx_fo|CRUDEOIL|2026-11-18|8200|CE"
	store := &fakeCandleStore{read: map[string][]market.Candle{
		inst + "|5m": {{Time: 1793000000, Open: 100, High: 105, Low: 99, Close: 104, Volume: 10}},
	}}
	s.candleStore = store

	res := s.fetchInstrumentCandles(context.Background(), inst, "5m", "2026-11-09", time.Now(), false)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if len(res.Candles) != 1 || res.Source != "sqlite" {
		t.Fatalf("res = %+v", res)
	}
}

func TestFetchInstrumentCandlesMCXCollectingWhenEmpty(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()
	s.candleStore = &fakeCandleStore{}

	res := s.fetchInstrumentCandles(context.Background(), "mcx_fo|CRUDEOIL|2026-11-18|8200|CE", "5m", "2026-11-09", time.Now(), false)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if len(res.Candles) != 0 {
		t.Fatalf("candles = %+v, want none", res.Candles)
	}
	if !strings.Contains(res.Warning, "Collecting live MCX candles") {
		t.Fatalf("warning = %q", res.Warning)
	}
}
