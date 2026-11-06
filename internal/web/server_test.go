package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/andybalholm/brotli"

	"github.com/akshaykhairmode/scalper/internal/auth"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/orderfeed"
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
	consumerKey string
	kotak       *kotakapi.Client
	now         func() time.Time
	started     time.Time
	cfg         *config.Config
	authClient  auth.KotakAuthenticator
	marketAPI   MarketAPI
	noTokenFunc bool
	configPath  string
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
		ConfigPath:  o.configPath,
		ConsumerKey: o.consumerKey,
		Kotak:       o.kotak,
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
	_, _ = s.a.SessionManager().NewBrokerSession(auth.TradeSession{Token: "tok", SID: "sid"}, auth.LoginMethodTOTP)

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

func TestCandlesBatchRoute(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}, marketAPI: fakeMarketAPI{}})
	url := "/api/candles/batch?spot=nse_cm%7CNifty%2050&ce=nse_fo%7CNIFTY26OCT25000CE&pe=nse_fo%7CNIFTY26OCT25000PE&tf=5m"
	rec := do(t, s, localRequest(http.MethodGet, url))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Spot    candleFetchResult            `json:"spot"`
		CE      candleFetchResult            `json:"ce"`
		PE      candleFetchResult            `json:"pe"`
		Results map[string]candleFetchResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Spot.Candles) != 1 || response.Spot.Candles[0].Close != 101 {
		t.Fatalf("spot candles = %+v", response.Spot.Candles)
	}
	if len(response.CE.Candles) != 1 || response.CE.Candles[0].Close != 101 {
		t.Fatalf("ce candles = %+v", response.CE.Candles)
	}
	if len(response.PE.Candles) != 1 || response.PE.Candles[0].Close != 101 {
		t.Fatalf("pe candles = %+v", response.PE.Candles)
	}
	if len(response.Results) != 3 {
		t.Fatalf("results count = %d, want 3", len(response.Results))
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
	for _, want := range []string{"Scalper", "NIFTY", "BANKNIFTY", "SENSEX", "present", "ready", "manifest.webmanifest", "theme-color", "apple-mobile-web-app-capable", "mobile-nav", "install-banner"} {
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
	if !strings.Contains(statusRec.Body.String(), `"authenticated":true`) {
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
	mpinReq.Header.Set("Content-Type", "application/json")
	mpinReq.Body = io.NopCloser(strings.NewReader(`{"mpin":"123456"}`))
	mpinRec := do(t, s, mpinReq)
	if mpinRec.Code != http.StatusConflict {
		t.Fatalf("MPIN without TOTP flow status = %d, want %d", mpinRec.Code, http.StatusConflict)
	}

	totpReq := localRequest(http.MethodPost, "/api/auth/totp")
	totpReq.Header.Set("Content-Type", "application/json")
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

type fakeMarketWithScrip struct {
	fakeMarketAPI
	status scrip.Status
}

func (f fakeMarketWithScrip) ScripStatus() scrip.Status {
	return f.status
}

func TestHealthComprehensiveDetails(t *testing.T) {
	mockMarket := fakeMarketWithScrip{
		status: scrip.Status{
			Loaded:  true,
			Source:  "database",
			Options: 12000,
			Spots:   500,
		},
	}
	s := newTestServer(t, serverOpts{
		db:        fakeDB{},
		token:     true,
		marketAPI: mockMarket,
	})
	s.orderFeed = orderfeed.New(testLogger())
	s.hsm = market.NewHSMFeed(testConfig(), nil, testLogger())
	defer s.Close()

	rec := do(t, s, localRequest(http.MethodGet, "/api/health"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	// 1. Database
	db := resp.Checks["database"]
	if db.Status != "ok" || !strings.Contains(db.Detail, "connected") {
		t.Fatalf("database check = %+v", db)
	}

	// 2. Logging
	log := resp.Checks["logging"]
	if log.Status != "ok" {
		t.Fatalf("logging check = %+v", log)
	}

	// 3. Session / Broker
	sess := resp.Checks["session"]
	if sess.Status != "ok" || !sess.TokenPresent {
		t.Fatalf("session check = %+v", sess)
	}
	broker := resp.Checks["broker"]
	if broker.Status != "ok" {
		t.Fatalf("broker check = %+v", broker)
	}

	// 4. Market Feed
	mf := resp.Checks["market_feed"]
	if mf.Status != "stopped" || mf.Endpoint == "" {
		t.Fatalf("market_feed check = %+v", mf)
	}

	// 5. Order Feed
	of := resp.Checks["order_feed"]
	if of.Status != "disconnected" {
		t.Fatalf("order_feed check = %+v", of)
	}

	// 6. Scrip Master
	sm := resp.Checks["scrip_master"]
	if sm.Status != "ok" || !sm.Loaded || sm.Count != 12500 || sm.Source != "database" {
		t.Fatalf("scrip_master check = %+v", sm)
	}
}

func TestOriginProtection(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	cases := []struct {
		name         string
		host         string
		origin       string
		secFetchSite string
		want         int
	}{
		{name: "ipv4 same origin", host: "127.0.0.1:8080", origin: "http://127.0.0.1:8080", want: http.StatusOK},
		{name: "no origin header", host: "127.0.0.1:8080", want: http.StatusOK},
		{name: "remote origin mismatch", host: "127.0.0.1:8080", origin: "http://evil.test", want: http.StatusForbidden},
		{name: "cross-site fetch", host: "127.0.0.1:8080", secFetchSite: "cross-site", want: http.StatusForbidden},
		{name: "invalid origin", host: "127.0.0.1:8080", origin: "://bad", want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := localRequest(http.MethodGet, "/healthz")
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.secFetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.secFetchSite)
			}
			rec := do(t, s, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
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

	// Test Brotli compressed static asset serving
	reqBr := localRequest(http.MethodGet, "/static/app.css")
	reqBr.Header.Set("Accept-Encoding", "gzip, deflate, br")
	recBr := do(t, s, reqBr)
	if recBr.Code != http.StatusOK {
		t.Fatalf("css br status = %d, want 200", recBr.Code)
	}
	if recBr.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("Content-Encoding = %q, want 'br'", recBr.Header().Get("Content-Encoding"))
	}
	if recBr.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want 'Accept-Encoding'", recBr.Header().Get("Vary"))
	}
	etag := recBr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag on br response")
	}

	// Test If-None-Match 304 response
	req304 := localRequest(http.MethodGet, "/static/app.css")
	req304.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req304.Header.Set("If-None-Match", etag)
	rec304 := do(t, s, req304)
	if rec304.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 Not Modified", rec304.Code)
	}

	// Verify decompressed Brotli content contains css token
	brReader := brotli.NewReader(recBr.Body)
	decompressed, err := io.ReadAll(brReader)
	if err != nil {
		t.Fatalf("decompress br response: %v", err)
	}
	if !strings.Contains(string(decompressed), "--accent") {
		t.Fatal("decompressed css body missing --accent")
	}
}

func TestPWARoutesAndAssets(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})

	// 1. /sw.js
	recSW := do(t, s, localRequest(http.MethodGet, "/sw.js"))
	if recSW.Code != http.StatusOK {
		t.Fatalf("/sw.js status = %d, want 200", recSW.Code)
	}
	if ct := recSW.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("/sw.js Content-Type = %q, want application/javascript", ct)
	}
	if allowed := recSW.Header().Get("Service-Worker-Allowed"); allowed != "/" {
		t.Fatalf("/sw.js Service-Worker-Allowed = %q, want '/'", allowed)
	}
	if !strings.Contains(recSW.Body.String(), "scalper") {
		t.Fatal("/sw.js content missing 'scalper'")
	}

	// 2. /manifest.webmanifest
	recMan := do(t, s, localRequest(http.MethodGet, "/manifest.webmanifest"))
	if recMan.Code != http.StatusOK {
		t.Fatalf("/manifest.webmanifest status = %d, want 200", recMan.Code)
	}
	if ct := recMan.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Fatalf("/manifest.webmanifest Content-Type = %q, want application/manifest+json", ct)
	}
	if !strings.Contains(recMan.Body.String(), `"standalone"`) {
		t.Fatal("/manifest.webmanifest missing standalone display")
	}

	// 3. /manifest.json
	recJSON := do(t, s, localRequest(http.MethodGet, "/manifest.json"))
	if recJSON.Code != http.StatusOK {
		t.Fatalf("/manifest.json status = %d, want 200", recJSON.Code)
	}
	if ct := recJSON.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Fatalf("/manifest.json Content-Type = %q, want application/manifest+json", ct)
	}

	// 4. Static icons
	recIconSVG := do(t, s, localRequest(http.MethodGet, "/static/icon.svg"))
	if recIconSVG.Code != http.StatusOK {
		t.Fatalf("/static/icon.svg status = %d, want 200", recIconSVG.Code)
	}

	recIcon192 := do(t, s, localRequest(http.MethodGet, "/static/icon-192.png"))
	if recIcon192.Code != http.StatusOK {
		t.Fatalf("/static/icon-192.png status = %d, want 200", recIcon192.Code)
	}
	if ct := recIcon192.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("/static/icon-192.png Content-Type = %q, want image/png", ct)
	}

	for _, icon := range []string{"icon-512.png", "icon-maskable-192.png", "icon-maskable-512.png", "apple-touch-icon.png"} {
		rec := do(t, s, localRequest(http.MethodGet, "/static/"+icon))
		if rec.Code != http.StatusOK {
			t.Fatalf("/static/%s status = %d, want 200", icon, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
			t.Fatalf("/static/%s Content-Type = %q, want image/png", icon, ct)
		}
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

func TestIsMalformedCandleRun(t *testing.T) {
	clean := []market.Candle{
		{Time: 1, Open: 100, High: 105, Low: 99, Close: 102},
		{Time: 2, Open: 102, High: 106, Low: 101, Close: 105},
		{Time: 3, Open: 105, High: 107, Low: 103, Close: 104},
		{Time: 4, Open: 104, High: 108, Low: 102, Close: 107},
		{Time: 5, Open: 107, High: 109, Low: 106, Close: 108},
	}
	if isMalformedCandleRun(clean) {
		t.Error("clean candles flagged as malformed")
	}

	malformed := []market.Candle{
		{Time: 1, Open: 22680, High: 22960, Low: 22650, Close: 22930},
		{Time: 2, Open: 22710, High: 22960, Low: 22670, Close: 22930},
		{Time: 3, Open: 22720, High: 22960, Low: 22700, Close: 22930},
		{Time: 4, Open: 22730, High: 22960, Low: 22710, Close: 22930},
		{Time: 5, Open: 22740, High: 22960, Low: 22720, Close: 22930},
	}
	if !isMalformedCandleRun(malformed) {
		t.Error("malformed run was not detected")
	}
}

func TestBasicAuthMiddleware(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "supersecret"

	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})

	// 1. Unauthenticated request should return 401 Unauthorized
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("WWW-Authenticate header must not be set to prevent browser native basic auth popup: %q", rec.Header().Get("WWW-Authenticate"))
	}

	// 2. Request with invalid credentials should return 401 Unauthorized
	reqWrong := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	reqWrong.SetBasicAuth("admin", "wrongpassword")
	recWrong := httptest.NewRecorder()
	s.Handler().ServeHTTP(recWrong, reqWrong)
	if recWrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong credentials status = %d, want %d", recWrong.Code, http.StatusUnauthorized)
	}

	// 3. Request with valid credentials should succeed (200 OK)
	reqValid := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	reqValid.SetBasicAuth("admin", "supersecret")
	recValid := httptest.NewRecorder()
	s.Handler().ServeHTTP(recValid, reqValid)
	if recValid.Code != http.StatusOK {
		t.Fatalf("valid credentials status = %d, want %d", recValid.Code, http.StatusOK)
	}
}

