// Package kotakhttp provides IPv4-only HTTP clients and rate limiting for Kotak APIs.
package kotakhttp

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// NewClient returns an HTTP client that dials Kotak over IPv4 only and routes
// every request through the process-wide Kotak rate limiter.
//
// Kotak's order APIs enforce a static-IP allowlist. On dual-stack hosts Go's
// default dialer prefers IPv6, which can make login and order requests egress
// from an address that is not on the allowlist even when the host's IPv4 is.
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	base := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &limitedTransport{base: base, limiter: defaultLimiter},
	}
}

// limitedTransport applies the shared Kotak limiter and records request
// statistics so the server can log the real call rate per endpoint.
type limitedTransport struct {
	base    http.RoundTripper
	limiter *limiter
}

func (t *limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limiter.wait(req.Context()); err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	record(req, resp, err)
	return resp, err
}

var stats = struct {
	sync.Mutex
	started time.Time
	counts  map[string]int
	limited int
	errors  int
}{started: time.Now(), counts: make(map[string]int)}

func record(req *http.Request, resp *http.Response, err error) {
	stats.Lock()
	defer stats.Unlock()
	stats.counts[endpointClass(req.URL.Path)]++
	if err != nil {
		stats.errors++
		return
	}
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		stats.limited++
	}
}

// endpointClass collapses Kotak paths into short, loggable buckets.
func endpointClass(path string) string {
	clean := strings.Trim(path, "/")
	switch {
	case strings.HasPrefix(clean, "script-details"):
		return "quotes"
	case strings.HasPrefix(clean, "login"):
		return "login"
	case strings.Contains(clean, "/orders"):
		return "orders"
	case strings.Contains(clean, "/trades"):
		return "trades"
	case strings.Contains(clean, "/positions"):
		return "positions"
	case strings.Contains(clean, "/limits"):
		return "limits"
	case strings.Contains(clean, "/holdings"):
		return "holdings"
	case strings.Contains(clean, "/order/"):
		return "order-write"
	}
	if clean == "" {
		return "other"
	}
	return clean
}

// Report returns the calls made since the previous Report and resets the
// counters. It is intended to be called once a minute by the server.
func Report() (counts map[string]int, limited, errors int) {
	stats.Lock()
	defer stats.Unlock()
	counts = stats.counts
	stats.counts = make(map[string]int)
	limited, errors = stats.limited, stats.errors
	stats.limited, stats.errors = 0, 0
	return counts, limited, errors
}
