package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// Redacted is the replacement value for every secret field.
const Redacted = "[REDACTED]"

var sensitiveSubstrings = []string{
	"token", "secret", "password", "passwd", "mpin", "totp", "cookie", "csrf",
	"authorization", "neo-fin-key", "neo_fin_key",
}

// IsSensitiveKey reports whether a log/audit key must be redacted.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	switch k {
	case "sid", "auth", "otp":
		return true
	}
	for _, s := range sensitiveSubstrings {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// RedactValue returns a safe copy of v for the given key. Maps and slices are
// traversed; a sensitive key is replaced wholesale.
func RedactValue(key string, v any) any {
	if IsSensitiveKey(key) {
		return Redacted
	}
	switch x := v.(type) {
	case map[string]any:
		return RedactMap(x)
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, val := range x {
			if IsSensitiveKey(k) {
				out[k] = Redacted
			} else {
				out[k] = val
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = RedactValue("", item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(x))
		for i, item := range x {
			out[i] = RedactMap(item)
		}
		return out
	case json.RawMessage:
		out, err := RedactJSON(x)
		if err != nil {
			// Never emit an unparseable raw payload: it may contain secrets.
			return "[unparseable payload redacted]"
		}
		return json.RawMessage(out)
	case slog.Value:
		return redactValue(x)
	default:
		return v
	}
}

// RedactMap returns a deep-redacted copy of m.
func RedactMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if IsSensitiveKey(k) {
			out[k] = Redacted
			continue
		}
		out[k] = RedactValue(k, v)
	}
	return out
}

// RedactArgs redacts slog-style key/value arguments. Odd trailing arguments
// and non-string attrs are preserved.
func RedactArgs(args []any) []any {
	out := make([]any, len(args))
	copy(out, args)
	for i := 0; i < len(out); i++ {
		switch x := out[i].(type) {
		case string:
			if i+1 < len(out) {
				out[i+1] = RedactValue(x, out[i+1])
				i++
			}
		case slog.Attr:
			out[i] = redactAttr(x)
		}
	}
	return out
}

// RedactJSON parses, redacts and re-encodes a JSON document.
func RedactJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("redact json: %w", err)
	}
	v = RedactValue("", v)
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("redact json: marshal: %w", err)
	}
	return out, nil
}

// RedactHeaders returns redacted request/response headers for logging.
func RedactHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if IsSensitiveKey(k) {
			out[k] = Redacted
			continue
		}
		out[k] = strings.Join(vals, ", ")
	}
	return out
}

// RedactURL removes sensitive query values and userinfo from a URL before it
// is logged. Unparseable input is replaced rather than emitted.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid url redacted]"
	}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), Redacted)
		}
	}
	q := u.Query()
	for k := range q {
		if IsSensitiveKey(k) {
			q.Set(k, Redacted)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// RedactQuery redacts an already-split query string (without the leading ?).
func RedactQuery(raw string) string {
	if raw == "" {
		return ""
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "[invalid query redacted]"
	}
	for k := range q {
		if IsSensitiveKey(k) {
			q.Set(k, Redacted)
		}
	}
	return q.Encode()
}

type redactHandler struct {
	next slog.Handler
}

func (h *redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return &redactHandler{next: h.next.WithAttrs(out)}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{next: h.next.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	return slog.Attr{Key: a.Key, Value: redactValue(a.Value.Resolve())}
}

func redactValue(v slog.Value) slog.Value {
	switch v.Kind() {
	case slog.KindGroup:
		attrs := v.Group()
		out := make([]slog.Attr, len(attrs))
		for i, a := range attrs {
			out[i] = redactAttr(a)
		}
		return slog.GroupValue(out...)
	case slog.KindAny:
		return slog.AnyValue(RedactValue("", v.Any()))
	default:
		return v
	}
}