func TestServeUnlockEndpoint(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "testuser"
	cfg.Web.BasicAuth.Password = "testpass123"

	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})

	// Invalid password
	bodyWrong := strings.NewReader(`{"username":"testuser","password":"wrong"}`)
	reqWrong := httptest.NewRequest(http.MethodPost, "/api/auth/serve-unlock", bodyWrong)
	reqWrong.Header.Set("Content-Type", "application/json")
	reqWrong.SetBasicAuth("testuser", "testpass123")
	recWrong := httptest.NewRecorder()
	s.Handler().ServeHTTP(recWrong, reqWrong)
	if recWrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong pass status = %d, want %d", recWrong.Code, http.StatusUnauthorized)
	}

	// Valid credentials
	bodyOK := strings.NewReader(`{"username":"testuser","password":"testpass123"}`)
	reqOK := httptest.NewRequest(http.MethodPost, "/api/auth/serve-unlock", bodyOK)
	reqOK.Header.Set("Content-Type", "application/json")
	reqOK.SetBasicAuth("testuser", "testpass123")
	recOK := httptest.NewRecorder()
	s.Handler().ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK {
		t.Fatalf("valid unlock status = %d, want %d", recOK.Code, http.StatusOK)
	}

	cookie := recOK.Result().Cookies()
	var found bool
	var sessionToken string
	for _, c := range cookie {
		if c.Name == "scalper_serve_session" {
			found = true
			sessionToken = c.Value
			break
		}
	}
	if !found || sessionToken == "" {
		t.Fatalf("expected scalper_serve_session cookie to be set")
	}

	// Verify that subsequent request to /healthz with the session cookie succeeds without BasicAuth header
	reqCookie := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	reqCookie.AddCookie(&http.Cookie{Name: "scalper_serve_session", Value: sessionToken})
	recCookie := httptest.NewRecorder()
	s.Handler().ServeHTTP(recCookie, reqCookie)
	if recCookie.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with session cookie, got %d", recCookie.Code)
	}
}

