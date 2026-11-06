package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/logging"
)

const serveSessionCookie = "scalper_serve_session"

type serveUnlockFailure struct {
	Count        int
	BlockedUntil time.Time
	LastSeen     time.Time
}

func (s *Server) handleServeUnlock(w http.ResponseWriter, r *http.Request) {
	if !requireJSONContentType(r) {
		_ = writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"ok": false, "error": "Content-Type must be application/json"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid request body"})
		return
	}

	cfg := s.configSnapshot()
	ba := cfg.Web.BasicAuth
	if strings.TrimSpace(ba.Username) == "" {
		_ = writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	key := serveUnlockKey(r)
	if wait := s.unlockRetryAfter(key); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		_ = writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "Too many attempts. Try again later."})
		return
	}

	wantUser := sha256.Sum256([]byte(ba.Username))
	wantPass := sha256.Sum256([]byte(ba.Password))
	gotUser := sha256.Sum256([]byte(req.Username))
	gotPass := sha256.Sum256([]byte(req.Password))
	if subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) != 1 ||
		subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) != 1 {
		s.recordUnlockFailure(key)
		s.log.Warn("serve unlock failed", "event", "serve_unlock_failed", "remote", key,
			"request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Invalid username or password"})
		return
	}
	s.clearUnlockFailures(key)

	token, err := newServeSessionID()
	if err != nil {
		s.log.Error("create serve session", "event", "serve_session_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "could not create session"})
		return
	}
	exp := time.Now().Add(24 * time.Hour)
	s.serveSessionsMu.Lock()
	s.serveSessions[token] = exp
	s.serveSessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is set dynamically for HTTPS
		Name:     serveSessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  exp,
	})
	s.log.Info("serve unlock succeeded", "event", "serve_unlock_succeeded", "remote", key,
		"request_id", logging.RequestID(r.Context()))
	_ = writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) isServeAuthenticated(r *http.Request) bool {
	cfg := s.configSnapshot()
	username := strings.TrimSpace(cfg.Web.BasicAuth.Username)
	if username == "" {
		return true
	}
	if cookie, err := r.Cookie(serveSessionCookie); err == nil && cookie.Value != "" && s.validServeSession(cookie.Value) {
		return true
	}
	if u, p, ok := r.BasicAuth(); ok {
		wantUser := sha256.Sum256([]byte(username))
		wantPass := sha256.Sum256([]byte(cfg.Web.BasicAuth.Password))
		gotUser := sha256.Sum256([]byte(u))
		gotPass := sha256.Sum256([]byte(p))
		if subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) == 1 &&
			subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) serveAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requestOriginAllowed(r) {
			_ = writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross-origin request rejected"})
			return
		}

		// Homepage, static assets and the unlock endpoint are reachable before
		// authentication. The homepage contains no account data.
		if r.URL.Path == "/" || r.URL.Path == "/api/auth/serve-unlock" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		if s.isServeAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Do not send WWW-Authenticate so browsers do not show a native dialog.
		_ = writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Terminal locked. Please authenticate."})
	})
}

func newServeSessionID() (string, error) {
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func (s *Server) validServeSession(token string) bool {
	now := time.Now()
	s.serveSessionsMu.Lock()
	defer s.serveSessionsMu.Unlock()
	for id, exp := range s.serveSessions {
		if !now.Before(exp) {
			delete(s.serveSessions, id)
		}
	}
	exp, ok := s.serveSessions[token]
	return ok && now.Before(exp)
}

func (s *Server) clearServeSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124
		Name:     serveSessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func (s *Server) revokeCurrentServeSession(r *http.Request) {
	if cookie, err := r.Cookie(serveSessionCookie); err == nil && cookie.Value != "" {
		s.serveSessionsMu.Lock()
		delete(s.serveSessions, cookie.Value)
		s.serveSessionsMu.Unlock()
	}
}

func (s *Server) revokeAllServeSessions() {
	s.serveSessionsMu.Lock()
	defer s.serveSessionsMu.Unlock()
	s.serveSessions = make(map[string]time.Time)
}

func (s *Server) unlockRetryAfter(key string) time.Duration {
	now := time.Now()
	s.serveUnlockMu.Lock()
	defer s.serveUnlockMu.Unlock()
	state, ok := s.serveUnlockFailures[key]
	if !ok {
		return 0
	}
	if now.Before(state.BlockedUntil) {
		return state.BlockedUntil.Sub(now)
	}
	return 0
}

func (s *Server) recordUnlockFailure(key string) {
	now := time.Now()
	s.serveUnlockMu.Lock()
	defer s.serveUnlockMu.Unlock()
	s.cleanupUnlockFailuresLocked(now)
	state := s.serveUnlockFailures[key]
	state.Count++
	state.LastSeen = now
	if state.Count >= 3 {
		delay := 5 * time.Second * time.Duration(1<<minInt(state.Count-3, 6))
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
		state.BlockedUntil = now.Add(delay)
	}
	s.serveUnlockFailures[key] = state
}

func (s *Server) clearUnlockFailures(key string) {
	s.serveUnlockMu.Lock()
	defer s.serveUnlockMu.Unlock()
	delete(s.serveUnlockFailures, key)
}

func (s *Server) cleanupUnlockFailuresLocked(now time.Time) {
	if len(s.serveUnlockFailures) < 1024 {
		return
	}
	for key, state := range s.serveUnlockFailures {
		if now.Sub(state.LastSeen) > time.Hour {
			delete(s.serveUnlockFailures, key)
		}
	}
}

func serveUnlockKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}

func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if idx := strings.IndexByte(proto, ','); idx >= 0 {
		proto = proto[:idx]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func requestOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		if parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host) {
			return true
		}
		// Allow mobile Capacitor apps, local/emulator development, and sslip.io origins
		if parsed.Scheme == "capacitor" || parsed.Hostname() == "localhost" ||
			parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "10.0.2.2" ||
			strings.HasSuffix(parsed.Hostname(), ".sslip.io") {
			return true
		}
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}
	if origin == "" {
		return true
	}
	return false
}

func requireJSONContentType(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
