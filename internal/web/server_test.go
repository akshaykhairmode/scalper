package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/akshaykhairmode/scalper/internal/auth"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testConfig() config.Config {
	c := config.Default()
	c.Kotak.MobileNumber = "+919999999999"
	c.Kotak.UCC = "TEST"
	return c
}

type serverOpts struct {
	db          DBPinger
	degraded    bool
	token       bool
	mpin        string
	now         func() time.Time
	started     time.Time
	cfg         *config.Config
	authClient  auth.KotakAuthenticator
	marketAPI   MarketAPI
	noTokenFunc bool
}

func newTestServer(t *testing.T, o serverOpts) *Server {
	t.Helper()
	cfg := testConfig()
	if o.cfg != nil {
		cfg = *o.cfg
	}
	now := o.now
	if now == nil {
		now = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }
	}
	started := o.started
	if started.IsZero() {
		started = now().Add(-90 * time.Minute)
	}
	opts := Options{
		Logger:      testLogger(),
		Config:      cfg,
		MPIN:        o.mpin,
		AuthClient:  o.authClient,
		Market:      o.marketAPI,
		DB:          o.db,
		LogDegraded: func() bool { return o.degraded },
		Started:     started,
		Now:         now,
	}
	if !o.noTokenFunc {
		opts.TokenPresent = func() bool { return o.token }
	}
	s, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestWebSocketUpgradeThroughMiddleware(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	s.hsm = market.NewHSMFeed(testConfig(), nil, testLogger())
	defer s.Close()

	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()

	endpoint := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	conn, response, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("dial WebSocket: %v (response: %+v)", err, response)
	}
	defer conn.Close()

	var status struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&status); err != nil {
		t.Fatalf("read initial feed status: %v", err)
	}
	if status.Type != "feed_status" {
		t.Fatalf("initial message type = %q, want feed_status", status.Type)
	}
}

type fakeMarketAPI struct {
	quote      market.Quote
	instrument scrip.Option
}

func (f fakeMarketAPI) Indices(context.Context, string) ([]market.IndexQuote, error) {
	return []market.IndexQuote{{Key: "NIFTY", LTP: 25_000}}, nil
}

func (f fakeMarketAPI) Expiries(string) ([]scrip.ExpiryInfo, error) {
	return []scrip.ExpiryInfo{{Date: f.instrument.Expiry}}, nil
}

func (f fakeMarketAPI) Chain(context.Context, string, string, string) ([]market.ChainRow, error) {
	return []market.ChainRow{}, nil
}

func (f fakeMarketAPI) Quote(context.Context, string, string) (market.Quote, error) {
	return f.quote, nil
}

func (f fakeMarketAPI) Quotes(_ context.Context, _ string, keys []string) (map[string]market.Quote, error) {
	quotes := make(map[string]market.Quote, len(keys))
	for _, key := range keys {
		quotes[key] = f.quote
	}
	return quotes, nil
}

func (fakeMarketAPI) Candles(context.Context, string, string, string) ([]market.Candle, error) {
	return []market.Candle{{Time: 1793092500, Open: 100, High: 102, Low: 99, Close: 101, Volume: 42}}, nil
}

func (f fakeMarketAPI) Option(key string) (scrip.Option, bool) {
	return f.instrument, key == f.instrument.InstrumentKey
}

func (f fakeMarketAPI) FindOption(segment, symbol string) (scrip.Option, bool) {
	return f.instrument, segment == f.instrument.ExchangeSegment && symbol == f.instrument.PSymbol
}

