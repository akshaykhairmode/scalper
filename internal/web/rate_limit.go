package web

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
)

// rateLimitBucketCap bounds the per-IP bucket map. A flood from many source
// IPs is bounded work: the map only ever holds this many entries and stale
// ones are evicted whenever the cap is exceeded.
const rateLimitBucketCap = 4096

// rateLimiter is a per-IP token bucket in front of the whole HTTP surface.
// It is the application's own flood valve: behind the reverse proxy it keeps
// a single hostile client from burning process CPU, and it is the only line
// of defence if the app is ever exposed directly. Health probes bypass it so
// uptime checks never fail during an attack.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	rate    float64 // tokens per second
	burst   float64
	now     func() time.Time
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(cfg config.RateLimit, now func() time.Time) *rateLimiter {
	if !cfg.Enabled {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{
		buckets: make(map[string]*rateBucket),
		rate:    float64(cfg.PerSecond),
		burst:   float64(cfg.Burst),
		now:     now,
	}
}

// allow consumes one token for the client IP, refilling elapsed time first.
// It reports whether the request may proceed and how long the client should
// wait when it may not.
func (l *rateLimiter) allow(ip string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.buckets[ip]
	if bucket == nil {
		if len(l.buckets) >= rateLimitBucketCap {
			l.evictStaleLocked(now)
		}
		bucket = &rateBucket{tokens: l.burst, last: now}
		l.buckets[ip] = bucket
	} else {
		elapsed := now.Sub(bucket.last).Seconds()
		if elapsed > 0 {
			bucket.tokens += elapsed * l.rate
			if bucket.tokens > l.burst {
				bucket.tokens = l.burst
			}
		}
	}
	bucket.last = now

	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	// Full refill time for one token, floored at one second so the
	// Retry-After header is always actionable.
	wait := time.Duration((1 - bucket.tokens) / l.rate * float64(time.Second))
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

// evictStaleLocked drops buckets idle for over a burst-refill window so a
// one-off IP never occupies the map forever. Called with l.mu held.
func (l *rateLimiter) evictStaleLocked(now time.Time) {
	idle := time.Duration(l.burst/l.rate*2) * time.Second
	if idle < time.Minute {
		idle = time.Minute
	}
	for ip, bucket := range l.buckets {
		if now.Sub(bucket.last) > idle {
			delete(l.buckets, ip)
		}
	}
}

// isRateLimitExemptPath keeps health probes answerable even while an IP is
// being throttled, so uptime checks never fail because of an attack.
func isRateLimitExemptPath(path string) bool {
	return path == "/healthz" || path == "/api/health"
}

// rateLimitMiddleware wraps the mux with the per-IP limiter. Limited
// requests are answered with 429 and a Retry-After header; they still flow
// through the outer access/request logging so abuse is observable.
func (s *Server) rateLimitMiddleware(h http.Handler) http.Handler {
	if s.rateLimiter == nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isRateLimitExemptPath(r.URL.Path) {
			allowed, wait := s.rateLimiter.allow(s.clientIPKey(r))
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
				_ = writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "Too many requests."})
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// clientIPKey resolves the client IP for rate accounting. X-Forwarded-For is
// trusted only when the direct peer is loopback (the reverse proxy on the
// same host), so remote clients cannot spoof their attribution by sending
// the header directly.
func (s *Server) clientIPKey(r *http.Request) string {
	clientIP := r.RemoteAddr
	if isLocalOrLoopbackRemote(r) {
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			if idx := strings.IndexByte(xff, ','); idx >= 0 {
				xff = strings.TrimSpace(xff[:idx])
			}
			if net.ParseIP(xff) != nil {
				clientIP = xff
			}
		}
	}
	if host, _, err := net.SplitHostPort(clientIP); err == nil {
		return host
	}
	return clientIP
}
