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
	if err != nil || v != 2 {
		t.Fatalf("SchemaVersion = %d, %v", v, err)
	}
	if !filepath.IsAbs(s.Path()) {
		t.Fatalf("path is not absolute: %s", s.Path())
	}
	want := []string{
		"audit_log", "candles", "instruments", "meta", "orders", "paper_account",
		"paper_positions", "schema_migrations", "scrip_sync", "spot_instruments", "trades",
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
		if err != nil || v != 2 {
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
	if len(m) != 2 {
		t.Fatalf("migrations = %+v", m)
	}
	if m[0].version != 1 || m[0].name != "0001_init.sql" || m[0].sql == "" {
		t.Fatalf("first migration = %+v", m[0])
	}
	if m[1].version != 2 || m[1].name != "0002_spot_psymbol.sql" || m[1].sql == "" {
		t.Fatalf("second migration = %+v", m[1])
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
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 99`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration was recorded")
	}
}
