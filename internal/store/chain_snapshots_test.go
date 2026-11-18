package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

const testChainExpiry = "2026-11-19"

func sampleSnapshot(capturedAt int64, underlying string) ChainSnapshot {
	return ChainSnapshot{
		TradingDay: time.Unix(capturedAt, 0).UTC().Format("2006-01-02"),
		CapturedAt: capturedAt,
		Underlying: underlying,
		Expiry:     testChainExpiry,
		Spot:       25012.5,
		PCR:        1.25,
		MaxPain:    25000,
		CallOIWall: 25100,
		PutOIWall:  24900,
		Rows: []ChainSnapshotRow{
			{
				Strike: 24900, CallLTP: 210.5, CallOI: 1200, CallOIChange: 300, CallVolume: 5000,
				PutLTP: 12.4, PutOI: 800, PutOIChange: -100, PutVolume: 2500,
			},
			{
				Strike: 25000, CallLTP: 120.25, CallOI: 5000, CallOIChange: 1200, CallVolume: 9000,
				PutLTP: 40.1, PutOI: 3000, PutOIChange: 500, PutVolume: 7000,
			},
			{
				// One-sided strike: no PE contract was served for this row.
				Strike: 25100, CallLTP: 60.5, CallOI: 900, CallOIChange: -50, CallVolume: 3000,
			},
		},
	}
}

func TestChainSnapshotSaveReadRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	first := sampleSnapshot(1_793_000_000, "NIFTY")
	second := sampleSnapshot(1_793_000_000+120, "NIFTY")
	second.Spot = 0
	second.PCR = 0.9
	second.Rows = second.Rows[:1]
	other := sampleSnapshot(1_793_000_000+60, "BANKNIFTY")

	for _, snap := range []ChainSnapshot{first, other, second} {
		id, err := s.SaveChainSnapshot(ctx, snap)
		if err != nil {
			t.Fatalf("SaveChainSnapshot (%s %s): %v", snap.Underlying, snap.Expiry, err)
		}
		if id <= 0 {
			t.Fatalf("SaveChainSnapshot (%s %s) id = %d, want > 0", snap.Underlying, snap.Expiry, id)
		}
	}

	got, err := s.ReadChainSnapshots(ctx, "NIFTY", "2026-11-19", 0, first.CapturedAt+600)
	if err != nil {
		t.Fatalf("ReadChainSnapshots: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("snapshots = %d, want 2 (BANKNIFTY must be filtered out)", len(got))
	}
	if got[0].CapturedAt > got[1].CapturedAt {
		t.Fatalf("snapshots are not ascending: %d then %d", got[0].CapturedAt, got[1].CapturedAt)
	}

	want := first
	if got[0].TradingDay != want.TradingDay || got[0].Spot != want.Spot ||
		got[0].PCR != want.PCR || got[0].MaxPain != want.MaxPain ||
		got[0].CallOIWall != want.CallOIWall || got[0].PutOIWall != want.PutOIWall {
		t.Fatalf("header round trip = %+v, want %+v", got[0], want)
	}
	if got[0].Strikes != len(want.Rows) {
		t.Fatalf("strikes = %d, want %d", got[0].Strikes, len(want.Rows))
	}
	if len(got[0].Rows) != len(want.Rows) {
		t.Fatalf("rows = %d, want %d", len(got[0].Rows), len(want.Rows))
	}
	for i, row := range want.Rows {
		if got[0].Rows[i] != row {
			t.Fatalf("row %d round trip = %+v, want %+v", i, got[0].Rows[i], row)
		}
	}

	// The second snapshot must carry its own (shorter) row set, proving rows
	// are attached per snapshot id rather than by underlying.
	if len(got[1].Rows) != 1 || got[1].Rows[0].Strike != second.Rows[0].Strike {
		t.Fatalf("second snapshot rows = %+v, want the single 24900 row", got[1].Rows)
	}
	if got[1].Spot != 0 || got[1].PCR != 0.9 {
		t.Fatalf("second snapshot header = %+v, want spot 0 and pcr 0.9", got[1])
	}
}

func TestChainSnapshotSaveValidation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	valid := sampleSnapshot(1_793_000_000, "NIFTY")

	cases := []struct {
		name   string
		mutate func(*ChainSnapshot)
	}{
		{"no rows", func(snap *ChainSnapshot) { snap.Rows = nil }},
		{"zero captured_at", func(snap *ChainSnapshot) { snap.CapturedAt = 0 }},
		{"empty underlying", func(snap *ChainSnapshot) { snap.Underlying = " " }},
		{"empty expiry", func(snap *ChainSnapshot) { snap.Expiry = "" }},
		{"zero strike", func(snap *ChainSnapshot) { snap.Rows[1].Strike = 0 }},
	}
	for _, tc := range cases {
		snap := valid
		tc.mutate(&snap)
		if _, err := s.SaveChainSnapshot(ctx, snap); err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestChainSnapshotReadValidation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.ReadChainSnapshots(ctx, "", "2026-11-19", 0, 10); err == nil {
		t.Fatal("empty underlying should fail")
	}
	if _, err := s.ReadChainSnapshots(ctx, "NIFTY", " ", 0, 10); err == nil {
		t.Fatal("empty expiry should fail")
	}
	if _, err := s.ReadChainSnapshots(ctx, "NIFTY", "2026-11-19", 20, 10); err == nil {
		t.Fatal("from after to should fail")
	}
	if _, err := s.ReadChainSnapshots(ctx, "NIFTY", "2026-11-19", -1, 10); err == nil {
		t.Fatal("negative from should fail")
	}
	if _, err := s.ReadChainSnapshots(ctx, "NIFTY", "2026-11-19", 0, 10); err != nil {
		t.Fatalf("empty range read: %v", err)
	}
}

