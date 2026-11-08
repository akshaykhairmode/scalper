// Package store opens the SQLite database and applies schema migrations.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	// Register the SQLite database driver used by sql.Open.
	_ "modernc.org/sqlite"

	"github.com/akshaykhairmode/scalper/internal/errs"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Options configures Store.
type Options struct {
	Path          string
	Logger        *slog.Logger
	Now           func() time.Time
	EncryptionKey []byte
}

// Store owns the database handle.
type Store struct {
	db   *sql.DB
	path string
	log  *slog.Logger
	now  func() time.Time
	key  []byte
}

// Open creates the database directory, opens SQLite in WAL mode and applies
// pending migrations.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if strings.TrimSpace(opts.Path) == "" {
		return nil, &errs.ConfigError{Field: "app.db_path", Err: errors.New("must not be empty")}
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	abs, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve db path %s: %w", opts.Path, err)
	}
	dbDir := filepath.Dir(abs)
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create db directory: %w", err)
	}
	_ = os.Chmod(dbDir, 0o700) // #nosec G302

	encKey := opts.EncryptionKey
	if len(encKey) == 0 {
		encKey = loadOrCreateKey(dbDir)
	}

	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: q.Encode()}).String()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", abs, err)
	}
	// WAL mode enables concurrent readers while busy_timeout handles serialized writes.
	// Allow up to 4 open connections so readers are not serialized behind each other.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		cerr := db.Close()
		return nil, errors.Join(fmt.Errorf("store: ping %s: %w", abs, err), cerr)
	}

	s := &Store{db: db, path: abs, log: log, now: now, key: encKey}
	if err := s.migrate(ctx); err != nil {
		cerr := db.Close()
		return nil, errors.Join(err, cerr)
	}
	_ = os.Chmod(abs, 0o600)
	log.Info("database ready", "event", "db_ready", "path", abs)
	return s, nil
}

func loadOrCreateKey(dir string) []byte {
	if envKey := strings.TrimSpace(os.Getenv("SCALPER_STORE_KEY")); envKey != "" {
		if raw, err := hex.DecodeString(envKey); err == nil && len(raw) == 32 {
			return raw
		}
		// Hash any arbitrary passphrase to derive a deterministic 32-byte key
		h := sha256.Sum256([]byte(envKey))
		return h[:]
	}
	keyPath := filepath.Join(dir, ".store.key")
	if data, err := os.ReadFile(keyPath); err == nil && len(data) == 32 { // #nosec G304
		return data
	}
	var newKey [32]byte
	if _, err := rand.Read(newKey[:]); err == nil {
		_ = os.WriteFile(keyPath, newKey[:], 0o600) // #nosec G304 G703
		return newKey[:]
	}
	return nil
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var out []migration
	seen := make(map[int]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("store: migration %q must be named <version>_<name>.sql", e.Name())
		}
		v, err := strconv.Atoi(prefix)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("store: migration %q has invalid version %q", e.Name(), prefix)
		}
		if seen[v] {
			return nil, fmt.Errorf("store: duplicate migration version %d", v)
		}
		seen[v] = true
		data, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read migration %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	if len(out) == 0 {
		return nil, errors.New("store: no migrations embedded")
	}
	return out, nil
}

func (s *Store) migrate(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}
	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := s.applyOne(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("store: query applied migrations: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && s.log != nil {
			s.log.Warn("close migration rows", "event", "db_rows_close_failed", "error", cerr)
		}
	}()
	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan migration version: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate migrations: %w", err)
	}
	return applied, nil
}

func (s *Store) applyOne(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration %d: %w", m.version, err)
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		rerr := tx.Rollback()
		return errors.Join(fmt.Errorf("store: apply migration %d (%s): %w", m.version, m.name, err), rerr)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, s.now().Unix()); err != nil {
		rerr := tx.Rollback()
		return errors.Join(fmt.Errorf("store: record migration %d: %w", m.version, err), rerr)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %d: %w", m.version, err)
	}
	s.log.Info("migration applied",
		"event", "migration_applied", "version", m.version, "name", m.name)
	return nil
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store: not open")
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: ping: %w", err)
	}
	return nil
}

// SchemaVersion returns the highest applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}

// SetMeta upserts a small key/value record.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("store: meta key must not be empty")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO meta (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, s.now().Unix()); err != nil {
		return fmt.Errorf("store: set meta %q: %w", key, err)
	}
	return nil
}

// GetMeta reads a meta record. It returns ("", false, nil) when absent.
func (s *Store) GetMeta(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: get meta %q: %w", key, err)
	}
	return value, true, nil
}

// Path returns the absolute database path.
func (s *Store) Path() string { return s.path }

// Close closes the database. It is safe to call once per successful Open.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}
	return nil
}
