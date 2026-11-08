package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/store"
)

func TestSecurityGatedEndpointsRequireAuth(t *testing.T) {
	// Case 1: Server has NO auth configured (no basic auth and no enrolled devices)
	cfgNoAuth := testConfig()
	cfgNoAuth.Web.BasicAuth.Username = ""
	cfgNoAuth.Web.BasicAuth.Password = ""
	sNoAuth := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfgNoAuth})
	defer sNoAuth.Close()

	gatedPaths := []string{
		"/logs",
		"/api/logs",
		"/settings",
		"/api/config",
	}

	for _, path := range gatedPaths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		sNoAuth.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("path %s without auth mechanism: expected 403 Forbidden, got %d", path, rec.Code)
		}
	}

	// Case 2: Server HAS basic auth configured, but request is unauthenticated
	cfgAuth := testConfig()
	cfgAuth.Web.BasicAuth.Username = "admin"
	cfgAuth.Web.BasicAuth.Password = "adminpass"
	sAuth := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfgAuth})
	defer sAuth.Close()

	for _, path := range gatedPaths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		sAuth.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("path %s unauthenticated: expected 401 Unauthorized, got %d", path, rec.Code)
		}
	}

	// Case 3: Server has basic auth configured, and valid credentials are sent
	for _, path := range gatedPaths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetBasicAuth("admin", "adminpass")
		rec := httptest.NewRecorder()
		sAuth.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("path %s authenticated: expected 200 OK, got %d", path, rec.Code)
		}
	}
}

func TestLoginRateLimiterAndCircuitBreaker(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()

	// 1. IP rate limiting: max 5 attempts per 5 minutes from same IP
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/login/totp", strings.NewReader(`{"totp":"123456"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.195:45000"
		waitSec, msg := s.checkLoginAllowed(req)
		if waitSec != 0 || msg != "" {
			t.Fatalf("attempt %d should be allowed, got waitSec=%d msg=%s", i+1, waitSec, msg)
		}
	}

	// 6th attempt from the same IP must be rejected with 429 rate limit
	req6 := httptest.NewRequest(http.MethodPost, "/api/login/totp", strings.NewReader(`{"totp":"123456"}`))
	req6.Header.Set("Content-Type", "application/json")
	req6.RemoteAddr = "203.0.113.195:45000"
	waitSec, msg := s.checkLoginAllowed(req6)
	if waitSec <= 0 || !strings.Contains(msg, "Too many login attempts") {
		t.Fatalf("6th attempt should be blocked: waitSec=%d, msg=%s", waitSec, msg)
	}

	// Different IP should still be allowed
	reqOtherIP := httptest.NewRequest(http.MethodPost, "/api/login/totp", strings.NewReader(`{"totp":"123456"}`))
	reqOtherIP.Header.Set("Content-Type", "application/json")
	reqOtherIP.RemoteAddr = "198.51.100.4:45000"
	waitOther, _ := s.checkLoginAllowed(reqOtherIP)
	if waitOther != 0 {
		t.Fatalf("different IP should be allowed, got waitSec=%d", waitOther)
	}

	// 2. Broker lockout circuit breaker (2-strike rule)
	s.recordBrokerLoginFailure()
	if s.loginFailures != 1 {
		t.Fatalf("failures = %d, want 1", s.loginFailures)
	}
	if !s.loginBlockedTill.IsZero() {
		t.Fatal("breaker should not trip on 1 failure")
	}

	// Second consecutive failure trips the breaker
	s.recordBrokerLoginFailure()
	if s.loginFailures != 2 {
		t.Fatalf("failures = %d, want 2", s.loginFailures)
	}
	if s.loginBlockedTill.IsZero() {
		t.Fatal("breaker should trip on 2 failures")
	}

	// Requests from ANY IP should now be blocked by the broker lockout circuit breaker
	reqAny := httptest.NewRequest(http.MethodPost, "/api/login/totp", strings.NewReader(`{"totp":"123456"}`))
	reqAny.Header.Set("Content-Type", "application/json")
	reqAny.RemoteAddr = "198.51.100.99:45000"
	waitLocked, lockMsg := s.checkLoginAllowed(reqAny)
	if waitLocked <= 0 || !strings.Contains(lockMsg, "Kotak account lockout") {
		t.Fatalf("expected broker lockout breaker message, got wait=%d msg=%s", waitLocked, lockMsg)
	}

	// Successful login resets the circuit breaker
	s.recordBrokerLoginSuccess()
	if s.loginFailures != 0 || !s.loginBlockedTill.IsZero() {
		t.Fatal("breaker should be reset on login success")
	}
}

func TestAtomicPairTokenConsumption(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()

	token, expiresAt, err := s.createPairToken()
	if err != nil {
		t.Fatalf("createPairToken: %v", err)
	}
	if len(token) != 64 { // 32 bytes hex encoded = 64 characters (256 bits)
		t.Fatalf("token length = %d, want 64", len(token))
	}
	if expiresAt.Before(time.Now()) {
		t.Fatal("expiresAt should be in future")
	}

	// First presentation succeeds and atomically deletes token
	if !s.takePairToken(token) {
		t.Fatal("first takePairToken should succeed")
	}

	// Immediate second presentation must fail (replay attack prevented)
	if s.takePairToken(token) {
		t.Fatal("second takePairToken must fail (token was already consumed)")
	}
}

