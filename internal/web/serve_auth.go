package web

import (
	"context"
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
	"github.com/akshaykhairmode/scalper/internal/store"
)

const (
	serveSessionCookie   = "scalper_serve_session"
	deviceBindingCookie  = "scalper_device_binding"
	serveSessionDuration = 3 * time.Hour
)

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
		Username       string `json:"username"`
		Password       string `json:"password"`
		RememberDevice bool   `json:"remember_device"`
		DeviceName     string `json:"device_name"`
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

	token := s.issueServeSession(w, r)
	if token == "" {
		s.log.Error("create serve session", "event", "serve_session_failed", "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "could not create session"})
		return
	}

	var boundDevID string
	if req.RememberDevice && s.deviceStore != nil {
		deviceName := strings.TrimSpace(req.DeviceName)
		if deviceName == "" {
			ua := r.UserAgent()
			switch {
			case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
				deviceName = "Apple Device (Mobile)"
			case strings.Contains(ua, "Android"):
				deviceName = "Android Device"
			case strings.Contains(ua, "Macintosh"):
				deviceName = "Mac Browser"
			case strings.Contains(ua, "Windows"):
				deviceName = "Windows PC"
			default:
				deviceName = "Trusted Device"
			}
		}
		rawID, err := newServeSessionID()
		if err == nil {
			devID := "dev-" + rawID
			now := time.Now()
			dev := store.Device{
				ID:         devID,
				DeviceName: deviceName,
				PublicKey:  "bound-device",
				Algorithm:  0,
				SignCount:  1,
				CreatedAt:  now,
				LastUsedAt: now,
			}
			if err := s.deviceStore.SaveDevice(r.Context(), dev); err == nil {
				s.setDeviceBindingCookie(w, r, devID)
				boundDevID = devID
			}
		}
	}

	s.log.Info("serve unlock succeeded", "event", "serve_unlock_succeeded", "remote", key,
		"request_id", logging.RequestID(r.Context()))
	resp := map[string]any{"ok": true, "token": token}
	if boundDevID != "" {
		resp["device_id"] = boundDevID
	}
	_ = writeJSON(w, http.StatusOK, resp)
}

func (s *Server) isServeAuthenticated(r *http.Request) bool {
	if s == nil || r == nil {
		return false
	}
	cfg := s.configSnapshot()
	username := strings.TrimSpace(cfg.Web.BasicAuth.Username)

	// 1. Session cookie
	if cookie, err := r.Cookie(serveSessionCookie); err == nil && cookie.Value != "" && s.validServeSession(cookie.Value) {
		return true
	}
	// 2. Custom header session token
	if token := strings.TrimSpace(r.Header.Get("X-Serve-Session")); token != "" && s.validServeSession(token) {
		return true
	}
	// 3. Bearer token
	if authHdr := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(authHdr), "bearer ") {
		token := strings.TrimSpace(authHdr[7:])
		if token != "" && s.validServeSession(token) {
			return true
		}
	}
	// 4. BasicAuth
	if username != "" {
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

	// 5. If basic auth is NOT configured, check if biometric devices are enrolled
	if s.deviceStore != nil {
		if count, err := s.deviceStore.CountDevices(r.Context()); err == nil && count > 0 {
			// Biometric enrollment is active: unauthenticated requests cannot proceed
			return false
		}
	}

	return true
}

func (s *Server) isDeviceBound(r *http.Request) bool {
	if s == nil || s.deviceStore == nil || r == nil {
		return false
	}
	var deviceID string
	if cookie, err := r.Cookie(deviceBindingCookie); err == nil && strings.TrimSpace(cookie.Value) != "" {
		deviceID = strings.TrimSpace(cookie.Value)
	} else if hdr := r.Header.Get("X-Device-ID"); strings.TrimSpace(hdr) != "" {
		deviceID = strings.TrimSpace(hdr)
	}
	if deviceID == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	dev, err := s.deviceStore.GetDevice(ctx, deviceID)
	if err != nil || dev == nil {
		return false
	}
	// Enforce 90-day device lifetime / inactivity expiration
	lastActive := dev.LastUsedAt
	if lastActive.IsZero() {
		lastActive = dev.CreatedAt
	}
	if !lastActive.IsZero() && time.Since(lastActive) > 90*24*time.Hour {
		return false
	}
	_ = s.deviceStore.UpdateDeviceUsage(ctx, dev.ID, dev.SignCount, time.Now())
	return true
}

