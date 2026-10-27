// Package errs defines the application error taxonomy.
//
// Sentinel errors are used for control flow with errors.Is. Errors crossing a
// boundary are wrapped with %w and enough context to locate the failure. No
// error is ever discarded.
package errs

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	// ErrConfig marks invalid or missing configuration.
	ErrConfig = errors.New("invalid configuration")
	// ErrStore marks persistence failures.
	ErrStore = errors.New("store failure")
	// ErrSessionExpired marks an expired or invalid broker session.
	ErrSessionExpired = errors.New("session expired")
	// ErrStaleData marks market data that is too old to trade on.
	ErrStaleData = errors.New("market data stale")
	// ErrInsufficientFunds marks a failed margin check.
	ErrInsufficientFunds = errors.New("insufficient funds")
	// ErrRiskBlocked marks an order blocked by a risk rule.
	ErrRiskBlocked = errors.New("risk rule blocked order")
	// ErrMarketClosed marks an order attempted outside the trading session.
	ErrMarketClosed = errors.New("market closed")
	// ErrNotLoggedIn marks an operation that needs a broker session.
	ErrNotLoggedIn = errors.New("not logged in")
	// ErrRateLimited marks a throttled or 429 response.
	ErrRateLimited = errors.New("rate limited")
	// ErrShuttingDown marks work rejected because shutdown started.
	ErrShuttingDown = errors.New("shutting down")

	// ErrShortFrame marks a truncated HSM frame.
	ErrShortFrame = errors.New("short HSM frame")
	// ErrMalformedFrame marks an HSM frame that violates the wire format.
	ErrMalformedFrame = errors.New("malformed HSM frame")
	// ErrBadPacketType marks an unexpected HSM packet type.
	ErrBadPacketType = errors.New("unexpected HSM packet type")
	// ErrUnknownTopic marks an HSM update for a topic with no snapshot.
	ErrUnknownTopic = errors.New("unknown HSM topic id")
)

// ConfigError describes a configuration problem with its source and field.
type ConfigError struct {
	Path  string
	Field string
	Err   error
}

func (e *ConfigError) Error() string {
	switch {
	case e.Field != "" && e.Path != "":
		return fmt.Sprintf("config %s: %s: %v", e.Path, e.Field, e.Err)
	case e.Field != "":
		return fmt.Sprintf("config: %s: %v", e.Field, e.Err)
	case e.Path != "":
		return fmt.Sprintf("config %s: %v", e.Path, e.Err)
	default:
		return fmt.Sprintf("config: %v", e.Err)
	}
}

// Unwrap returns the underlying cause.
func (e *ConfigError) Unwrap() error { return e.Err }

// Is reports whether target is ErrConfig, so errors.Is(err, ErrConfig) works
// for every configuration failure.
func (e *ConfigError) Is(target error) bool { return target == ErrConfig }

// BrokerError is a typed failure returned by a broker REST call.
//
// Raw is retained for troubleshooting but is never part of Error(), because it
// can contain credentials or account data.
type BrokerError struct {
	Op         string
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
	Raw        json.RawMessage
}

func (e *BrokerError) Error() string {
	op := e.Op
	if op == "" {
		op = "broker request"
	}
	code := e.Code
	if code == "" {
		code = "-"
	}
	return fmt.Sprintf("%s failed: status=%d code=%s message=%q retryable=%t",
		op, e.StatusCode, code, e.Message, e.Retryable)
}

// Unwrap maps retryable broker failures to ErrRateLimited so callers can make
// a retry decision without inspecting string messages.
func (e *BrokerError) Unwrap() error {
	if e.Retryable {
		return ErrRateLimited
	}
	return nil
}