func TestServeUnlockRateLimit(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "testuser"
	cfg.Web.BasicAuth.Password = "testpass123"
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})
	defer s.Close()

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/serve-unlock",
			strings.NewReader(`{"username":"testuser","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i+1, rec.Code)
		}
	}

	valid := httptest.NewRequest(http.MethodPost, "/api/auth/serve-unlock",
		strings.NewReader(`{"username":"testuser","password":"testpass123"}`))
	valid.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, valid)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out attempt status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on 429")
	}
}

func TestServeLogoutRevokesSession(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "testuser"
	cfg.Web.BasicAuth.Password = "testpass123"
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})
	defer s.Close()

	unlock := httptest.NewRequest(http.MethodPost, "/api/auth/serve-unlock",
		strings.NewReader(`{"username":"testuser","password":"testpass123"}`))
	unlock.Header.Set("Content-Type", "application/json")
	unlockRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(unlockRec, unlock)
	if unlockRec.Code != http.StatusOK {
		t.Fatalf("unlock status = %d: %s", unlockRec.Code, unlockRec.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, cookie := range unlockRec.Result().Cookies() {
		if cookie.Name == serveSessionCookie {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("unlock did not set a serve session cookie")
	}

	authed := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	authed.AddCookie(sessionCookie)
	authedRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(authedRec, authed)
	if authedRec.Code != http.StatusOK {
		t.Fatalf("session request status = %d", authedRec.Code)
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logout.AddCookie(sessionCookie)
	logoutRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(logoutRec, logout)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d", logoutRec.Code)
	}

	reused := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	reused.AddCookie(sessionCookie)
	reusedRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(reusedRec, reused)
	if reusedRec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session request status = %d, want 401", reusedRec.Code)
	}
}

func TestServeOriginAndJSONContentTypeProtection(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "strongpass"
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})
	defer s.Close()

	cross := httptest.NewRequest(http.MethodPost, "/api/auth/serve-unlock",
		strings.NewReader(`{"username":"admin","password":"strongpass"}`))
	cross.Header.Set("Content-Type", "application/json")
	cross.Header.Set("Origin", "https://evil.example")
	crossRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(crossRec, cross)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", crossRec.Code)
	}

	noType := httptest.NewRequest(http.MethodPost, "/api/auth/serve-unlock",
		strings.NewReader(`{"username":"admin","password":"strongpass"}`))
	noTypeRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(noTypeRec, noType)
	if noTypeRec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("missing content-type status = %d, want 415", noTypeRec.Code)
	}
}

func TestAuthFlowCookieSecureBehindTLSProxy(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp", strings.NewReader(`{"totp":"123456"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	_, err := authFlowID(rec, req, true)
	if err != nil {
		t.Fatalf("authFlowID: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authFlowCookieName {
		t.Fatalf("cookies = %+v", cookies)
	}
	if !cookies[0].Secure {
		t.Fatal("auth flow cookie must be Secure behind an HTTPS proxy")
	}
}

func TestServeModeIndexFooter(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "serve mode") {
		t.Fatalf("expected 'serve mode' in footer, got: %s", body)
	}
}

func TestLogsEndpoints(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "scalper.log")
	if err := os.WriteFile(logFile, []byte("line 1\nline 2: WARN something\nline 3: ERROR test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.Logging.File = logFile
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg})

	// Test GET /logs (HTML view)
	reqView := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/logs", nil)
	reqView.Host = "127.0.0.1:8080"
	recView := httptest.NewRecorder()
	s.Handler().ServeHTTP(recView, reqView)
	if recView.Code != http.StatusOK {
		t.Fatalf("GET /logs status = %d, want 200", recView.Code)
	}
	if !strings.Contains(recView.Body.String(), "Scalper Logs") {
		t.Fatalf("expected 'Scalper Logs' in /logs HTML")
	}

	// Test GET /api/logs (JSON API)
	reqAPI := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/logs?lines=10", nil)
	reqAPI.Host = "127.0.0.1:8080"
	recAPI := httptest.NewRecorder()
	s.Handler().ServeHTTP(recAPI, reqAPI)
	if recAPI.Code != http.StatusOK {
		t.Fatalf("GET /api/logs status = %d, want 200", recAPI.Code)
	}
	var data struct {
		Ok    bool     `json:"ok"`
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(recAPI.Body.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal logs json: %v", err)
	}
	if !data.Ok || len(data.Lines) != 3 {
		t.Fatalf("unexpected logs response: %+v", data)
	}

	// Test GET /api/logs?download=1 (download raw file)
	reqDL := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/logs?download=1", nil)
	reqDL.Host = "127.0.0.1:8080"
	recDL := httptest.NewRecorder()
	s.Handler().ServeHTTP(recDL, reqDL)
	if recDL.Code != http.StatusOK {
		t.Fatalf("GET /api/logs?download=1 status = %d, want 200", recDL.Code)
	}
	if !strings.Contains(recDL.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("expected Content-Disposition attachment header, got: %s", recDL.Header().Get("Content-Disposition"))
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestBrokerSquareOffAll(t *testing.T) {
	t.Run("unauthorized without session", func(t *testing.T) {
		client := kotakapi.NewClient(nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("should not be called")
		}))
		s := newTestServer(t, serverOpts{db: fakeDB{}, kotak: client, consumerKey: "ck-123"})
		defer s.Close()

		req := localRequest(http.MethodPost, "/api/broker/square-off-all")
		rec := do(t, s, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("service unavailable without kotak client", func(t *testing.T) {
		s := newTestServer(t, serverOpts{db: fakeDB{}})
		defer s.Close()

		req := localRequest(http.MethodPost, "/api/broker/square-off-all")
		rec := do(t, s, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 Service Unavailable, got %d", rec.Code)
		}
	})

	t.Run("successful multi-position exit", func(t *testing.T) {
		var placedOrders []kotakapi.Order
		var orderMu sync.Mutex

		transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			switch {
			case strings.Contains(req.URL.Path, "portfolio/v2/positions"):
				recorder.WriteHeader(http.StatusOK)
				_, _ = recorder.Write([]byte(`{
					"stat": "Ok",
					"data": [
						{
							"exchange_segment": "nse_fo",
							"trading_symbol": "NIFTY26NOV25000CE",
							"quantity": 100,
							"product": "MIS"
						},
						{
							"exchange_segment": "nse_fo",
							"trading_symbol": "BANKNIFTY26NOV52000PE",
							"quantity": -50,
							"product": "NRML"
						},
						{
							"exchange_segment": "nse_fo",
							"trading_symbol": "FINNIFTY26NOV24000CE",
							"quantity": 0,
							"product": "MIS"
						}
					]
				}`))
			case strings.Contains(req.URL.Path, "quick/order/rule/ms/place"):
				bodyBytes, _ := io.ReadAll(req.Body)
				values, err := url.ParseQuery(string(bodyBytes))
				if err == nil {
					jData := values.Get("jData")
					var form map[string]any
					if err := json.Unmarshal([]byte(jData), &form); err == nil {
						qty, _ := strconv.ParseInt(fmt.Sprint(form["qt"]), 10, 64)
						orderMu.Lock()
						placedOrders = append(placedOrders, kotakapi.Order{
							ExchangeSegment: fmt.Sprint(form["es"]),
							TradingSymbol:   fmt.Sprint(form["ts"]),
							TransactionType: fmt.Sprint(form["tt"]),
							Quantity:        qty,
							Product:         fmt.Sprint(form["pc"]),
							OrderType:       fmt.Sprint(form["pt"]),
						})
						orderMu.Unlock()
					}
				}
				recorder.WriteHeader(http.StatusOK)
				_, _ = recorder.Write([]byte(`{"stat":"Ok","nOrdNo":"123456"}`))
			default:
				recorder.WriteHeader(http.StatusOK)
				_, _ = recorder.Write([]byte(`{"stat":"Ok","data":[]}`))
			}
			return recorder.Result(), nil
		})

		client := kotakapi.NewClient(nil, transport)
		s := newTestServer(t, serverOpts{db: fakeDB{}, kotak: client, consumerKey: "test-consumer-key"})
		defer s.Close()

		_, err := s.a.SessionManager().NewBrokerSession(auth.TradeSession{
			Token:   "trade-token",
			SID:     "trade-sid",
			BaseURL: "https://cis.kotaksecurities.com",
		}, auth.LoginMethodMPIN)
		if err != nil {
			t.Fatalf("NewBrokerSession: %v", err)
		}

		req := localRequest(http.MethodPost, "/api/broker/square-off-all")
		rec := do(t, s, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Success         bool     `json:"success"`
			ClosedPositions int      `json:"closed_positions"`
			Errors          []string `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if !resp.Success {
			t.Fatalf("expected success true, got false")
		}
		if resp.ClosedPositions != 2 {
			t.Fatalf("expected closed_positions 2, got %d", resp.ClosedPositions)
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("expected 0 errors, got %v", resp.Errors)
		}

		orderMu.Lock()
		defer orderMu.Unlock()
		if len(placedOrders) != 2 {
			t.Fatalf("expected 2 placed orders, got %d", len(placedOrders))
		}

		ordersBySymbol := make(map[string]kotakapi.Order)
		for _, o := range placedOrders {
			ordersBySymbol[o.TradingSymbol] = o
		}

		longExit, ok := ordersBySymbol["NIFTY26NOV25000CE"]
		if !ok {
			t.Fatalf("missing exit order for NIFTY26NOV25000CE")
		}
		if longExit.TransactionType != "S" {
			t.Errorf("long position exit should be SELL ('S'), got %s", longExit.TransactionType)
		}
		if longExit.Quantity != 100 {
			t.Errorf("long position exit quantity should be 100, got %d", longExit.Quantity)
		}
		if longExit.Product != "MIS" {
			t.Errorf("long position exit product should be MIS, got %s", longExit.Product)
		}

		shortExit, ok := ordersBySymbol["BANKNIFTY26NOV52000PE"]
		if !ok {
			t.Fatalf("missing exit order for BANKNIFTY26NOV52000PE")
		}
		if shortExit.TransactionType != "B" {
			t.Errorf("short position exit should be BUY ('B'), got %s", shortExit.TransactionType)
		}
		if shortExit.Quantity != 50 {
			t.Errorf("short position exit quantity should be 50, got %d", shortExit.Quantity)
		}
		if shortExit.Product != "NRML" {
			t.Errorf("short position exit product should be NRML, got %s", shortExit.Product)
		}
	})
}