func (s *Server) setDeviceBindingCookie(w http.ResponseWriter, r *http.Request, deviceID string) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124
		Name:     deviceBindingCookie,
		Value:    deviceID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   365 * 24 * 3600, // 1 year
	})
}

func (s *Server) clearDeviceBindingCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124
		Name:     deviceBindingCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) issueServeSession(w http.ResponseWriter, r *http.Request) string {
	token, err := newServeSessionID()
	if err != nil {
		return ""
	}
	exp := time.Now().Add(serveSessionDuration)
	s.serveSessionsMu.Lock()
	s.serveSessions[token] = exp
	s.serveSessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{ // #nosec G124
		Name:     serveSessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
	})
	return token
}

// POST /api/auth/device-unlock
// Allows a previously registered / bound device (via X-Device-ID, JSON device_id, or cookie)
// to automatically unlock the terminal without requiring username and password.
func (s *Server) handleDeviceUnlock(w http.ResponseWriter, r *http.Request) {
	if s.deviceStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Device storage unavailable"})
		return
	}

	var req struct {
		DeviceID string `json:"device_id"`
	}
	if requireJSONContentType(r) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		if cookie, err := r.Cookie(deviceBindingCookie); err == nil && strings.TrimSpace(cookie.Value) != "" {
			deviceID = strings.TrimSpace(cookie.Value)
		} else if hdr := r.Header.Get("X-Device-ID"); strings.TrimSpace(hdr) != "" {
			deviceID = strings.TrimSpace(hdr)
		}
	}

	if deviceID == "" {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "device_id required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	dev, err := s.deviceStore.GetDevice(ctx, deviceID)
	if err != nil || dev == nil {
		_ = writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Device not recognized or revoked"})
		return
	}

	now := time.Now()
	_ = s.deviceStore.UpdateDeviceUsage(ctx, dev.ID, dev.SignCount, now)

	s.setDeviceBindingCookie(w, r, dev.ID)
	token := s.issueServeSession(w, r)
	s.clearUnlockFailures(serveUnlockKey(r))

	s.log.Info("device unlock succeeded", "device_name", dev.DeviceName, "id", dev.ID, "request_id", logging.RequestID(r.Context()))
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"token":       token,
		"device_id":   dev.ID,
		"device_name": dev.DeviceName,
	})
}

func (s *Server) serveAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requestOriginAllowed(r) {
			_ = writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross-origin request rejected"})
			return
		}

		// Homepage, static assets, unlock endpoint, device unlock, biometric login and device discovery are reachable before
		// authentication. The homepage contains no account data.
		if r.URL.Path == "/" ||
			r.URL.Path == "/api/auth/serve-unlock" ||
			r.URL.Path == "/api/auth/device-unlock" ||
			r.URL.Path == "/sw.js" ||
			r.URL.Path == "/manifest.webmanifest" ||
			r.URL.Path == "/manifest.json" ||
			r.URL.Path == "/favicon.ico" ||
			r.URL.Path == "/robots.txt" ||
			strings.HasPrefix(r.URL.Path, "/static/") ||
			r.URL.Path == "/api/auth/webauthn/login/begin" ||
			r.URL.Path == "/api/auth/webauthn/login/finish" ||
			(r.URL.Path == "/api/devices" && r.Method == http.MethodGet) {
			next.ServeHTTP(w, r)
			return
		}

		// Device enrollment using a temporary pairing token
		if (r.URL.Path == "/api/auth/webauthn/register/begin" || r.URL.Path == "/api/auth/webauthn/register/finish" ||
			r.URL.Path == "/api/devices/bind") &&
			s.validPairToken(extractPairToken(r)) {
			next.ServeHTTP(w, r)
			return
		}

		if s.isServeAuthenticated(r) || s.isDeviceBound(r) {
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
		SameSite: http.SameSiteLaxMode,
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

func (s *Server) requestOriginAllowed(r *http.Request) bool {
	// 1. Requests from Scalper native Android container (Capacitor sets X-Requested-With package ID)
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Requested-With")), "com.scalper.terminal") {
		return true
	}

	// 2. Allow top-level browser/webview document navigations (e.g. following pairing QR link or terminal launch)
	if r.Method == http.MethodGet && (strings.EqualFold(r.Header.Get("Sec-Fetch-Mode"), "navigate") ||
		strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "document")) {
		return true
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		return s.isAllowedOrigin(r, origin)
	}

	// 3. Reject unapproved cross-site fetch or API requests
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}

	return true
}

