package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

type requestIDKey struct{}

// WithRequestID stores a correlation id in ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the correlation id, or "" when absent.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// ValidRequestID reports whether an inbound X-Request-ID is safe to reuse.
func ValidRequestID(id string) bool { return requestIDPattern.MatchString(id) }

// NewRequestID returns a 24-character random id. It never returns an error:
// crypto/rand failure would be unrecoverable, so it falls back to a
// time-independent placeholder that is still safe to log.
func NewRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req_unknown_000000000000"
	}
	return hex.EncodeToString(b[:])
}
