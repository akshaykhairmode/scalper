package kotakhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEndpointClass(t *testing.T) {
	cases := map[string]string{
		"/script-details/1.0/quotes/neosymbol/nse_cm%7CNifty%2050": "quotes",
		"/quick/user/orders":         "orders",
		"/quick/user/orders/123":     "orders",
		"/quick/user/trades":         "trades",
		"/portfolio/v2/positions":    "positions",
		"/quick/user/limits":         "limits",
		"/portfolio/v1/holdings":     "holdings",
		"/quick/order/rule/ms/place": "order-write",
		"/quick/order/cancel":        "order-write",
		"/login/1.0/tradeApiLogin":   "login",
		"/":                          "other",
		"/custom/path":               "custom/path",
	}
	for path, want := range cases {
		if got := endpointClass(path); got != want {
			t.Errorf("endpointClass(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestReportResetsCounters(t *testing.T) {
	stats.Lock()
	stats.counts["orders"] = 3
	stats.limited = 1
	stats.errors = 2
	stats.Unlock()

	counts, limited, errors := Report()
	if counts["orders"] != 3 || limited != 1 || errors != 2 {
		t.Fatalf("Report() = %v, %d, %d", counts, limited, errors)
	}
	counts, limited, errors = Report()
	if len(counts) != 0 || limited != 0 || errors != 0 {
		t.Fatalf("second Report() = %v, %d, %d; want empty", counts, limited, errors)
	}
}

type dummyTransport func(*http.Request) (*http.Response, error)

func (d dummyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return d(r)
}

func TestLimitedTransport(t *testing.T) {
	lim := newLimiter(10, 10, 10)
	trans := &limitedTransport{
		base: dummyTransport(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			if req.URL.Path == "/429" {
				rec.WriteHeader(http.StatusTooManyRequests)
			} else if req.URL.Path == "/err" {
				return nil, errors.New("network failure")
			} else {
				rec.WriteHeader(http.StatusOK)
			}
			return rec.Result(), nil
		}),
		limiter: lim,
	}

	// Normal request
	req1, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.kotak.com/quick/user/orders", nil)
	resp1, err1 := trans.RoundTrip(req1)
	if err1 != nil || resp1.StatusCode != http.StatusOK {
		t.Fatalf("unexpected req1: %v, %v", resp1, err1)
	}

	// 429 request
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.kotak.com/429", nil)
	resp2, _ := trans.RoundTrip(req2)
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("unexpected req2: %v", resp2)
	}

	// Error request
	req3, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.kotak.com/err", nil)
	_, err3 := trans.RoundTrip(req3)
	if err3 == nil {
		t.Fatal("expected error for req3")
	}

	counts, limited, errs := Report()
	if counts["orders"] != 1 || limited != 1 || errs != 1 {
		t.Errorf("Report got counts=%v, limited=%d, errs=%d", counts, limited, errs)
	}

	// Canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reqCanceled, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.kotak.com/orders", nil)
	if _, err := trans.RoundTrip(reqCanceled); err == nil {
		t.Error("expected context canceled error")
	}
}

func TestLimiterWait(t *testing.T) {
	lim := newLimiter(5, 5, 5)
	ctx := context.Background()
	if err := lim.wait(ctx); err != nil {
		t.Fatalf("limiter wait failed: %v", err)
	}

	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lim.wait(ctxCancel); err == nil {
		t.Fatal("expected error on canceled context")
	}
}

func TestNewClient(t *testing.T) {
	client := NewClient(5 * time.Second)
	if client == nil || client.Timeout != 5*time.Second {
		t.Fatalf("unexpected client: %v", client)
	}
}