func TestCandlesRoute(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}, marketAPI: fakeMarketAPI{}})
	rec := do(t, s, localRequest(http.MethodGet, "/api/candles?instrument=nse_fo%7CNIFTY%7C2026-10-29%7C25000%7CCE&tf=5m"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Candles []market.Candle `json:"candles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Candles) != 1 || response.Candles[0].Close != 101 {
		t.Fatalf("candles = %+v", response.Candles)
	}
}

type testKotakAuthenticator struct {
	view     auth.ViewSession
	trade    auth.TradeSession
	totp     string
	mpin     string
	viewUsed auth.ViewSession
}

func (f *testKotakAuthenticator) LoginTOTP(_ context.Context, totp string) (auth.ViewSession, error) {
	f.totp = totp
	return f.view, nil
}

func (f *testKotakAuthenticator) ValidateMPIN(_ context.Context, view auth.ViewSession, mpin string) (auth.TradeSession, error) {
	f.viewUsed = view
	f.mpin = mpin
	return f.trade, nil
}

func do(t *testing.T, s *Server, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func localRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:8080"
	return req
}

func TestIndexPage(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}, token: true})
	rec := do(t, s, localRequest(http.MethodGet, "/"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	for _, want := range []string{"Scalper", "NIFTY", "BANKNIFTY", "SENSEX", "present", "ready"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index body missing %q", want)
		}
	}
	if strings.Contains(body, "SIMULATED") || strings.Contains(body, "data-demo") {
		t.Fatal("index page should not render fake-data controls")
	}
	if !strings.Contains(body, "id=\"mpin-input\"") {
		t.Fatal("index page without configured MPIN should render the MPIN field")
	}

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestKotakLoginUsesConfiguredMPINWithoutExposingIt(t *testing.T) {
	const configuredMPIN = "654321"
	client := &testKotakAuthenticator{
		view:  auth.ViewSession{Token: "view-token-secret", SID: "view-sid-secret"},
		trade: auth.TradeSession{Token: "trade-token-secret", SID: "trade-sid-secret", BaseURL: "https://cis.kotaksecurities.com"},
	}
	s := newTestServer(t, serverOpts{db: fakeDB{}, authClient: client, mpin: configuredMPIN})

	pageRec := do(t, s, localRequest(http.MethodGet, "/"))
	if pageRec.Code != http.StatusOK {
		t.Fatalf("index status = %d, body = %s", pageRec.Code, pageRec.Body.String())
	}
	if strings.Contains(pageRec.Body.String(), "id=\"mpin-input\"") ||
		strings.Contains(pageRec.Body.String(), configuredMPIN) {
		t.Fatal("configured MPIN field or secret was rendered in the page")
	}
	if !strings.Contains(pageRec.Body.String(), `"mpinConfigured":true`) {
		t.Fatal("bootstrap config should indicate that MPIN is configured")
	}

	totpReq := localRequest(http.MethodPost, "/api/auth/totp")
	totpReq.Header.Set("Content-Type", "application/json")
	totpReq.Body = io.NopCloser(strings.NewReader(`{"totp":"123456"}`))
	totpRec := do(t, s, totpReq)
	if totpRec.Code != http.StatusOK {
		t.Fatalf("TOTP status = %d, body = %s", totpRec.Code, totpRec.Body.String())
	}
	if client.totp != "123456" || client.mpin != configuredMPIN || client.viewUsed != client.view {
		t.Fatalf("auto-login inputs: totp=%q mpin=%q view=%+v", client.totp, client.mpin, client.viewUsed)
	}
	for _, secret := range []string{
		configuredMPIN, "view-token-secret", "view-sid-secret",
		"trade-token-secret", "trade-sid-secret", "cis.kotaksecurities.com",
	} {
		if strings.Contains(totpRec.Body.String(), secret) {
			t.Fatalf("TOTP response leaked %q", secret)
		}
	}
	var clearedFlowCookie bool
	for _, cookie := range totpRec.Result().Cookies() {
		if cookie.Name == authFlowCookieName && cookie.MaxAge < 0 {
			clearedFlowCookie = true
		}
	}
	if !clearedFlowCookie {
		t.Fatal("auto-login should clear the auth flow cookie")
	}
	if !strings.Contains(totpRec.Body.String(), `"login_method":"mpin"`) {
		t.Fatalf("TOTP response does not contain the completed session: %s", totpRec.Body.String())
	}
}

func TestKotakLoginHandlersKeepTokensServerSide(t *testing.T) {
	client := &testKotakAuthenticator{
		view:  auth.ViewSession{Token: "view-token-secret", SID: "view-sid-secret"},
		trade: auth.TradeSession{Token: "trade-token-secret", SID: "trade-sid-secret", BaseURL: "https://cis.kotaksecurities.com"},
	}
	s := newTestServer(t, serverOpts{db: fakeDB{}, authClient: client})

	totpReq := localRequest(http.MethodPost, "/api/auth/totp")
	totpReq.Header.Set("Content-Type", "application/json")
	totpReq.Body = io.NopCloser(strings.NewReader(`{"totp":"123456"}`))
	totpRec := do(t, s, totpReq)
	if totpRec.Code != http.StatusOK {
		t.Fatalf("TOTP status = %d, body = %s", totpRec.Code, totpRec.Body.String())
	}
	if client.totp != "123456" {
		t.Fatalf("TOTP sent to client = %q", client.totp)
	}
	cookies := totpRec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authFlowCookieName || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("auth flow cookie = %+v", cookies)
	}
	for _, secret := range []string{"view-token-secret", "view-sid-secret"} {
		if strings.Contains(totpRec.Body.String(), secret) {
			t.Fatalf("TOTP response leaked %q", secret)
		}
	}

	mpinReq := localRequest(http.MethodPost, "/api/auth/mpin")
	mpinReq.Header.Set("Content-Type", "application/json")
	mpinReq.Body = io.NopCloser(strings.NewReader(`{"mpin":"654321"}`))
	mpinReq.AddCookie(cookies[0])
	mpinRec := do(t, s, mpinReq)
	if mpinRec.Code != http.StatusOK {
		t.Fatalf("MPIN status = %d, body = %s", mpinRec.Code, mpinRec.Body.String())
	}
	if client.mpin != "654321" || client.viewUsed != client.view {
		t.Fatalf("MPIN client inputs: mpin=%q view=%+v", client.mpin, client.viewUsed)
	}
	for _, secret := range []string{"trade-token-secret", "trade-sid-secret", "cis.kotaksecurities.com"} {
		if strings.Contains(mpinRec.Body.String(), secret) {
			t.Fatalf("MPIN response leaked %q", secret)
		}
	}
	statusRec := do(t, s, localRequest(http.MethodGet, "/api/session"))
	if !strings.Contains(statusRec.Body.String(), `"authenticated":true`) ||
		!strings.Contains(statusRec.Body.String(), `"paper_mode":false`) {
		t.Fatalf("session status = %s", statusRec.Body.String())
	}
	healthRec := do(t, s, localRequest(http.MethodGet, "/api/health"))
	var health healthResponse
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health after login: %v", err)
	}
	if health.Mode != "live" {
		t.Fatalf("health mode after login = %q, want live", health.Mode)
	}
}

func TestKotakLoginRequiresFlowAndConsumerKey(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})

	mpinReq := localRequest(http.MethodPost, "/api/auth/mpin")
	mpinReq.Body = io.NopCloser(strings.NewReader(`{"mpin":"123456"}`))
	mpinRec := do(t, s, mpinReq)
	if mpinRec.Code != http.StatusConflict {
		t.Fatalf("MPIN without TOTP flow status = %d, want %d", mpinRec.Code, http.StatusConflict)
	}

	totpReq := localRequest(http.MethodPost, "/api/auth/totp")
	totpReq.Body = io.NopCloser(strings.NewReader(`{"totp":"123456"}`))
	totpRec := do(t, s, totpReq)
	if totpRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("TOTP without consumer key status = %d, want %d", totpRec.Code, http.StatusServiceUnavailable)
	}
}

func TestIndexWithoutTokenAndDegraded(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}, token: false, degraded: true})
	rec := do(t, s, localRequest(http.MethodGet, "/"))
	body := rec.Body.String()
	if !strings.Contains(body, "not set") {
		t.Fatal("token should be reported as not set")
	}

	if !strings.Contains(body, "degraded") {
		t.Fatal("log sink should be reported as degraded")
	}
}

func TestLegacyPaperOrderEndpointUnavailable(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	req := localRequest(http.MethodPost, "/api/orders")
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(strings.NewReader(`{"mode":"paper"}`))
	rec := do(t, s, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("legacy paper order status = %d, want 405", rec.Code)
	}
}

func TestIndexUnknownPath(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	rec := do(t, s, localRequest(http.MethodGet, "/nope"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHealthOK(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}, token: true})
	rec := do(t, s, localRequest(http.MethodGet, "/api/health"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "ok" || resp.Mode != "live" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Checks["database"].Status != "ok" {
		t.Fatalf("database check = %+v", resp.Checks["database"])
	}
	if resp.Checks["session"].Status != "ok" {
		t.Fatalf("session check = %+v", resp.Checks["session"])
	}
	if resp.UptimeSeconds != 5400 {
		t.Fatalf("uptime = %d", resp.UptimeSeconds)
	}
	if resp.Version["go_version"] == nil {
		t.Fatal("version metadata missing")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestHealthUnhealthy(t *testing.T) {
	t.Run("database failure", func(t *testing.T) {
		s := newTestServer(t, serverOpts{db: fakeDB{err: errors.New("down")}})
		rec := do(t, s, localRequest(http.MethodGet, "/healthz"))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
		var resp healthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Status != "fail" || resp.Checks["database"].Status != "fail" {
			t.Fatalf("response = %+v", resp)
		}
	})
	t.Run("no database configured", func(t *testing.T) {
		s := newTestServer(t, serverOpts{})
		rec := do(t, s, localRequest(http.MethodGet, "/healthz"))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	})
	t.Run("degraded logging", func(t *testing.T) {
		s := newTestServer(t, serverOpts{db: fakeDB{}, degraded: true})
		rec := do(t, s, localRequest(http.MethodGet, "/healthz"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp healthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Status != "degraded" {
			t.Fatalf("status = %q, want degraded", resp.Status)
		}
	})
	t.Run("no token", func(t *testing.T) {
		s := newTestServer(t, serverOpts{db: fakeDB{}, token: false})
		rec := do(t, s, localRequest(http.MethodGet, "/healthz"))
		var resp healthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Checks["session"].Status != "unauthenticated" {
			t.Fatalf("session check = %+v", resp.Checks["session"])
		}
	})
}

func TestHealthWithoutCallbacks(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}, noTokenFunc: true})
	rec := do(t, s, localRequest(http.MethodGet, "/healthz"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Checks["logging"].Status != "ok" {
		t.Fatalf("logging check = %+v", resp.Checks["logging"])
	}
}

func TestLocalOnly(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	cases := []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{name: "ipv4", host: "127.0.0.1:8080", want: http.StatusOK},
		{name: "localhost", host: "localhost:8080", want: http.StatusOK},
		{name: "ipv6", host: "[::1]:8080", want: http.StatusOK},
		{name: "bare host", host: "127.0.0.1", want: http.StatusOK},
		{name: "remote host", host: "example.com:8080", want: http.StatusForbidden},
		{name: "remote ip", host: "10.0.0.1:8080", want: http.StatusForbidden},
		{name: "local origin", host: "127.0.0.1:8080", origin: "http://localhost:8080", want: http.StatusOK},
		{name: "remote origin", host: "127.0.0.1:8080", origin: "http://evil.test", want: http.StatusForbidden},
		{name: "invalid origin", host: "127.0.0.1:8080", origin: "://bad", want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := localRequest(http.MethodGet, "/healthz")
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := do(t, s, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusForbidden {
				if !strings.Contains(rec.Body.String(), "localhost only") {
					t.Fatalf("body = %s", rec.Body.String())
				}
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	rec := do(t, s, localRequest(http.MethodGet, "/healthz"))
	h := rec.Header()
	for _, key := range []string{
		"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options",
		"Referrer-Policy", "Permissions-Policy",
	} {
		if h.Get(key) == "" {
			t.Fatalf("missing header %s", key)
		}
	}
	if h.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("X-Frame-Options = %q", h.Get("X-Frame-Options"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", h.Get("X-Content-Type-Options"))
	}
}

func TestRequestID(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})

	req := localRequest(http.MethodGet, "/healthz")
	req.Header.Set("X-Request-ID", "client-provided-123")
	rec := do(t, s, req)
	if got := rec.Header().Get("X-Request-ID"); got != "client-provided-123" {
		t.Fatalf("request id = %q", got)
	}

	req = localRequest(http.MethodGet, "/healthz")
	req.Header.Set("X-Request-ID", "bad id")
	rec = do(t, s, req)
	got := rec.Header().Get("X-Request-ID")
	if got == "bad id" || len(got) != 24 {
		t.Fatalf("invalid id not replaced: %q", got)
	}
}

func TestStaticAssetsAndFavicon(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	rec := do(t, s, localRequest(http.MethodGet, "/static/demo.js"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("removed demo asset status = %d, want 404", rec.Code)
	}

	rec = do(t, s, localRequest(http.MethodGet, "/static/app.css"))
	if rec.Code != http.StatusOK {
		t.Fatalf("css status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "--accent") {
		t.Fatal("css body unexpected")
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("static asset Cache-Control = %q, want no-cache", rec.Header().Get("Cache-Control"))
	}

	rec = do(t, s, localRequest(http.MethodGet, "/favicon.ico"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("favicon status = %d", rec.Code)
	}
}

func TestRecoverPanic(t *testing.T) {
	s := &Server{log: testLogger(), now: time.Now}
	h := s.recoverPanic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, localRequest(http.MethodGet, "/boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRecoverPanicRepanicsAbort(t *testing.T) {
	s := &Server{log: testLogger()}
	h := s.recoverPanic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		rec := recover()
		recoveredErr, ok := rec.(error)
		if !ok || !errors.Is(recoveredErr, http.ErrAbortHandler) {
			t.Fatalf("recovered %v, want ErrAbortHandler", rec)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), localRequest(http.MethodGet, "/"))
}

func TestStatusRecorder(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	if _, err := sr.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	sr.WriteHeader(http.StatusTeapot) // second header ignored
	if sr.status != http.StatusOK {
		t.Fatalf("status = %d", sr.status)
	}
	if sr.bytes != 2 {
		t.Fatalf("bytes = %d", sr.bytes)
	}
	if sr.Unwrap() != rec {
		t.Fatal("Unwrap mismatch")
	}
}

func TestIsLocalHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":       true,
		"127.0.0.1:8080":  true,
		"localhost":       true,
		"localhost:1234":  true,
		"LOCALHOST:80":    true,
		"[::1]:8080":      true,
		"::1":             true,
		"0.0.0.0:8080":    false,
		"192.168.1.10:80": false,
		"evil.test:80":    false,
		"":                false,
	}
	for host, want := range cases {
		if got := isLocalHost(host); got != want {
			t.Errorf("isLocalHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestWriteJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := writeJSONError(rec, http.StatusBadRequest, "nope", "req-1"); err != nil {
		t.Fatal(err)
	}
	var got jsonError
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "nope" || got.RequestID != "req-1" {
		t.Fatalf("json error = %+v", got)
	}
}

func TestWriteJSONEncodeFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := writeJSON(rec, http.StatusOK, make(chan int)); err == nil {
		t.Fatal("expected encode error")
	}
}

func TestIndexUsesUTCWhenTimezoneInvalid(t *testing.T) {
	cfg := testConfig()
	cfg.App.Timezone = "Not/AZone"
	cfg.Market.Indices = nil
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})
	rec := do(t, s, localRequest(http.MethodGet, "/"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "12:00:00 IST") {
		t.Fatalf("body missing formatted time: %s", rec.Body.String())
	}
}