func TestChainSnapshotReadFiltersByRange(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	base := int64(1_793_000_000)
	for i := range 3 {
		snap := sampleSnapshot(base+int64(i)*60, "NIFTY")
		if _, err := s.SaveChainSnapshot(ctx, snap); err != nil {
			t.Fatalf("SaveChainSnapshot %d: %v", i, err)
		}
	}

	got, err := s.ReadChainSnapshots(ctx, "NIFTY", "2026-11-19", base+60, base+60)
	if err != nil {
		t.Fatalf("ReadChainSnapshots: %v", err)
	}
	if len(got) != 1 || got[0].CapturedAt != base+60 {
		t.Fatalf("range filter = %d snapshots, want exactly the %d capture", len(got), base+60)
	}
}

func TestChainSnapshotReadTruncatesAtLimit(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// Seed more headers than the read cap directly through SQL; the rows and
	// the read path stay under test without 2001 slow round trips.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chain_snapshots
			(trading_day, captured_at, underlying, expiry, spot, pcr, max_pain, call_oi_wall, put_oi_wall, strikes)
		VALUES ('2026-11-18', ?, 'NIFTY', '2026-11-19', 0, 0, 0, 0, 0, 0)`)
	if err != nil {
		t.Fatalf("prepare seed stmt: %v", err)
	}
	base := int64(1_793_000_000)
	for i := range MaxReadChainSnapshots + 1 {
		if _, err := stmt.ExecContext(ctx, base+int64(i)); err != nil {
			t.Fatalf("seed snapshot %d: %v", i, err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("close seed stmt: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}

	got, err := s.ReadChainSnapshots(ctx, "NIFTY", "2026-11-19", base, base+MaxReadChainSnapshots+100)
	if err != nil {
		t.Fatalf("ReadChainSnapshots: %v", err)
	}
	if len(got) != MaxReadChainSnapshots {
		t.Fatalf("snapshots = %d, want the %d cap", len(got), MaxReadChainSnapshots)
	}
	if got[0].CapturedAt != base {
		t.Fatalf("first snapshot = %d, want the earliest capture %d", got[0].CapturedAt, base)
	}
	if last := got[len(got)-1]; last.CapturedAt != base+MaxReadChainSnapshots-1 {
		t.Fatalf("last snapshot = %d, want %d (the newest is truncated away)",
			last.CapturedAt, base+MaxReadChainSnapshots-1)
	}
}

func TestPruneChainSnapshots(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	base := int64(1_793_000_000)
	old := sampleSnapshot(base, "NIFTY")
	fresh := sampleSnapshot(base+3600, "NIFTY")
	for i, snap := range []ChainSnapshot{old, fresh} {
		if _, err := s.SaveChainSnapshot(ctx, snap); err != nil {
			t.Fatalf("SaveChainSnapshot %d: %v", i, err)
		}
	}

	removed, err := s.PruneChainSnapshots(ctx, base+1800)
	if err != nil {
		t.Fatalf("PruneChainSnapshots: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	got, err := s.ReadChainSnapshots(ctx, "NIFTY", "2026-11-19", 0, base+7200)
	if err != nil {
		t.Fatalf("ReadChainSnapshots after prune: %v", err)
	}
	if len(got) != 1 || got[0].CapturedAt != fresh.CapturedAt {
		t.Fatalf("remaining snapshots = %+v, want only the fresh capture", got)
	}
	if len(got[0].Rows) != len(fresh.Rows) {
		t.Fatalf("fresh snapshot rows = %d, want %d", len(got[0].Rows), len(fresh.Rows))
	}

	var orphanRows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chain_snapshot_rows`).Scan(&orphanRows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if orphanRows != len(fresh.Rows) {
		t.Fatalf("strike rows after prune = %d, want %d (old rows must cascade)", orphanRows, len(fresh.Rows))
	}

	if removed, err := s.PruneChainSnapshots(ctx, 0); err != nil || removed != 0 {
		t.Fatalf("prune with zero before = %d, %v, want 0, nil", removed, err)
	}
}

func TestChainSnapshotNilStore(t *testing.T) {
	var s *Store
	ctx := context.Background()
	if _, err := s.SaveChainSnapshot(ctx, sampleSnapshot(1, "NIFTY")); err == nil {
		t.Fatal("nil store SaveChainSnapshot should fail")
	}
	if _, err := s.ReadChainSnapshots(ctx, "NIFTY", "2026-11-19", 0, 1); err == nil {
		t.Fatal("nil store ReadChainSnapshots should fail")
	}
	if _, err := s.PruneChainSnapshots(ctx, 1); err == nil {
		t.Fatal("nil store PruneChainSnapshots should fail")
	}
}

func TestChainSnapshotsMigrationApplied(t *testing.T) {
	// A pre-0015 database must gain the chain snapshot tables on open.
	path := filepath.Join(t.TempDir(), "scalper.db")
	v0, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := v0.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	v1, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := v1.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	v, err := v1.SchemaVersion(context.Background())
	if err != nil || v != 15 {
		t.Fatalf("SchemaVersion = %d, %v, want 15", v, err)
	}
	// Open must be idempotent and the tables must survive the second migrate.
	if _, err := v1.SaveChainSnapshot(context.Background(), sampleSnapshot(1_793_000_000, "NIFTY")); err != nil {
		t.Fatalf("SaveChainSnapshot after reopen: %v", err)
	}
	if _, err := v1.ReadChainSnapshots(context.Background(), "NIFTY", "2026-11-19", 0, 1_793_000_100); err != nil {
		t.Fatalf("ReadChainSnapshots after reopen: %v", err)
	}
	if err := v1.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
