package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
)

func rateLimitTestConfig(perSecond, burst int) config.Config {
	cfg := testConfig()
	cfg.Web.RateLimit = config.RateLimit{Enabled: true, PerSecond: perSecond, Burst: burst}
	return cfg
}

func TestRateLimitMiddlewareBlocksFloods(t *testing.T) {
	cfg := rateLimitTestConfig(1, 2)
	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	clock := base
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg, now: func() time.Time { return clock }})

	// The burst of two passes; the third request from the same IP is
	// limited with an actionable retry hint.
	for i := 0; i < 2; i++ {
		rec := do(t, s, localRequest(http.MethodGet, "/healthz/../api/indices"))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d within the burst was limited", i+1)
		}
	}
	rec := do(t, s, localRequest(http.MethodGet, "/api/indices"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("limited response must carry a Retry-After header")
	}

	// Health probes stay answerable while the IP is throttled.
	for _, path := range []string{"/healthz", "/api/health"} {
		rec := do(t, s, localRequest(http.MethodGet, path))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("health probe %s was limited", path)
		}
	}

	// Tokens refill at the configured rate: one clock second admits another
	// request from the same IP.
	clock = base.Add(2 * time.Second)
	rec = do(t, s, localRequest(http.MethodGet, "/api/indices"))
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("request after refill window was still limited")
	}

	// A different IP has its own bucket and is unaffected throughout.
	req := localRequest(http.MethodGet, "/api/indices")
	req.RemoteAddr = "10.9.9.9:4321"
	rec = do(t, s, req)
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("second IP was limited by the first IP's bucket")
	}
}

func TestRateLimitIgnoresSpoofedForwardedForFromRemotePeers(t *testing.T) {
	cfg := rateLimitTestConfig(1, 2)
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})

	// A non-loopback client claiming to be someone else via
	// X-Forwarded-For must not escape its own bucket.
	limited := 0
	for i := 0; i < 6; i++ {
		req := localRequest(http.MethodGet, "/api/indices")
		req.RemoteAddr = "203.0.113.7:1000"
		req.Header.Set("X-Forwarded-For", "1.1.1.1")
		rec := do(t, s, req)
		if rec.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("spoofed X-Forwarded-For let a remote peer escape its bucket")
	}
}

func TestRateLimitDisabledPassesEverything(t *testing.T) {
	cfg := testConfig()
	cfg.Web.RateLimit = config.RateLimit{Enabled: false, PerSecond: 1, Burst: 1}
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})
	for i := 0; i < 10; i++ {
		rec := do(t, s, localRequest(http.MethodGet, "/healthz/../api/indices"))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d limited while the limiter is disabled", i+1)
		}
	}
}

func TestFeedConnectionCapRejectsWhenFull(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	s.feedMu.Lock()
	for i := 0; i < maxFeedConnections; i++ {
		s.feedClients[&feedClient{}] = struct{}{}
	}
	s.feedMu.Unlock()

	rec := do(t, s, localRequest(http.MethodGet, "/ws"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("ws connect with a full cap = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("refused ws connection must carry a Retry-After header")
	}
}

func TestFeedConnectionPerIPCapRejectsSpread(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	// httptest requests carry the default remote address 192.0.2.1:1234.
	s.feedMu.Lock()
	s.feedClientIPs["192.0.2.1"] = maxFeedConnectionsPerIP
	s.feedMu.Unlock()

	rec := do(t, s, localRequest(http.MethodGet, "/ws"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("ws connect at the per-IP cap = %d, want 429", rec.Code)
	}

	// A different IP passes the cap check and proceeds into the handler's
	// regular gates (auth/feed availability), which is any status but 429.
	req := localRequest(http.MethodGet, "/ws")
	req.RemoteAddr = "10.8.8.8:2222"
	rec = do(t, s, req)
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("second IP was refused by the first IP's connection cap")
	}
}
