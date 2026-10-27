package errs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSentinelErrors(t *testing.T) {
	all := []error{
		ErrConfig, ErrStore, ErrSessionExpired, ErrStaleData, ErrInsufficientFunds,
		ErrRiskBlocked, ErrMarketClosed, ErrNotLoggedIn, ErrRateLimited, ErrShuttingDown,
		ErrShortFrame, ErrMalformedFrame, ErrBadPacketType, ErrUnknownTopic,
	}
	seen := make(map[string]bool, len(all))
	for _, err := range all {
		if err == nil {
			t.Fatal("nil sentinel error")
		}
		msg := err.Error()
		if msg == "" {
			t.Fatalf("empty message for %#v", err)
		}
		if seen[msg] {
			t.Fatalf("duplicate sentinel message %q", msg)
		}
		seen[msg] = true
		if !errors.Is(err, err) {
			t.Fatalf("sentinel %q not equal to itself", msg)
		}
	}
}

func TestConfigErrorFormatting(t *testing.T) {
	cases := []struct {
		err  ConfigError
		want string
	}{
		{
			err:  ConfigError{Path: "config.yaml", Field: "app.listen", Err: errors.New("bad")},
			want: "config config.yaml: app.listen: bad",
		},
		{
			err:  ConfigError{Field: "app.listen", Err: errors.New("bad")},
			want: "config: app.listen: bad",
		},
		{
			err:  ConfigError{Path: "config.yaml", Err: errors.New("bad")},
			want: "config config.yaml: bad",
		},
		{
			err:  ConfigError{Err: errors.New("bad")},
			want: "config: bad",
		},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
		if !errors.Is(&tc.err, ErrConfig) {
			t.Errorf("errors.Is(ErrConfig) = false for %q", tc.want)
		}
		cause := errors.New("wrapped")
		wrapped := &ConfigError{Field: "x", Err: fmt.Errorf("context: %w", cause)}
		if !errors.Is(wrapped, cause) {
			t.Error("Unwrap chain broken")
		}
	}
}

func TestBrokerError(t *testing.T) {
	e := &BrokerError{
		Op:         "place order",
		StatusCode: 400,
		Code:       "AB1001",
		Message:    "rejected",
		Raw:        json.RawMessage(`{"token":"secret"}`),
	}
	msg := e.Error()
	for _, want := range []string{"place order", "status=400", "AB1001", "rejected", "retryable=false"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q missing %q", msg, want)
		}
	}
	if strings.Contains(msg, "secret") {
		t.Errorf("Error() leaked raw payload: %q", msg)
	}
	if e.Unwrap() != nil {
		t.Error("non-retryable error should not unwrap")
	}

	empty := &BrokerError{}
	if got := empty.Error(); !strings.HasPrefix(got, "broker request failed") {
		t.Errorf("default Error() = %q", got)
	}

	retryable := &BrokerError{Op: "quotes", StatusCode: 429, Retryable: true}
	if !errors.Is(retryable, ErrRateLimited) {
		t.Error("retryable broker error should match ErrRateLimited")
	}
	if errors.Is(retryable, ErrInsufficientFunds) {
		t.Error("unexpected sentinel match")
	}
}
