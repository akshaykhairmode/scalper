package logging

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rotatingWriter is a size-based rotating file sink. It is intentionally
// small: one process, one file, numeric backups.
type rotatingWriter struct {
	mu        sync.Mutex
	path      string
	file      *os.File
	size      int64
	maxSize   int64
	maxBackup int
	retention time.Duration
	now       func() time.Time
	closed    bool
}

func newRotatingWriter(path string, maxSizeMB, maxBackups, retentionDays int) (*rotatingWriter, error) {
	if path == "" {
		return nil, errors.New("logging: empty log file path")
	}
	if maxSizeMB <= 0 {
		return nil, fmt.Errorf("logging: max size must be > 0, got %d", maxSizeMB)
	}
	if maxBackups <= 0 {
		return nil, fmt.Errorf("logging: max backups must be > 0, got %d", maxBackups)
	}
	if retentionDays <= 0 {
		return nil, fmt.Errorf("logging: retention days must be > 0, got %d", retentionDays)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("logging: create log directory: %w", err)
	}
	// The path is supplied explicitly by the local operator in configuration.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("logging: open log file %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		cerr := f.Close()
		return nil, errors.Join(fmt.Errorf("logging: stat log file: %w", err), cerr)
	}
	w := &rotatingWriter{
		path:      path,
		file:      f,
		size:      info.Size(),
		maxSize:   int64(maxSizeMB) * 1024 * 1024,
		maxBackup: maxBackups,
		retention: time.Duration(retentionDays) * 24 * time.Hour,
		now:       time.Now,
	}
	if err := w.pruneLocked(); err != nil {
		cerr := w.file.Close()
		return nil, errors.Join(fmt.Errorf("logging: prune old logs: %w", err), cerr)
	}
	return w, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("logging: write log file: %w", err)
	}
	return n, nil
}

func (w *rotatingWriter) rotateLocked() error {
	var errs []error
	if err := w.file.Sync(); err != nil {
		errs = append(errs, fmt.Errorf("sync: %w", err))
	}
	if err := w.file.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close: %w", err))
	}

	oldest := w.backupPath(w.maxBackup)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("remove %s: %w", oldest, err))
	}
	for i := w.maxBackup - 1; i >= 1; i-- {
		from, to := w.backupPath(i), w.backupPath(i+1)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("rename %s to %s: %w", from, to, err))
		}
	}
	if err := os.Rename(w.path, w.backupPath(1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("rename active log: %w", err))
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		w.closed = true
		errs = append(errs, fmt.Errorf("open new log file: %w", err))
		return errors.Join(errs...)
	}
	w.file = f
	w.size = 0
	if err := w.pruneLocked(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (w *rotatingWriter) backupPath(n int) string {
	return w.path + "." + strconv.Itoa(n)
}

func (w *rotatingWriter) pruneLocked() error {
	dir := filepath.Dir(w.path)
	base := filepath.Base(w.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read log directory: %w", err)
	}
	cutoff := w.now().Add(-w.retention)
	var errs []error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, base+".") {
			continue
		}
		suffix := strings.TrimPrefix(name, base+".")
		n, err := strconv.Atoi(suffix)
		if err != nil || n < 1 || n > w.maxBackup {
			continue
		}
		info, err := e.Info()
		if err != nil {
			errs = append(errs, fmt.Errorf("stat %s: %w", name, err))
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("remove %s: %w", name, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var errs []error
	if err := w.file.Sync(); err != nil {
		errs = append(errs, fmt.Errorf("logging: sync log file: %w", err))
	}
	if err := w.file.Close(); err != nil {
		errs = append(errs, fmt.Errorf("logging: close log file: %w", err))
	}
	return errors.Join(errs...)
}
