package logging

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// rotatingWriter wraps lumberjack.Logger for size- and time-based log rotation.
type rotatingWriter struct {
	*lumberjack.Logger
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

	lj := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxSizeMB,     // megabytes
		MaxBackups: maxBackups,    // files
		MaxAge:     retentionDays, // days
		Compress:   true,          // compress old backups with gzip
	}

	return &rotatingWriter{Logger: lj}, nil
}
