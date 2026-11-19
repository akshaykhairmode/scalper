package store

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/errs"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), Options{
		Path:   filepath.Join(t.TempDir(), "nested", "scalper.db"),
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Now:    func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func TestOpenAndSchema(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != 16 {
		t.Fatalf("SchemaVersion = %d, %v", v, err)
	}
	if !filepath.IsAbs(s.Path()) {
		t.Fatalf("path is not absolute: %s", s.Path())
	}
	want := []string{
		"ai_evaluations", "ai_playbooks", "audit_log", "background_watchlist", "brackets", "candles",
		"chain_snapshot_rows", "chain_snapshots", "devices", "instruments", "meta", "oi_baselines", "orders",
		"paper_account", "paper_orders", "paper_positions", "paper_trades",
		"schema_migrations", "scrip_sync", "sessions", "spot_instruments", "trades",
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tables = %v, want %v", got, want)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scalper.db")
	for i := 0; i < 2; i++ {
		s, err := Open(context.Background(), Options{Path: path})
		if err != nil {
			t.Fatalf("Open %d: %v", i, err)
		}
		v, err := s.SchemaVersion(context.Background())
		if err != nil || v != 16 {
			t.Fatalf("version = %d, %v", v, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close %d: %v", i, err)
		}
	}
}

func TestMeta(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, ok, err := s.GetMeta(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing meta = ok=%v err=%v", ok, err)
	}
	if err := s.SetMeta(ctx, "last_backup", "2026-10-27"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMeta(ctx, "last_backup")
	if err != nil || !ok || got != "2026-10-27" {
		t.Fatalf("GetMeta = %q, %v, %v", got, ok, err)
	}
	if err := s.SetMeta(ctx, "last_backup", "2026-10-28"); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.GetMeta(ctx, "last_backup")
	if err != nil || got != "2026-10-28" {
		t.Fatalf("updated meta = %q, %v", got, err)
	}
	if err := s.SetMeta(ctx, " ", "x"); err == nil {
		t.Fatal("expected empty key error")
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(context.Background(), Options{Path: " "}); !errors.Is(err, errs.ErrConfig) {
		t.Fatalf("empty path err = %v", err)
	}

	base := t.TempDir()
	notDir := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Options{Path: filepath.Join(notDir, "db.sqlite")}); err == nil {
		t.Fatal("expected directory creation error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "db.sqlite")}); err == nil {
		t.Fatal("expected canceled context error")
	}
}

func TestNilAndClosedStore(t *testing.T) {
	var s *Store
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("nil Ping should fail")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}

	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("closed Ping should fail")
	}
}

func TestLoadMigrations(t *testing.T) {
	m, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 16 {
		t.Fatalf("migrations = %+v", m)
	}
	if m[0].version != 1 || m[0].name != "0001_init.sql" || m[0].sql == "" {
		t.Fatalf("first migration = %+v", m[0])
	}
	if m[1].version != 2 || m[1].name != "0002_spot_psymbol.sql" || m[1].sql == "" {
		t.Fatalf("second migration = %+v", m[1])
	}
	if m[2].version != 3 || m[2].name != "0003_brackets.sql" || m[2].sql == "" {
		t.Fatalf("third migration = %+v", m[2])
	}
	if m[3].version != 4 || m[3].name != "0004_sessions.sql" || m[3].sql == "" {
		t.Fatalf("fourth migration = %+v", m[3])
	}
	if m[4].version != 5 || m[4].name != "0005_devices.sql" || m[4].sql == "" {
		t.Fatalf("fifth migration = %+v", m[4])
	}
	if m[5].version != 6 || m[5].name != "0006_ai.sql" || m[5].sql == "" {
		t.Fatalf("sixth migration = %+v", m[5])
	}
	if m[6].version != 7 || m[6].name != "0007_ai_outcomes.sql" || m[6].sql == "" {
		t.Fatalf("seventh migration = %+v", m[6])
	}
	if m[7].version != 8 || m[7].name != "0008_ai_paper_trades.sql" || m[7].sql == "" {
		t.Fatalf("eighth migration = %+v", m[7])
	}
	if m[8].version != 9 || m[8].name != "0009_background_watchlist.sql" || m[8].sql == "" {
		t.Fatalf("ninth migration = %+v", m[8])
	}
	if len(m) < 10 || m[9].version != 10 || m[9].name != "0010_bracket_exchange_stop.sql" || m[9].sql == "" {
		t.Fatalf("tenth migration = %+v", m[9])
	}
	if len(m) < 11 || m[10].version != 11 || m[10].name != "0011_paper_trading.sql" || m[10].sql == "" {
		t.Fatalf("eleventh migration = %+v", m[10])
	}
	if len(m) < 12 || m[11].version != 12 || m[11].name != "0012_paper_order_validity.sql" || m[11].sql == "" {
		t.Fatalf("twelfth migration = %+v", m[11])
	}
	if len(m) < 13 || m[12].version != 13 || m[12].name != "0013_paper_positions_product.sql" || m[12].sql == "" {
		t.Fatalf("thirteenth migration = %+v", m[12])
	}
	if len(m) < 14 || m[13].version != 14 || m[13].name != "0014_candle_oi.sql" || m[13].sql == "" {
		t.Fatalf("fourteenth migration = %+v", m[13])
	}
	if len(m) < 15 || m[14].version != 15 || m[14].name != "0015_chain_snapshots.sql" || m[14].sql == "" {
		t.Fatalf("fifteenth migration = %+v", m[14])
	}
	if len(m) < 16 || m[15].version != 16 || m[15].name != "0016_bracket_exchange_stop_reason.sql" || m[15].sql == "" {
		t.Fatalf("sixteenth migration = %+v", m[15])
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	err := s.applyOne(ctx, migration{version: 99, name: "0099_bad.sql", sql: `CREATE TABLE broken (`})
	if err == nil {
		t.Fatal("expected migration error")
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != 16 {
		t.Fatalf("version = %d, want 16", v)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 99`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration was recorded")
	}
}

func TestCandlesWriteReadAndAggregate(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// 5 1-minute bars spanning 09:15 to 09:19 (300 seconds)
	baseTime := int64(1793245500)
	raw1m := []market.Candle{
		{Time: baseTime, Open: 100, High: 105, Low: 99, Close: 102, Volume: 10, OpenInterest: 100},
		{Time: baseTime + 60, Open: 102, High: 108, Low: 101, Close: 107, Volume: 20, OpenInterest: 200},
		{Time: baseTime + 120, Open: 107, High: 107, Low: 95, Close: 98, Volume: 30, OpenInterest: 300},
		{Time: baseTime + 180, Open: 98, High: 103, Low: 97, Close: 101, Volume: 40, OpenInterest: 400},
		{Time: baseTime + 240, Open: 101, High: 104, Low: 100, Close: 103, Volume: 50, OpenInterest: 500},
	}

	if err := s.WriteCandles(ctx, "test_inst", "1m", raw1m); err != nil {
		t.Fatalf("WriteCandles: %v", err)
	}

	got1m, err := s.ReadCandles(ctx, "test_inst", "1m")
	if err != nil || len(got1m) != 5 {
		t.Fatalf("ReadCandles: len=%d err=%v", len(got1m), err)
	}
	if got1m[0].OpenInterest != 100 || got1m[4].OpenInterest != 500 {
		t.Fatalf("open interest roundtrip = %d..%d, want 100..500", got1m[0].OpenInterest, got1m[4].OpenInterest)
	}

	// Aggregate into 5m
	agg5m, err := s.AggregateCandlesFrom1m(ctx, "test_inst", "5m")
	if err != nil {
		t.Fatalf("AggregateCandlesFrom1m: %v", err)
	}
	if len(agg5m) != 1 {
		t.Fatalf("expected 1 aggregated 5m candle, got %d", len(agg5m))
	}

	c := agg5m[0]
	if c.Time != baseTime {
		t.Errorf("Time = %d, want %d", c.Time, baseTime)
	}
	if c.Open != 100 {
		t.Errorf("Open = %v, want 100", c.Open)
	}
	if c.High != 108 {
		t.Errorf("High = %v, want 108", c.High)
	}
	if c.Low != 95 {
		t.Errorf("Low = %v, want 95", c.Low)
	}
	if c.Close != 103 {
		t.Errorf("Close = %v, want 103", c.Close)
	}
	if c.Volume != 150 {
		t.Errorf("Volume = %d, want 150", c.Volume)
	}
	if c.OpenInterest != 500 {
		t.Errorf("OpenInterest = %d, want the latest 1m reading (500)", c.OpenInterest)
	}

	// Rewriting a bar without OI (e.g. a spot/index re-upsert) must not erase a
	// stored option OI; the upsert keeps the previous value when excluded.oi = 0.
	if err := s.WriteCandles(ctx, "test_inst", "1m", []market.Candle{
		{Time: baseTime, Open: 100, High: 105, Low: 99, Close: 102, Volume: 11, OpenInterest: 0},
	}); err != nil {
		t.Fatalf("WriteCandles zero-OI rewrite: %v", err)
	}
	got1m, err = s.ReadCandles(ctx, "test_inst", "1m")
	if err != nil {
		t.Fatalf("ReadCandles after rewrite: %v", err)
	}
	if got1m[0].OpenInterest != 100 {
		t.Fatalf("zero-OI rewrite wiped open interest: %d, want 100", got1m[0].OpenInterest)
	}
}

func TestAggregateCandlesKeepsLatestNonZeroOI(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	loc := time.FixedZone("IST", istOffsetSeconds)
	base := time.Date(2026, time.November, 18, 9, 15, 0, 0, loc).Unix()

	// One 5m bucket whose middle/end bars carry no OI: the bucket must keep 100.
	// A second bucket starts at 0 and only later gets a real reading.
	bars := []market.Candle{
		{Time: base, Open: 100, High: 101, Low: 99, Close: 100, Volume: 1, OpenInterest: 100},
		{Time: base + 60, Open: 100, High: 102, Low: 100, Close: 101, Volume: 2, OpenInterest: 0},
		{Time: base + 120, Open: 101, High: 103, Low: 101, Close: 102, Volume: 3, OpenInterest: 0},
		{Time: base + 300, Open: 102, High: 104, Low: 102, Close: 103, Volume: 4, OpenInterest: 0},
		{Time: base + 360, Open: 103, High: 105, Low: 103, Close: 104, Volume: 5, OpenInterest: 250},
	}
	if err := s.WriteCandles(ctx, "oi_agg", "1m", bars); err != nil {
		t.Fatalf("WriteCandles: %v", err)
	}
	agg, err := s.AggregateCandlesFrom1m(ctx, "oi_agg", "5m")
	if err != nil {
		t.Fatalf("AggregateCandlesFrom1m: %v", err)
	}
	if len(agg) != 2 {
		t.Fatalf("buckets = %d, want 2", len(agg))
	}
	if agg[0].OpenInterest != 100 {
		t.Fatalf("first bucket OI = %d, want 100 (latest non-zero)", agg[0].OpenInterest)
	}
	if agg[1].OpenInterest != 250 {
		t.Fatalf("second bucket OI = %d, want 250", agg[1].OpenInterest)
	}
}

func TestAggregateCandlesFrom1mSessionAligned(t *testing.T) {
	loc := time.FixedZone("IST", istOffsetSeconds)
	day1 := time.Date(2026, time.November, 16, 9, 15, 0, 0, loc).Unix()
	day2 := time.Date(2026, time.November, 17, 9, 15, 0, 0, loc).Unix()

	t.Run("1h bars anchor at the session open", func(t *testing.T) {
		s := openTest(t)
		ctx := context.Background()
		var raw []market.Candle
		// Two full hours on day 1 (09:15-11:14) plus a short day 2 whose first
		// bar must start a fresh anchor rather than extend day 1's last bucket.
		for i := 0; i < 120; i++ {
			raw = append(raw, synthetic1m(day1+int64(i*60), i))
		}
		for i := 0; i < 10; i++ {
			raw = append(raw, synthetic1m(day2+int64(i*60), i))
		}
		if err := s.WriteCandles(ctx, "nse_fo|NIFTY|2026-11-26|24000|CE", "1m", raw); err != nil {
			t.Fatalf("WriteCandles: %v", err)
		}

		got, err := s.AggregateCandlesFrom1m(ctx, "nse_fo|NIFTY|2026-11-26|24000|CE", "1h")
		if err != nil {
			t.Fatalf("AggregateCandlesFrom1m: %v", err)
		}
		// Epoch-aligned buckets would produce 09:30, 10:30 and 11:30 (IST),
		// dropping the 09:15-09:29 bars entirely.
		wantTimes := []int64{day1, day1 + 3600, day2}
		if len(got) != len(wantTimes) {
			t.Fatalf("got %d 1h bars, want %d: %+v", len(got), len(wantTimes), got)
		}
		for i, want := range wantTimes {
			if got[i].Time != want {
				t.Errorf("bar %d Time = %d (%s), want %d (%s)", i, got[i].Time,
					time.Unix(got[i].Time, 0).In(loc).Format("15:04"),
					want, time.Unix(want, 0).In(loc).Format("15:04"))
			}
		}

		// The first bar covers 09:15-10:14 inclusive: open from the first 1m
		// bar, close from the sixtieth, and all 60 volumes summed.
		first := got[0]
		if first.Open != 100 || first.High != 161 || first.Low != 99 || first.Close != 160 {
			t.Errorf("first 1h OHLC = %+v, want open=100 high=161 low=99 close=160", first)
		}
		if first.Volume != 600 {
			t.Errorf("first 1h volume = %d, want 600 (60 bars x 10)", first.Volume)
		}
		second := got[1]
		if second.Open != 160 || second.High != 221 || second.Low != 159 || second.Close != 220 {
			t.Errorf("second 1h OHLC = %+v, want open=160 high=221 low=159 close=220", second)
		}
		if second.Volume != 600 {
			t.Errorf("second 1h volume = %d, want 600", second.Volume)
		}
		// Day 2 gets its own 09:15 bucket; it must not merge into day 1.
		last := got[2]
		if last.Open != 100 || last.High != 111 || last.Low != 99 || last.Close != 110 || last.Volume != 100 {
			t.Errorf("day 2 1h bar = %+v, want open=100 high=111 low=99 close=110 volume=100", last)
		}
	})

	t.Run("5m bars match the client's session buckets", func(t *testing.T) {
		s := openTest(t)
		ctx := context.Background()
		var raw []market.Candle
		for i := 0; i < 13; i++ {
			raw = append(raw, synthetic1m(day1+int64(i*60), i))
		}
		if err := s.WriteCandles(ctx, "mcx_fo|CRUDEOIL|2026-11-18|8200|CE", "1m", raw); err != nil {
			t.Fatalf("WriteCandles: %v", err)
		}

		got, err := s.AggregateCandlesFrom1m(ctx, "mcx_fo|CRUDEOIL|2026-11-18|8200|CE", "5m")
		if err != nil {
			t.Fatalf("AggregateCandlesFrom1m: %v", err)
		}
		// web/candle_aggregator.sessionBucket and the client both anchor at the
		// session open, so 5m starts at 09:15 rather than 09:15 rounded down.
		wantTimes := []int64{day1, day1 + 300, day1 + 600}
		if len(got) != len(wantTimes) {
			t.Fatalf("got %d 5m bars, want %d: %+v", len(got), len(wantTimes), got)
		}
		wantVolumes := []int64{50, 50, 30}
		for i, want := range wantTimes {
			if got[i].Time != want {
				t.Errorf("bar %d Time = %d, want %d", i, got[i].Time, want)
			}
			if got[i].Volume != wantVolumes[i] {
				t.Errorf("bar %d Volume = %d, want %d", i, got[i].Volume, wantVolumes[i])
			}
		}
		if got[0].Open != 100 || got[0].High != 106 || got[0].Low != 99 || got[0].Close != 105 {
			t.Errorf("first 5m OHLC = %+v, want open=100 high=106 low=99 close=105", got[0])
		}
	})

	t.Run("mid-session collection still anchors at the open", func(t *testing.T) {
		s := openTest(t)
		ctx := context.Background()
		late := time.Date(2026, time.November, 16, 10, 2, 0, 0, loc).Unix()
		var raw []market.Candle
		for i := 0; i < 13; i++ {
			raw = append(raw, synthetic1m(late+int64(i*60), i))
		}
		const nse = "nse_fo|NIFTY|2026-11-26|24000|CE"
		if err := s.WriteCandles(ctx, nse, "1m", raw); err != nil {
			t.Fatalf("WriteCandles: %v", err)
		}
		got, err := s.AggregateCandlesFrom1m(ctx, nse, "5m")
		if err != nil {
			t.Fatalf("AggregateCandlesFrom1m: %v", err)
		}
		// Anchoring on the first stored bar would produce 10:02/10:07 buckets,
		// which interleave with the client's 10:00/10:05 grid after a merge.
		want := []int64{
			time.Date(2026, time.November, 16, 10, 0, 0, 0, loc).Unix(),
			time.Date(2026, time.November, 16, 10, 5, 0, 0, loc).Unix(),
			time.Date(2026, time.November, 16, 10, 10, 0, 0, loc).Unix(),
		}
		if len(got) != len(want) {
			t.Fatalf("got %d 5m bars, want %d: %+v", len(got), len(want), got)
		}
		for i, value := range want {
			if got[i].Time != value {
				t.Errorf("bar %d Time = %s, want %s", i,
					time.Unix(got[i].Time, 0).In(loc).Format("15:04"),
					time.Unix(value, 0).In(loc).Format("15:04"))
			}
		}
		// The first bucket is partial (10:00-10:04 has only 10:02-10:04) but must
		// still carry exactly the bars it saw.
		if got[0].Volume != 30 || got[1].Volume != 50 || got[2].Volume != 50 {
			t.Errorf("volumes = %d/%d/%d, want 30/50/50", got[0].Volume, got[1].Volume, got[2].Volume)
		}
	})
}

// synthetic1m builds a valid 1m bar whose prices walk upward with the minute
// offset, making ordering and OHLC mistakes visible in assertions.
func synthetic1m(ts int64, offset int) market.Candle {
	open := 100 + float64(offset)
	return market.Candle{
		Time:   ts,
		Open:   open,
		High:   open + 2,
		Low:    open - 1,
		Close:  open + 1,
		Volume: 10,
	}
}

func TestScripSnapshotAndSyncAudit(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	options := []scrip.Option{
		{
			InstrumentKey:   "nse_fo|NIFTY|2026-11-26|24000|CE",
			ExchangeSegment: "nse_fo",
			PSymbol:         "12345",
			PTrdSymbol:      "NIFTY26NOV24000CE",
			Underlying:      "NIFTY",
			Expiry:          "2026-11-26",
			StrikePaise:     2400000,
			OptionType:      scrip.Call,
			LotSize:         25,
		},
		{
			InstrumentKey:   "nse_fo|NIFTY|2026-11-26|24000|PE",
			ExchangeSegment: "nse_fo",
			PSymbol:         "12346",
			PTrdSymbol:      "NIFTY26NOV24000PE",
			Underlying:      "NIFTY",
			Expiry:          "2026-11-26",
			StrikePaise:     2400000,
			OptionType:      scrip.Put,
			LotSize:         25,
		},
	}
	spots := []scrip.Spot{
		{
			InstrumentKey:   "nse_cm|Nifty 50",
			ExchangeSegment: "nse_cm",
			Symbol:          "Nifty 50",
			DisplayName:     "NIFTY 50",
			PSymbol:         "26000",
		},
	}
	syncs := []scrip.SyncRecord{
		{
			Segment:  "nse_fo",
			FileURL:  "https://example.com/nse_fo.csv",
			Rows:     2,
			SyncedAt: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC),
			Status:   "ok",
		},
	}

	if err := s.ReplaceScripSnapshot(ctx, options, spots, syncs); err != nil {
		t.Fatalf("ReplaceScripSnapshot failed: %v", err)
	}

	optCount, spotCount, err := s.CountInstruments(ctx)
	if err != nil {
		t.Fatalf("CountInstruments: %v", err)
	}
	if optCount != 2 || spotCount != 1 {
		t.Fatalf("CountInstruments = %d options, %d spots; want 2 options, 1 spot", optCount, spotCount)
	}

	loadedOpts, loadedSpots, err := s.LoadScripSnapshot(ctx)
	if err != nil {
		t.Fatalf("LoadScripSnapshot: %v", err)
	}
	if len(loadedOpts) != 2 {
		t.Fatalf("loadedOpts len = %d, want 2", len(loadedOpts))
	}
	if loadedOpts[0].InstrumentKey != options[0].InstrumentKey {
		t.Errorf("loaded option[0] key = %s, want %s", loadedOpts[0].InstrumentKey, options[0].InstrumentKey)
	}
	if len(loadedSpots) != 1 {
		t.Fatalf("loadedSpots len = %d, want 1", len(loadedSpots))
	}
	if loadedSpots[0].Symbol != spots[0].Symbol {
		t.Errorf("loaded spot symbol = %s, want %s", loadedSpots[0].Symbol, spots[0].Symbol)
	}

	recentSyncs, err := s.LatestScripSync(ctx, 10)
	if err != nil {
		t.Fatalf("LatestScripSync: %v", err)
	}
	if len(recentSyncs) != 1 {
		t.Fatalf("recentSyncs len = %d, want 1", len(recentSyncs))
	}
	if recentSyncs[0].Segment != "nse_fo" || recentSyncs[0].Status != "ok" {
		t.Errorf("recentSyncs[0] = %+v", recentSyncs[0])
	}

	// Test validation error handling on ReplaceScripSnapshot
	dupOptions := append(options, options[0])
	if err := s.ReplaceScripSnapshot(ctx, dupOptions, spots, syncs); err == nil {
		t.Fatal("expected error on duplicate option key, got nil")
	}

	badSyncs := []scrip.SyncRecord{{Segment: "", Status: "ok"}}
	if err := s.ReplaceScripSnapshot(ctx, options, spots, badSyncs); err == nil {
		t.Fatal("expected error on empty sync segment, got nil")
	}
}

func TestCandleSyncTracking(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	syncedAt, ok, err := s.CandleSyncAt(ctx, "test_inst", "5m", "2026-11-06")
	if err != nil {
		t.Fatalf("CandleSyncAt initial: %v", err)
	}
	if ok || syncedAt != 0 {
		t.Fatalf("CandleSyncAt initial want !ok and 0, got ok=%v, %d", ok, syncedAt)
	}

	now := int64(1700000000)
	if err := s.MarkCandlesSynced(ctx, "test_inst", "5m", "2026-11-06", now); err != nil {
		t.Fatalf("MarkCandlesSynced: %v", err)
	}

	syncedAt, ok, err = s.CandleSyncAt(ctx, "test_inst", "5m", "2026-11-06")
	if err != nil {
		t.Fatalf("CandleSyncAt after sync: %v", err)
	}
	if !ok || syncedAt != now {
		t.Fatalf("CandleSyncAt = %d (ok=%v), want %d", syncedAt, ok, now)
	}
}

func TestPruneCandles(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Now().Unix()
	err := s.WriteCandles(ctx, "prune_inst", "1m", []market.Candle{
		{Time: base - 100000, Open: 1, High: 1, Low: 1, Close: 1},
		{Time: base, Open: 2, High: 2, Low: 2, Close: 2},
	})
	if err != nil {
		t.Fatalf("WriteCandles: %v", err)
	}
	rows, err := s.PruneCandles(ctx, base-10)
	if err != nil {
		t.Fatalf("PruneCandles: %v", err)
	}
	if rows != 1 {
		t.Fatalf("pruned rows = %d, want 1", rows)
	}
	got, err := s.ReadCandles(ctx, "prune_inst", "1m")
	if err != nil {
		t.Fatalf("ReadCandles: %v", err)
	}
	if len(got) != 1 || got[0].Close != 2 {
		t.Fatalf("remaining candles = %+v", got)
	}
}

func TestOIBaselinesKeepFirstSeenValuePerDay(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.SaveOIBaselines(ctx, "2026-11-18", map[string]int64{
		"nse_fo|25000CE": 1000,
		"nse_fo|25000PE": 500,
	}); err != nil {
		t.Fatalf("SaveOIBaselines: %v", err)
	}

	loaded, err := s.LoadOIBaselines(ctx, "2026-11-18")
	if err != nil {
		t.Fatalf("LoadOIBaselines: %v", err)
	}
	if loaded["nse_fo|25000CE"] != 1000 || loaded["nse_fo|25000PE"] != 500 {
		t.Fatalf("baselines = %v, want seeded values", loaded)
	}

	// A later batch must not overwrite the earliest reading, but may add keys.
	if err := s.SaveOIBaselines(ctx, "2026-11-18", map[string]int64{
		"nse_fo|25000CE": 9999,
		"nse_fo|25100CE": 42,
	}); err != nil {
		t.Fatalf("SaveOIBaselines second batch: %v", err)
	}
	loaded, err = s.LoadOIBaselines(ctx, "2026-11-18")
	if err != nil {
		t.Fatalf("LoadOIBaselines second: %v", err)
	}
	if loaded["nse_fo|25000CE"] != 1000 {
		t.Fatalf("existing baseline overwritten: %v", loaded)
	}
	if loaded["nse_fo|25100CE"] != 42 {
		t.Fatalf("new baseline missing: %v", loaded)
	}

	nextDay, err := s.LoadOIBaselines(ctx, "2026-11-19")
	if err != nil {
		t.Fatalf("LoadOIBaselines next day: %v", err)
	}
	if len(nextDay) != 0 {
		t.Fatalf("next-day baselines = %v, want none", nextDay)
	}

	if err := s.SaveOIBaselines(ctx, "", map[string]int64{"x": 1}); err != nil {
		t.Fatalf("SaveOIBaselines without a day should be a no-op: %v", err)
	}
}