func TestDeviceMaskingInDeviceList(t *testing.T) {
	dir := t.TempDir()
	storeDB, err := store.Open(context.Background(), store.Options{Path: dir + "/test.db"})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer func() { _ = storeDB.Close() }()

	cfg := testConfig()
	s := newTestServer(t, serverOpts{db: fakeDB{}, deviceStore: storeDB, cfg: &cfg})
	defer s.Close()

	// Save a device with private key / public key material
	dev := store.Device{
		ID:         "dev-secret123",
		DeviceName: "My Phone",
		PublicKey:  "SECRET_CRYPTO_PUBLIC_KEY_PEM_DATA",
		Algorithm:  -7,
		SignCount:  42,
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
	}
	if err := storeDB.SaveDevice(context.Background(), dev); err != nil {
		t.Fatalf("SaveDevice: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Host = "127.0.0.1:8080"
	req.SetBasicAuth("admin", "adminpass")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if strings.Contains(body, "SECRET_CRYPTO_PUBLIC_KEY") {
		t.Fatalf("PublicKey leaked in device list response: %s", body)
	}
	if strings.Contains(body, "sign_count") {
		t.Fatalf("sign_count should not be returned in device list: %s", body)
	}
	if !strings.Contains(body, "My Phone") || !strings.Contains(body, "dev-secret123") {
		t.Fatalf("expected device_name and id in response: %s", body)
	}
}

func TestServeAuthMiddlewareAllowedOrigins(t *testing.T) {
	cfg := testConfig()
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "adminpass"
	cfg.Web.AllowedOrigins = []string{"https://app.mytrustedscalper.com"}
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})
	defer s.Close()

	// 1. Request with configured allowed origin should pass origin check
	reqAllowed := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	reqAllowed.Host = "scalper.internal:8080"
	reqAllowed.Header.Set("Origin", "https://app.mytrustedscalper.com")
	reqAllowed.SetBasicAuth("admin", "adminpass")
	recAllowed := httptest.NewRecorder()
	s.Handler().ServeHTTP(recAllowed, reqAllowed)
	if recAllowed.Code == http.StatusForbidden {
		t.Fatalf("allowed origin https://app.mytrustedscalper.com was rejected with 403: %s", recAllowed.Body.String())
	}

	// 2. Request with untrusted origin should be rejected with 403 Forbidden
	reqEvil := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	reqEvil.Host = "scalper.internal:8080"
	reqEvil.Header.Set("Origin", "https://evil-phishing.com")
	reqEvil.SetBasicAuth("admin", "adminpass")
	recEvil := httptest.NewRecorder()
	s.Handler().ServeHTTP(recEvil, reqEvil)
	if recEvil.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for untrusted origin, got %d", recEvil.Code)
	}

	// 3. Request with 'null' origin should be rejected with 403 Forbidden
	reqNull := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	reqNull.Host = "scalper.internal:8080"
	reqNull.Header.Set("Origin", "null")
	reqNull.SetBasicAuth("admin", "adminpass")
	recNull := httptest.NewRecorder()
	s.Handler().ServeHTTP(recNull, reqNull)
	if recNull.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for null origin, got %d", recNull.Code)
	}

	// 4. Capacitor mobile app scheme should be accepted
	reqCap := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	reqCap.Host = "scalper.internal:8080"
	reqCap.Header.Set("Origin", "capacitor://localhost")
	reqCap.SetBasicAuth("admin", "adminpass")
	recCap := httptest.NewRecorder()
	s.Handler().ServeHTTP(recCap, reqCap)
	if recCap.Code == http.StatusForbidden {
		t.Fatalf("capacitor://localhost origin should be allowed, got 403: %s", recCap.Body.String())
	}
}

func TestEmptyBasicAuthWithEnrolledDevicesRejectsUnauthenticated(t *testing.T) {
	dir := t.TempDir()
	storeDB, err := store.Open(context.Background(), store.Options{Path: dir + "/test.db"})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer func() { _ = storeDB.Close() }()

	// Basic auth is unconfigured / empty
	cfg := testConfig()
	cfg.Web.BasicAuth.Username = ""
	cfg.Web.BasicAuth.Password = ""
	s := newTestServer(t, serverOpts{db: fakeDB{}, deviceStore: storeDB, cfg: &cfg})
	defer s.Close()

	// Enrolling a device activates authentication enforcement
	dev := store.Device{
		ID:         "dev-biometric-1",
		DeviceName: "Enrolled Phone",
		PublicKey:  "pubkey",
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
	}
	if err := storeDB.SaveDevice(context.Background(), dev); err != nil {
		t.Fatalf("SaveDevice: %v", err)
	}

	// Unauthenticated request to /settings or /api/session must be rejected
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized when devices are enrolled and no session present, got %d", rec.Code)
	}
}
