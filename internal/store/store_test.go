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
	if err != nil || v != 3 {
		t.Fatalf("SchemaVersion = %d, %v", v, err)
	}
	if !filepath.IsAbs(s.Path()) {
		t.Fatalf("path is not absolute: %s", s.Path())
	}
	want := []string{
		"audit_log", "brackets", "candles", "instruments", "meta", "orders",
		"schema_migrations", "scrip_sync", "spot_instruments", "trades",
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
		if err != nil || v != 3 {
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
	if len(m) != 3 {
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
	if v != 3 {
		t.Fatalf("version = %d, want 3", v)
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
		{Time: baseTime, Open: 100, High: 105, Low: 99, Close: 102, Volume: 10},
		{Time: baseTime + 60, Open: 102, High: 108, Low: 101, Close: 107, Volume: 20},
		{Time: baseTime + 120, Open: 107, High: 107, Low: 95, Close: 98, Volume: 30},
		{Time: baseTime + 180, Open: 98, High: 103, Low: 97, Close: 101, Volume: 40},
		{Time: baseTime + 240, Open: 101, High: 104, Low: 100, Close: 103, Volume: 50},
	}

	if err := s.WriteCandles(ctx, "test_inst", "1m", raw1m); err != nil {
		t.Fatalf("WriteCandles: %v", err)
	}

	got1m, err := s.ReadCandles(ctx, "test_inst", "1m")
	if err != nil || len(got1m) != 5 {
		t.Fatalf("ReadCandles: len=%d err=%v", len(got1m), err)
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
