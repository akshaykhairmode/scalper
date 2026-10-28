// Package auth tests Kotak authentication and local session management.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"log/slog"
)

type fakeKotak struct {
	view          ViewSession
	trade         TradeSession
	loginErr      error
	validateErr   error
	loginCalls    int
	validateCalls int
	gotView       ViewSession
	gotMPIN       string
}

func (f *fakeKotak) LoginTOTP(context.Context, string) (ViewSession, error) {
	f.loginCalls++
	return f.view, f.loginErr
}

func (f *fakeKotak) ValidateMPIN(_ context.Context, view ViewSession, mpin string) (TradeSession, error) {
	f.validateCalls++
	f.gotView = view
	f.gotMPIN = mpin
	return f.trade, f.validateErr
}

func TestKotakClientLoginFlow(t *testing.T) {
	const consumerKey = "consumer-key-secret"
	requests := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != consumerKey {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("neo-fin-key"); got != "neotradeapi" {
			t.Errorf("neo-fin-key = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		switch r.URL.Path {
		case "/login/1.0/tradeApiLogin":
			if r.Header.Get("Auth") != "" || r.Header.Get("sid") != "" {
				t.Error("initial login unexpectedly included a view session")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode TOTP body: %v", err)
			}
			if body["mobileNumber"] != "+911234567890" || body["ucc"] != "client-1" || body["totp"] != "123456" {
				t.Errorf("TOTP request body = %#v", body)
			}
			_, _ = fmt.Fprint(w, `{"data":{"token":"view-secret","sid":"view-sid","kType":"View","status":"success"}}`)
		case "/login/1.0/tradeApiValidate":
			if r.Header.Get("Auth") != "view-secret" || r.Header.Get("sid") != "view-sid" {
				t.Errorf("MPIN headers Auth=%q sid=%q", r.Header.Get("Auth"), r.Header.Get("sid"))
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode MPIN body: %v", err)
			}
			if body["mpin"] != "123456" || len(body) != 1 {
				t.Errorf("MPIN request body = %#v", body)
			}
			_, _ = fmt.Fprint(w, `{"data":{"token":"trade-secret","sid":"trade-sid","baseUrl":"https://cis.kotaksecurities.com","feedUrl":"wss://sfeed.kotaksecurities.com/apifeed","kType":"Trade","status":"success"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	var logOutput bytes.Buffer
	client := &kotakClient{
		log:         slog.New(slog.NewTextHandler(&logOutput, nil)),
		consumerKey: consumerKey,
		mobile:      "+911234567890",
		ucc:         "client-1",
		neoFinKey:   "neotradeapi",
		loginURL:    api.URL,
		http:        api.Client(),
	}
	view, err := client.LoginTOTP(context.Background(), "123456")
	if err != nil {
		t.Fatalf("LoginTOTP: %v", err)
	}
	if view != (ViewSession{Token: "view-secret", SID: "view-sid"}) {
		t.Fatalf("view session = %+v", view)
	}
	trade, err := client.ValidateMPIN(context.Background(), view, "123456")
	if err != nil {
		t.Fatalf("ValidateMPIN: %v", err)
	}
	if trade != (TradeSession{
		Token: "trade-secret", SID: "trade-sid",
		BaseURL: "https://cis.kotaksecurities.com",
		FeedURL: "wss://sfeed.kotaksecurities.com/apifeed",
	}) {
		t.Fatalf("trade session = %+v", trade)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	logText := logOutput.String()
	for _, secret := range []string{
		consumerKey, "neotradeapi", "+911234567890", "client-1", "123456",
		"view-secret", "view-sid", "trade-secret", "trade-sid",
	} {
		if strings.Contains(logText, secret) {
			t.Fatalf("API log leaked %q: %s", secret, logText)
		}
	}
	for _, want := range []string{
		"kotak_api_exchange",
		"tradeApiLogin",
		"tradeApiValidate",
		"request_body=",
		"response_body=",
		"status=200",
		"[REDACTED]",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("API log missing %q: %s", want, logText)
		}
	}
}

func TestResolveFeedURLFromDataCenterConfiguration(t *testing.T) {
	client := &kotakClient{
		http: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host != "lapi.kotaksecurities.com" || req.URL.Path != "/5config/config" {
				t.Errorf("configuration URL = %s", req.URL)
			}
			query := req.URL.Query()
			if query.Get("appVersion") != sfeedSDKVersion ||
				query.Get("platform") != "api" || query.Get("environment") != "prod" {
				t.Errorf("configuration query = %v", query)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(
					`{"data":{"configs":{"dc1_broadcast_source":"source1","dc1_source1_broadcast_endpoint":"wss://sfeed.kotaksecurities.com/apifeed"}}}`,
				)),
				Header: make(http.Header),
			}, nil
		})},
	}
	got, err := client.resolveFeedURL(context.Background(), "dc1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://sfeed.kotaksecurities.com/apifeed" {
		t.Fatalf("resolved feed URL = %q", got)
	}
}

func TestValidateMPINFallsBackToSessionFeedURLWhenDataCenterURLIsInvalid(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/1.0/tradeApiValidate":
			_, _ = fmt.Fprint(w, `{"data":{"token":"trade-secret","sid":"trade-sid","baseUrl":"https://cis.kotaksecurities.com","dataCenter":"E43","feedUrl":"wss://sfeed.kotaksecurities.com/apifeed","status":"success"}}`)
		case "/5config/config":
			_, _ = fmt.Fprint(w, `{"data":{"configs":{"E43_broadcast_source":"source","E43_source_broadcast_endpoint":"https://example.com/feed"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	client := &kotakClient{
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		consumerKey: "consumer-key",
		loginURL:    api.URL,
		http:        api.Client(),
	}
	trade, err := client.ValidateMPIN(context.Background(), ViewSession{
		Token: "view-token",
		SID:   "view-sid",
	}, "123456")
	if err != nil {
		t.Fatal(err)
	}
	if trade.FeedURL != "wss://sfeed.kotaksecurities.com/apifeed" {
		t.Fatalf("feed URL = %q; want valid MPIN response URL", trade.FeedURL)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestKotakClientRejectsBadResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "API rejection", body: `{"status":"error","message":"secret response"}`, want: "HTTP 401"},
		{name: "missing token", body: `{"data":{"sid":"view-sid","status":"success"}}`, want: "missing its view session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer api.Close()
			client := &kotakClient{
				consumerKey: "consumer-key",
				loginURL:    api.URL,
				http:        api.Client(),
			}
			_, err := client.LoginTOTP(context.Background(), "123456")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret response") {
				t.Fatal("API response body leaked through error")
			}
		})
	}
}

func TestKotakClientRequiresConsumerKey(t *testing.T) {
	client := NewKotakAuthenticator("", "mobile", "ucc", "neotradeapi")
	_, err := client.LoginTOTP(context.Background(), "123456")
	if !errors.Is(err, ErrMissingConsumerKey) {
		t.Fatalf("error = %v, want ErrMissingConsumerKey", err)
	}
}

func TestValidateBaseURL(t *testing.T) {
	if _, err := validateBaseURL("http://untrusted.example"); err == nil {
		t.Fatal("accepted non-HTTPS base URL")
	}
	if _, err := validateBaseURL("https://user:pass@example.com"); err == nil {
		t.Fatal("accepted base URL with user info")
	}
	if _, err := validateBaseURL("https://cis.kotaksecurities.com.attacker.example"); err == nil {
		t.Fatal("accepted base URL outside Kotak domain")
	}
	if got, err := validateBaseURL("https://cis.kotaksecurities.com/"); err != nil || got != "https://cis.kotaksecurities.com" {
		t.Fatalf("validateBaseURL = %q, %v", got, err)
	}
}

func TestSessionManagerNewSession(t *testing.T) {
	sm := NewSessionManager(24 * time.Hour)
	token := "test-token-" + fmt.Sprintf("%08d", time.Now().Unix()%100000000)
	session := sm.NewSession(token, LoginMethodTOTP, false)
	if session == nil || session.AccessToken != token || session.LoginMethod != LoginMethodTOTP {
		t.Fatalf("NewSession = %+v", session)
	}
	if session.PaperMode {
		t.Fatal("live session marked as paper")
	}
}

func TestSessionManagerGetAndRevoke(t *testing.T) {
	sm := NewSessionManager(24 * time.Hour)
	session := sm.NewSession("test-session", LoginMethodMPIN, true)
	if got := sm.GetSession("test-session"); got != session {
		t.Fatal("GetSession did not return created session")
	}
	if !sm.IsSessionValid("test-session") {
		t.Fatal("session should be valid")
	}
	sm.RevokeSession("test-session")
	if sm.IsSessionValid("test-session") {
		t.Fatal("session should be invalid after revocation")
	}
}

func TestSessionManagerCleanupExpired(t *testing.T) {
	sm := NewSessionManager(24 * time.Hour)
	sm.NewSession("expired", LoginMethodMPIN, false).TokenExpiry = time.Now().Add(-time.Minute)
	sm.NewSession("active", LoginMethodMPIN, false)
	sm.CleanupExpired()
	if sm.IsSessionValid("expired") || !sm.IsSessionValid("active") {
		t.Fatal("CleanupExpired did not remove only the expired session")
	}
}

func TestServerLoginFlowKeepsBrokerCredentialsServerSide(t *testing.T) {
	client := &fakeKotak{
		view:  ViewSession{Token: "view-secret", SID: "view-sid"},
		trade: TradeSession{Token: "trade-secret", SID: "trade-sid", BaseURL: "https://cis.kotaksecurities.com"},
	}
	server := NewServerWithAuthenticator(slog.Default(), client)
	if err := server.BeginLogin(context.Background(), "flow-1", "123456"); err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := server.CompleteLogin(context.Background(), "flow-1", "654321")
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if client.gotView.Token != "view-secret" || client.gotMPIN != "654321" {
		t.Fatalf("validation inputs were not forwarded: view=%+v mpin=%q", client.gotView, client.gotMPIN)
	}
	if session.BrokerToken != "trade-secret" || session.BrokerSID != "trade-sid" || session.BaseURL != "https://cis.kotaksecurities.com" {
		t.Fatalf("broker session not retained: %+v", session)
	}
	status := server.SessionStatus()
	if !status.Authenticated || status.PaperMode || status.LoginMethod != string(LoginMethodMPIN) {
		t.Fatalf("SessionStatus = %+v", status)
	}
	data, err := json.Marshal(ToSessionData(session))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"trade-secret", "trade-sid", "cis.kotaksecurities.com", session.AccessToken} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("session data leaked %q: %s", secret, data)
		}
	}
}

func TestServerLoginFlowValidatesAndExpiresPendingLogin(t *testing.T) {
	client := &fakeKotak{view: ViewSession{Token: "view", SID: "sid"}}
	server := NewServerWithAuthenticator(slog.Default(), client)
	if err := server.BeginLogin(context.Background(), "flow", "12345x"); err == nil {
		t.Fatal("accepted malformed TOTP")
	}
	if client.loginCalls != 0 {
		t.Fatal("sent invalid TOTP to Kotak")
	}
	if err := server.BeginLogin(context.Background(), "flow", "123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.CompleteLogin(context.Background(), "flow", "1234"); err == nil {
		t.Fatal("accepted four-digit MPIN")
	}
	if client.validateCalls != 0 {
		t.Fatal("sent invalid MPIN to Kotak")
	}
	server.pendingMu.Lock()
	pending := server.pendingLogins["flow"]
	pending.expiresAt = time.Now().Add(-time.Second)
	server.pendingLogins["flow"] = pending
	server.pendingMu.Unlock()
	if _, err := server.CompleteLogin(context.Background(), "flow", "123456"); !errors.Is(err, ErrNoPendingLogin) {
		t.Fatalf("expired login error = %v", err)
	}
}

func TestServerSessionStatusWithoutLogin(t *testing.T) {
	server := NewServerWithAuthenticator(slog.Default(), &fakeKotak{})
	status := server.SessionStatus()
	if status.Authenticated || status.PaperMode {
		t.Fatalf("unauthenticated status = %+v", status)
	}
}
