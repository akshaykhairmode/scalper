// Package logging builds the application logger.
//
// It provides structured slog logging, mandatory redaction, request
// correlation, buffered rotating file output and a visible degraded state when
// the file sink fails. Ticks are sampled by the market layer; every other state
// transition is logged.
package logging

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures the logger.
type Options struct {
	Level         string
	Format        string
	File          string
	MaxSizeMB     int
	MaxBackups    int
	RetentionDays int
}

// Handle owns the logger and its optional file sink.
type Handle struct {
	Logger *slog.Logger

	closer   io.Closer
	degraded atomic.Bool

	mu      sync.Mutex
	sinkErr error
}

// New builds a logger with base attributes. Invalid level/format is an error;
// a file sink failure falls back to stderr, sets Degraded, and is reported by
// the returned logger (startup must not fail because logging cannot open a
// file).
func New(opts Options, base ...any) (*Handle, error) {
	level, err := ParseLevel(opts.Level)
	if err != nil {
		return nil, err
	}
	if opts.Format != "json" && opts.Format != "text" {
		return nil, fmt.Errorf("logging: unsupported format %q", opts.Format)
	}

	h := &Handle{}
	var sink io.Writer = os.Stderr

	var buffered *bufferedSink
	if opts.File != "" {
		rw, err := newRotatingWriter(opts.File, opts.MaxSizeMB, opts.MaxBackups, opts.RetentionDays)
		if err != nil {
			h.markDegraded(err)
		} else {
			buffered = newBufferedSink(rw, time.Second, func(err error) {
				h.markDegraded(err)
				werr := writeSinkWarning(os.Stderr, err)
				h.markDegraded(werr)
			})
			h.closer = buffered
			sink = &fallbackWriter{primary: buffered, stderr: os.Stderr, onDegrade: h.markDegraded}
		}
	}

	var handler slog.Handler
	handlerOpts := &slog.HandlerOptions{Level: level}
	if opts.Format == "json" {
		handler = slog.NewJSONHandler(sink, handlerOpts)
	} else {
		handler = slog.NewTextHandler(sink, handlerOpts)
	}
	handler = &redactHandler{next: handler}
	handler = &flushHandler{next: handler, flush: func() error {
		if buffered == nil {
			return nil
		}
		return buffered.Flush()
	}}

	logger := slog.New(handler)
	if len(base) > 0 {
		logger = logger.With(base...)
	}
	h.Logger = logger

	if opts.File != "" && h.Degraded() {
		h.mu.Lock()
		sinkErr := h.sinkErr
		h.mu.Unlock()
		logger.Warn("log file unavailable; using stderr",
			"event", "log_sink_failed", "path", opts.File, "error", sinkErr)
	}
	return h, nil
}

// ParseLevel maps a configuration value to a slog level.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logging: unsupported level %q", s)
	}
}

// Degraded reports whether the file sink failed and logging fell back to
// stderr. It is safe for concurrent use.
func (h *Handle) Degraded() bool { return h != nil && h.degraded.Load() }

// SinkError returns the first file-sink failure, if any.
func (h *Handle) SinkError() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sinkErr
}

func (h *Handle) markDegraded(err error) {
	if h == nil || err == nil {
		return
	}
	h.mu.Lock()
	if h.sinkErr == nil {
		h.sinkErr = err
	}
	h.mu.Unlock()
	h.degraded.Store(true)
}

// Close flushes buffered output and closes the file sink. It is idempotent and
// returns every cleanup failure.
func (h *Handle) Close() error {
	if h == nil || h.closer == nil {
		return nil
	}
	return h.closer.Close()
}

// flushHandler flushes buffered output after warn/error records so important
// lines are durable immediately.
type flushHandler struct {
	next  slog.Handler
	flush func() error
}

func (h *flushHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *flushHandler) Handle(ctx context.Context, r slog.Record) error {
	err := h.next.Handle(ctx, r)
	if r.Level >= slog.LevelWarn {
		if ferr := h.flush(); ferr != nil {
			err = errors.Join(err, ferr)
		}
	}
	return err
}

func (h *flushHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &flushHandler{next: h.next.WithAttrs(attrs), flush: h.flush}
}

func (h *flushHandler) WithGroup(name string) slog.Handler {
	return &flushHandler{next: h.next.WithGroup(name), flush: h.flush}
}

// bufferedSink buffers writes and flushes periodically, on warn/error and on
// close. Flush failures are reported through onError and never crash the app.
type bufferedSink struct {
	mu     sync.Mutex
	bw     *bufio.Writer
	under  io.WriteCloser
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	closed bool
	onErr  func(error)
}

func newBufferedSink(w io.WriteCloser, interval time.Duration, onErr func(error)) *bufferedSink {
	b := &bufferedSink{
		bw:    bufio.NewWriterSize(w, 64*1024),
		under: w,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	var once sync.Once
	b.onErr = func(err error) {
		once.Do(func() { onErr(err) })
	}
	go b.loop(interval)
	return b
}

func (b *bufferedSink) loop(interval time.Duration) {
	defer close(b.done)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := b.Flush(); err != nil && b.onErr != nil {
				// onErr is idempotent; calling it here keeps the error visible
				// even if Flush's own report was already delivered.
				b.onErr(err)
			}
		case <-b.stop:
			return
		}
	}
}

func (b *bufferedSink) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, os.ErrClosed
	}
	return b.bw.Write(p)
}

func (b *bufferedSink) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	err := b.bw.Flush()
	if err != nil && b.onErr != nil {
		b.onErr(err)
	}
	return err
}

func (b *bufferedSink) Close() error {
	var err error
	b.once.Do(func() {
		close(b.stop)
		<-b.done
		b.mu.Lock()
		defer b.mu.Unlock()
		b.closed = true
		err = errors.Join(b.bw.Flush(), b.under.Close())
	})
	return err
}

// fallbackWriter writes to stderr if the primary sink fails, so a full disk or
// permission change can never stop the trading process from logging.
type fallbackWriter struct {
	primary   io.Writer
	stderr    io.Writer
	onDegrade func(error)
	failed    atomic.Bool
}

func (f *fallbackWriter) Write(p []byte) (int, error) {
	if f.failed.Load() {
		return f.stderr.Write(p)
	}
	n, err := f.primary.Write(p)
	if err == nil {
		return n, nil
	}
	f.failed.Store(true)
	if f.onDegrade != nil {
		f.onDegrade(err)
	}
	warnErr := writeSinkWarning(f.stderr, err)
	// The original record still goes to stderr; the sink failure is visible and
	// recorded, so this is a logged fallback rather than a swallowed error.
	if _, werr := f.stderr.Write(p); werr != nil {
		return n, errors.Join(err, warnErr, werr)
	}
	if warnErr != nil {
		return n, errors.Join(err, warnErr)
	}
	return len(p), nil
}

func writeSinkWarning(w io.Writer, err error) error {
	if err == nil {
		return nil
	}
	line := fmt.Sprintf("time=%s level=WARN msg=%q event=log_sink_failed error=%q\n",
		time.Now().Format(time.RFC3339Nano), "log sink failed; falling back to stderr", err.Error())
	_, werr := io.WriteString(w, line)
	return werr
}
