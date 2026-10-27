package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type errWriter struct{ err error }

func (e errWriter) Write([]byte) (int, error) { return 0, e.err }

func testOptions(file string) Options {
	return Options{
		Level:         "debug",
		Format:        "json",
		File:          file,
		MaxSizeMB:     1,
		MaxBackups:    2,
		RetentionDays: 1,
	}
}

func TestNewInvalidOptions(t *testing.T) {
	if _, err := New(Options{Level: "trace", Format: "json"}, nil); err == nil {
		t.Fatal("expected invalid level error")
	}
	if _, err := New(Options{Level: "info", Format: "xml"}, nil); err == nil {
		t.Fatal("expected invalid format error")
	}
}

func TestFileLoggingAndRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scalper.log")
	h, err := New(testOptions(path), "component", "test")
	if err != nil {
		t.Fatal(err)
	}
	log := h.Logger
	log.Debug("debug line", "event", "test_debug")
	log.Info("login", "event", "login", "request_id", "req123456", "token", "SECRET-TOKEN")
	log.Warn("order", "event", "order",
		"mpin", "SECRET-MPIN",
		"auth", "SECRET-AUTH",
		"nested", map[string]any{"password": "SECRET-PASS", "ok": "visible"})
	if h.Degraded() {
		t.Fatalf("unexpected degraded: %v", h.SinkError())
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"SECRET-TOKEN", "SECRET-MPIN", "SECRET-AUTH", "SECRET-PASS"} {
		if strings.Contains(text, secret) {
			t.Fatalf("log leaked %q:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, Redacted) {
		t.Fatalf("log has no redaction marker:\n%s", text)
	}
	if !strings.Contains(text, `"visible"`) {
		t.Fatalf("non-secret field missing:\n%s", text)
	}
	if !strings.Contains(text, `"component":"test"`) {
		t.Fatalf("base field missing:\n%s", text)
	}
}

func TestWarnFlushesImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scalper.log")
	h, err := New(testOptions(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})
	h.Logger.Info("buffered", "event", "buffered")
	h.Logger.Warn("urgent", "event", "urgent")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"urgent"`) {
		t.Fatalf("warn line was not flushed: %q", string(data))
	}
}

func TestTextFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scalper.log")
	opts := testOptions(path)
	opts.Format = "text"
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	h.Logger.Info("hello", "event", "text_test")
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "msg=hello") || !strings.Contains(string(data), "level=INFO") {
		t.Fatalf("unexpected text log: %q", string(data))
	}
}

func TestFileSinkFailureFallsBack(t *testing.T) {
	base := t.TempDir()
	notDir := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := testOptions(filepath.Join(notDir, "scalper.log"))
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Degraded() {
		t.Fatal("expected degraded logger")
	}
	if h.SinkError() == nil {
		t.Fatal("expected sink error")
	}
	h.Logger.Info("still logging", "event", "fallback")
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRotatingWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "scalper.log")
	w, err := newRotatingWriter(path, 1, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.maxSize = 64
	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte("012345678901234567890123456789\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".1", path + ".2"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
	if _, err := os.Stat(path + ".3"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup 3 should not exist: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := w.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close err = %v", err)
	}
}

func TestRotatingWriterPrunesExpiredBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scalper.log")
	w, err := newRotatingWriter(path, 1, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	old := filepath.Join(dir, "scalper.log.1")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "scalper.log.2")
	if err := os.WriteFile(fresh, []byte("fresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.pruneLocked(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired backup still exists: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh backup removed: %v", err)
	}
}

func TestRotatingWriterValidation(t *testing.T) {
	cases := []Options{
		{File: "", MaxSizeMB: 1, MaxBackups: 1, RetentionDays: 1},
		{File: filepath.Join(t.TempDir(), "x"), MaxSizeMB: 0, MaxBackups: 1, RetentionDays: 1},
		{File: filepath.Join(t.TempDir(), "x"), MaxSizeMB: 1, MaxBackups: 0, RetentionDays: 1},
		{File: filepath.Join(t.TempDir(), "x"), MaxSizeMB: 1, MaxBackups: 1, RetentionDays: 0},
	}
	for _, c := range cases {
		if _, err := newRotatingWriter(c.File, c.MaxSizeMB, c.MaxBackups, c.RetentionDays); err == nil {
			t.Fatalf("expected error for %+v", c)
		}
	}
}

func TestBufferedSink(t *testing.T) {
	var buf bytes.Buffer
	sink := newBufferedSink(nopWriteCloser{&buf}, time.Hour, func(err error) {
		t.Errorf("unexpected sink error: %v", err)
	})
	if _, err := sink.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("write was not buffered: %q", buf.String())
	}
	if err := sink.Flush(); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello" {
		t.Fatalf("buffer = %q", buf.String())
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestFallbackWriter(t *testing.T) {
	var stderr bytes.Buffer
	var degraded error
	f := &fallbackWriter{
		primary:   errWriter{err: errors.New("disk full")},
		stderr:    &stderr,
		onDegrade: func(err error) { degraded = err },
	}
	n, err := f.Write([]byte("line\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("n = %d", n)
	}
	if !f.failed.Load() {
		t.Fatal("writer did not mark failure")
	}
	if degraded == nil {
		t.Fatal("onDegrade not called")
	}
	text := stderr.String()
	if !strings.Contains(text, "log sink failed") || !strings.Contains(text, "line") {
		t.Fatalf("stderr = %q", text)
	}
	if _, err := f.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	if strings.Count(stderr.String(), "log sink failed") != 1 {
		t.Fatalf("warning repeated: %q", stderr.String())
	}
}

func TestWriteSinkWarningError(t *testing.T) {
	if err := writeSinkWarning(errWriter{err: errors.New("no stderr")}, errors.New("boom")); err == nil {
		t.Fatal("expected error")
	}
	if err := writeSinkWarning(io.Discard, nil); err != nil {
		t.Fatalf("nil error: %v", err)
	}
}

func TestRedactionPrimitives(t *testing.T) {
	sensitive := []string{"token", "access_token", "viewToken", "secret", "password", "passwd", "mpin", "totp", "otp", "Authorization", "neo-fin-key", "neo_fin_key", "Sid", "auth", "Cookie", "csrf_token"}
	for _, key := range sensitive {
		if !IsSensitiveKey(key) {
			t.Errorf("IsSensitiveKey(%q) = false", key)
		}
	}
	safe := []string{"request_id", "broker_order_id", "session_id", "instrument", "symbol", "qty"}
	for _, key := range safe {
		if IsSensitiveKey(key) {
			t.Errorf("IsSensitiveKey(%q) = true", key)
		}
	}

	in := map[string]any{
		"token": "SECRET",
		"nested": map[string]any{
			"mpin": "1234",
			"safe": "ok",
		},
		"list": []any{
			map[string]any{"auth": "SECRET2"},
			"plain",
		},
		"headers":     map[string]string{"Authorization": "SECRET3", "Accept": "json"},
		"raw_message": json.RawMessage(`{"secret":"SECRET4","ok":true}`),
		"bad_raw":     json.RawMessage(`not json`),
	}
	out := RedactMap(in)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, secret := range []string{"SECRET", "1234", "SECRET2", "SECRET3", "SECRET4"} {
		if strings.Contains(text, secret) {
			t.Fatalf("redaction leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, `"safe":"ok"`) && !strings.Contains(text, `"ok":true`) {
		t.Fatalf("safe values missing: %s", text)
	}
}

func TestRedactArgsAndJSON(t *testing.T) {
	args := RedactArgs([]any{"token", "SECRET", "count", 2, slog.String("mpin", "1234"), "tail"})
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "1234") {
		t.Fatalf("args leaked: %s", raw)
	}

	out, err := RedactJSON([]byte(`{"auth":"x","safe":{"token":"y","n":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"x"`) || strings.Contains(string(out), `"y"`) {
		t.Fatalf("json leaked: %s", out)
	}
	if _, err := RedactJSON([]byte(`{`)); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestRedactHeadersURL(t *testing.T) {
	h := RedactHeaders(map[string][]string{
		"Authorization": {"Bearer SECRET"},
		"Accept":        {"application/json"},
		"Cookie":        {"sid=SECRET2"},
	})
	if h["Authorization"] != Redacted || h["Cookie"] != Redacted {
		t.Fatalf("headers not redacted: %+v", h)
	}
	if h["Accept"] != "application/json" {
		t.Fatalf("safe header changed: %+v", h)
	}

	u := RedactURL("https://user:SECRET@example.test/path?token=SECRET2&x=1")
	if strings.Contains(u, "SECRET") {
		t.Fatalf("url leaked: %s", u)
	}
	if !strings.Contains(u, "x=1") {
		t.Fatalf("safe query dropped: %s", u)
	}
	if got := RedactURL("://bad"); got != "[invalid url redacted]" {
		t.Fatalf("invalid url = %q", got)
	}
	q := RedactQuery("token=SECRET&x=1")
	if strings.Contains(q, "SECRET") || !strings.Contains(q, "x=1") {
		t.Fatalf("query = %q", q)
	}
	if RedactQuery("") != "" {
		t.Fatal("empty query changed")
	}
	if got := RedactQuery("%zz"); got != "[invalid query redacted]" {
		t.Fatalf("bad query = %q", got)
	}
}

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want slog.Level
	}{{"debug", slog.LevelDebug}, {"info", slog.LevelInfo}, {"warn", slog.LevelWarn}, {"error", slog.LevelError}} {
		got, err := ParseLevel(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseLevel(%q) = %v, %v", tc.in, got, err)
		}
	}
	if _, err := ParseLevel("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRequestID(t *testing.T) {
	if ValidRequestID("short") {
		t.Fatal("short id accepted")
	}
	if ValidRequestID("bad id with spaces") {
		t.Fatal("invalid chars accepted")
	}
	if !ValidRequestID("abcDEF0123456789-_") {
		t.Fatal("valid id rejected")
	}
	id := NewRequestID()
	if len(id) != 24 || !ValidRequestID(id) {
		t.Fatalf("id = %q", id)
	}
	if RequestID(context.Background()) != "" {
		t.Fatal("unexpected id")
	}
	ctx := WithRequestID(context.Background(), id)
	if got := RequestID(ctx); got != id {
		t.Fatalf("id = %q, want %q", got, id)
	}
	if RequestID(nil) != "" {
		t.Fatal("nil context should return empty")
	}
}

func TestLoggerLevelFiltering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scalper.log")
	opts := testOptions(path)
	opts.Level = "warn"
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	h.Logger.Info("hidden", "event", "hidden")
	h.Logger.Warn("shown", "event", "shown")
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hidden") {
		t.Fatalf("info was logged at warn level: %s", data)
	}
	if !strings.Contains(string(data), "shown") {
		t.Fatalf("warn missing: %s", data)
	}
}

func TestNilHandle(t *testing.T) {
	var h *Handle
	if h.Degraded() || h.SinkError() != nil || h.Close() != nil {
		t.Fatal("nil handle should be inert")
	}
}