// isAllowedOrigin reports whether an origin is permitted to access the server.
// Allowed: same host (PWA/web), Capacitor/Ionic schemes, localhost/private LAN,
// and origins explicitly declared in cfg.Web.AllowedOrigins.
func (s *Server) isAllowedOrigin(r *http.Request, origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	// Untrusted or sandboxed "null" origin is strictly rejected to prevent iframe CSRF attacks.
	if strings.EqualFold(origin, "null") {
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed == nil {
		return false
	}

	// 1. Mobile native app container schemes (Capacitor / Ionic)
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "capacitor" || scheme == "ionic" {
		if parsed.Hostname() == "localhost" || parsed.Hostname() == "" {
			return true
		}
	}

	// 2. Direct match with Request Host (covers same-origin PWA & web requests)
	if r != nil && r.Host != "" {
		if strings.EqualFold(parsed.Host, r.Host) {
			return true
		}
		if strings.EqualFold(parsed.Hostname(), stripPort(r.Host)) {
			return true
		}
	}

	// 2b. Reverse proxy forwarded host (only trusted from loopback / local proxies)
	if r != nil && isLocalOrLoopbackRemote(r) {
		fwdHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
		if idx := strings.IndexByte(fwdHost, ','); idx >= 0 {
			fwdHost = strings.TrimSpace(fwdHost[:idx])
		}
		if fwdHost != "" {
			if strings.EqualFold(parsed.Host, fwdHost) || strings.EqualFold(parsed.Hostname(), stripPort(fwdHost)) {
				return true
			}
		}
	}

	// 3. User configured allowed origins in config.yaml / environment
	if s != nil {
		cfg := s.configSnapshot()
		for _, allowed := range cfg.Web.AllowedOrigins {
			allowed = strings.TrimSpace(allowed)
			if allowed == "" {
				continue
			}
			if strings.EqualFold(origin, allowed) {
				return true
			}
			if aURL, err := url.Parse(allowed); err == nil && aURL != nil {
				if strings.EqualFold(parsed.Host, aURL.Host) || strings.EqualFold(parsed.Hostname(), aURL.Hostname()) {
					return true
				}
			}
		}
	}

	// 4. Localhost, loopback, Android emulator host, and Tailscale private mesh
	hostname := parsed.Hostname()
	hLower := strings.ToLower(hostname)
	if strings.HasSuffix(hLower, ".ts.net") || strings.HasSuffix(hLower, ".tailscale.net") {
		return true
	}
	if hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1" || hostname == "10.0.2.2" || hostname == "10.0.3.2" {
		return true
	}
	if ip := net.ParseIP(hostname); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return true
		}
	}

	return false
}

func isAllowedOriginStatic(r *http.Request, origin string) bool {
	return (*Server)(nil).isAllowedOrigin(r, origin)
}

func stripPort(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err == nil {
		return h
	}
	return host
}

func isLocalOrLoopbackRemote(r *http.Request) bool {
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || host == "192.0.2.1"
	}
	return host == "localhost" || host == "127.0.0.1"
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
