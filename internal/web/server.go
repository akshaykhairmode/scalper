// Package web serves the localhost UI, health endpoint and static assets.
package web

import (
	"bufio"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/akshaykhairmode/scalper/internal/auth"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/orderfeed"
	"github.com/akshaykhairmode/scalper/internal/scrip"
	"github.com/akshaykhairmode/scalper/internal/version"
)

//go:embed templates/index.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// DBPinger is the subset of the store needed by health checks.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// CandleStore caches normalized historical bars in SQLite.
type CandleStore interface {
	ReadCandles(ctx context.Context, instrumentKey, timeframe string) ([]market.Candle, error)
	WriteCandles(ctx context.Context, instrumentKey, timeframe string, candles []market.Candle) error
	CandleSyncAt(ctx context.Context, instrumentKey, timeframe, sessionDate string) (int64, bool, error)
	MarkCandlesSynced(ctx context.Context, instrumentKey, timeframe, sessionDate string, syncedAt int64) error
}

// MarketAPI is the market data subset used by the web server.
type MarketAPI interface {
	Indices(context.Context, string) ([]market.IndexQuote, error)
	Expiries(string) ([]scrip.ExpiryInfo, error)
	Chain(context.Context, string, string, string) ([]market.ChainRow, error)
	Quote(context.Context, string, string) (market.Quote, error)
	Quotes(context.Context, string, []string) (map[string]market.Quote, error)
	Candles(context.Context, string, string, string) ([]market.Candle, error)
	Option(string) (scrip.Option, bool)
	FindOption(string, string) (scrip.Option, bool)
}

type chainWindowAPI interface {
	ChainWindow(context.Context, string, string, string, float64, int, string) (market.ChainPage, error)
}

// Options configures the HTTP server.
type Options struct {
	Logger       *slog.Logger
	Config       config.Config
	ConsumerKey  string
	MPIN         string
	AuthClient   auth.KotakAuthenticator
	Market       MarketAPI
	HSM          *market.HSMFeed
	Kotak        *kotakapi.Client
	OrderFeed    *orderfeed.Client
	DB           DBPinger
	CandleStore  CandleStore
	LogDegraded  func() bool
	TokenPresent func() bool
	Started      time.Time
	Now          func() time.Time
}

// Server holds the HTTP handler and its dependencies.
type Server struct {
	log          *slog.Logger
	cfg          config.Config
	mpin         string
	db           DBPinger
	candleStore  CandleStore
	logDegraded  func() bool
	tokenPresent func() bool
	started      time.Time
	now          func() time.Time
	tmpl         *template.Template
	handler      http.Handler
	bootstrap    template.JS

	// Auth is the authentication handler.
	a           *auth.Server
	market      MarketAPI
	hsm         *market.HSMFeed
	kotak       *kotakapi.Client
	orderFeed   *orderfeed.Client
	consumerKey string
	books       *booksCache

	feedMu      sync.RWMutex
	feedWriteMu sync.Mutex
	feedClients map[*websocket.Conn]struct{}
	feedCtx     context.Context
	feedCancel  context.CancelFunc

	indicesMu  sync.Mutex
	indicesRaw json.RawMessage
	indicesAt  time.Time
}

// bootstrapConfig is rendered into the page so the frontend can start without
// a round-trip. It contains no secrets: only display settings and the index list.
type bootstrapConfig struct {
	Indices           []bootstrapIndex     `json:"indices"`
	Commodities       []bootstrapCommodity `json:"commodities,omitempty"`
	Timeframes        []string             `json:"timeframes"`
	DefaultTimeframe  string               `json:"defaultTimeframe"`
	DefaultProduct    string               `json:"defaultProduct"`
	DefaultLots       int                  `json:"defaultLots"`
	DefaultUnit       string               `json:"defaultUnit"`
	AllowShares       bool                 `json:"allowShares"`
	ConfirmLiveOrders bool                 `json:"confirmLiveOrders"`
	StartingCash      float64              `json:"startingCash"`
	ShortMarginPct    float64              `json:"shortMarginPct"`
	PayoffRangePct    int                  `json:"payoffRangePct"`
	PayoffPoints      int                  `json:"payoffPoints"`
	Version           string               `json:"version"`
	MPINConfigured    bool                 `json:"mpinConfigured"`
	MarketSession     bootstrapSession     `json:"marketSession"`
}

type bootstrapSession struct {
	Open     string   `json:"open"`
	Close    string   `json:"close"`
	Holidays []string `json:"holidays"`
}

type bootstrapIndex struct {
	Key             string `json:"key"`
	DisplayName     string `json:"displayName"`
	ExchangeSegment string `json:"exchangeSegment"`
	IndexSegment    string `json:"indexSegment"`
	IndexSymbol     string `json:"indexSymbol"`
	ExpiryMode      string `json:"expiryMode"`
	DefaultExpiry   string `json:"defaultExpiry"`
}

type bootstrapCommodity struct {
	Key             string `json:"key"`
	DisplayName     string `json:"displayName"`
	ExchangeSegment string `json:"exchangeSegment"`
}

func newBootstrap(cfg config.Config, mpinConfigured bool) (template.JS, error) {
	boot := bootstrapConfig{
		Timeframes:        append([]string(nil), cfg.Charts.Timeframes...),
		DefaultTimeframe:  cfg.Charts.DefaultTimeframe,
		DefaultProduct:    cfg.Trading.DefaultProduct,
		DefaultLots:       cfg.Trading.Quantity.DefaultLots,
		DefaultUnit:       cfg.Trading.Quantity.DefaultUnit,
		AllowShares:       cfg.Trading.Quantity.AllowShares,
		ConfirmLiveOrders: cfg.Trading.ConfirmLiveOrders,
		StartingCash:      cfg.Trading.Paper.StartingCash,
		ShortMarginPct:    cfg.Trading.Paper.ShortMarginPct,
		PayoffRangePct:    cfg.Trading.RiskView.PayoffRangePct,
		PayoffPoints:      cfg.Trading.RiskView.PayoffPoints,
		Version:           version.Version,
		MPINConfigured:    mpinConfigured,
		MarketSession: bootstrapSession{
			Open:     cfg.Trading.Session.Open,
			Close:    cfg.Trading.Session.Close,
			Holidays: append([]string(nil), cfg.Market.Holidays...),
		},
	}
	for _, idx := range cfg.Market.Indices {
		boot.Indices = append(boot.Indices, bootstrapIndex{
			Key:             idx.Key,
			DisplayName:     idx.DisplayName,
			ExchangeSegment: idx.ExchangeSegment,
			IndexSegment:    idx.IndexSegment,
			IndexSymbol:     idx.IndexSymbol,
			ExpiryMode:      idx.ExpiryMode,
			DefaultExpiry:   idx.DefaultExpiry,
		})
	}
	for _, commodity := range cfg.Market.Commodities {
		boot.Commodities = append(boot.Commodities, bootstrapCommodity{
			Key: commodity.Key, DisplayName: commodity.DisplayName,
			ExchangeSegment: commodity.ExchangeSegment,
		})
	}
	data, err := json.Marshal(boot)
	if err != nil {
		return "", fmt.Errorf("web: encode bootstrap config: %w", err)
	}
	// JSON marshaling escapes script-sensitive characters before template.JS.
	return template.JS(data), nil // #nosec G203
}

// New builds the handler. It fails only if the embedded templates cannot be
// parsed, which is a build-time defect.
func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Started.IsZero() {
		opts.Started = opts.Now()
	}
	tmpl, err := template.ParseFS(templateFS, "templates/index.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	bootstrap, err := newBootstrap(opts.Config, strings.TrimSpace(opts.MPIN) != "")
	if err != nil {
		return nil, err
	}
	authServer := auth.NewServer(opts.Logger, opts.Config, opts.ConsumerKey)
	if opts.AuthClient != nil {
		authServer = auth.NewServerWithAuthenticator(opts.Logger, opts.AuthClient)
	}
	feedCtx, feedCancel := context.WithCancel(context.Background())
	s := &Server{
		log:          opts.Logger,
		cfg:          opts.Config,
		mpin:         strings.TrimSpace(opts.MPIN),
		db:           opts.DB,
		candleStore:  opts.CandleStore,
		logDegraded:  opts.LogDegraded,
		tokenPresent: opts.TokenPresent,
		started:      opts.Started,
		now:          opts.Now,
		tmpl:         tmpl,
		bootstrap:    bootstrap,
		a:            authServer,
		market:       opts.Market,
		hsm:          opts.HSM,
		kotak:        opts.Kotak,
		orderFeed:    opts.OrderFeed,
		consumerKey:  strings.TrimSpace(opts.ConsumerKey),
		books:        newBooksCache(),
		feedClients:  make(map[*websocket.Conn]struct{}),
		feedCtx:      feedCtx,
		feedCancel:   feedCancel,
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("web: static assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// Auth routes
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /api/auth/totp", s.handleTotpLogin)
	mux.HandleFunc("POST /api/auth/mpin", s.handleMPINLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/session", s.handleSessionStatus)
	mux.HandleFunc("GET /api/session/status", s.handleSessionStatus)
	mux.HandleFunc("GET /api/indices", s.handleIndices)
	mux.HandleFunc("GET /api/expiries", s.handleExpiries)
	mux.HandleFunc("GET /api/chain", s.handleChain)
	// Account books and order actions are available only through authenticated broker APIs.
	mux.HandleFunc("GET /api/broker/orders", s.handleBrokerOrders)
	mux.HandleFunc("GET /api/broker/orders/{id}", s.handleBrokerOrder)
	mux.HandleFunc("GET /api/broker/orders/{id}/history", s.handleBrokerOrderHistory)
	mux.HandleFunc("POST /api/broker/orders", s.handleBrokerPlaceOrder)
	mux.HandleFunc("PATCH /api/broker/orders/{id}", s.handleBrokerModifyOrder)
	mux.HandleFunc("DELETE /api/broker/orders/{id}", s.handleBrokerCancelOrder)
	mux.HandleFunc("GET /api/broker/trades", s.handleBrokerTrades)
	mux.HandleFunc("GET /api/broker/positions", s.handleBrokerPositions)
	mux.HandleFunc("GET /api/broker/holdings", s.handleBrokerHoldings)
	mux.HandleFunc("GET /api/broker/limits", s.handleBrokerLimits)
	mux.HandleFunc("GET /api/candles", s.handleCandles)
	mux.HandleFunc("GET /ws", s.handleWebSocket)

	var h http.Handler = mux
	h = localOnly(s.log, h)
	h = securityHeaders(h)
	h = s.accessLog(h)
	h = requestID(h)
	h = s.recoverPanic(h)
	s.handler = h
	if s.hsm != nil {
		go s.forwardHSMEvents()
	}
	if s.orderFeed != nil {
		go s.forwardOrderFeedEvents()
	}
	if s.kotak != nil {
		go s.runAccountBooks(feedCtx)
		go s.logKotakRate(feedCtx)
	}
	return s, nil
}

// Handler returns the complete middleware chain.
func (s *Server) Handler() http.Handler { return s.handler }

// Close stops the server's feed event forwarding and WebSocket clients.
func (s *Server) Close() {
	if s.orderFeed != nil {
		s.orderFeed.Stop()
	}
	if s.feedCancel != nil {
		s.feedCancel()
	}
	s.feedMu.Lock()
	for client := range s.feedClients {
		_ = client.Close()
		delete(s.feedClients, client)
	}
	s.feedMu.Unlock()
}

type pageData struct {
	Title          string
	Mode           string
	Version        string
	Commit         string
	Underlyings    []pageUnderlying
	Uptime         string
	Now            string
	TokenPresent   bool
	LogDegraded    bool
	DBOK           bool
	MPINConfigured bool
	Bootstrap      template.JS
}

type pageUnderlying struct {
	Key   string
	Label string
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	checks := s.healthChecks(r.Context())
	dbOK := checks["database"].Status == "ok"
	token := false
	if s.tokenPresent != nil {
		token = s.tokenPresent()
	}
	underlyings := make([]pageUnderlying, 0, len(s.cfg.Market.Indices)+len(s.cfg.Market.Commodities))
	for _, idx := range s.cfg.Market.Indices {
		underlyings = append(underlyings, pageUnderlying{
			Key: idx.Key, Label: underlyingLabel(idx.DisplayName, idx.ExchangeSegment),
		})
	}
	for _, commodity := range s.cfg.Market.Commodities {
		underlyings = append(underlyings, pageUnderlying{
			Key: commodity.Key, Label: underlyingLabel(commodity.DisplayName, commodity.ExchangeSegment),
		})
	}
	data := pageData{
		Title:          "Scalper",
		Mode:           "LIVE",
		Version:        version.Version,
		Commit:         version.Commit,
		Underlyings:    underlyings,
		Uptime:         s.now().Sub(s.started).Round(time.Second).String(),
		Now:            s.now().In(s.cfg.Location()).Format("15:04:05 IST"),
		TokenPresent:   token,
		LogDegraded:    s.loggingDegraded(),
		DBOK:           dbOK,
		MPINConfigured: s.mpin != "",
		Bootstrap:      s.bootstrap,
	}
	var buf strings.Builder
	if err := s.tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		s.log.Error("render index", "event", "http_render_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		if werr := writeJSONError(w, http.StatusInternalServerError, "render failed", logging.RequestID(r.Context())); werr != nil {
			s.log.Warn("write render error response", "event", "http_write_failed", "error", werr,
				"request_id", logging.RequestID(r.Context()))
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, buf.String()); err != nil {
		s.log.Warn("write index response", "event", "http_write_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
	}
}

func underlyingLabel(name, segment string) string {
	exchange := ""
	switch segment {
	case "nse_fo", "nse_cm":
		exchange = "NSE"
	case "bse_fo", "bse_cm":
		exchange = "BSE"
	case "mcx_fo":
		exchange = "MCX"
	default:
		exchange = strings.ToUpper(strings.TrimSuffix(segment, "_fo"))
	}
	if strings.TrimSpace(name) == "" {
		name = segment
	}
	if exchange == "" {
		return name
	}
	return name + " · " + exchange
}

type healthCheck struct {
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

type healthResponse struct {
	Status        string                 `json:"status"`
	Time          string                 `json:"time"`
	UptimeSeconds int64                  `json:"uptime_seconds"`
	Mode          string                 `json:"mode"`
	Version       map[string]any         `json:"version"`
	Checks        map[string]healthCheck `json:"checks"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	checks := s.healthChecks(r.Context())
	status := "ok"
	httpStatus := http.StatusOK
	for _, c := range checks {
		switch c.Status {
		case "fail":
			status = "fail"
			httpStatus = http.StatusServiceUnavailable
		case "degraded":
			if status == "ok" {
				status = "degraded"
			}
		}
	}
	resp := healthResponse{
		Status:        status,
		Time:          s.now().UTC().Format(time.RFC3339Nano),
		UptimeSeconds: int64(s.now().Sub(s.started).Seconds()),
		Mode:          s.currentMode(),
		Version:       version.Info(),
		Checks:        checks,
	}
	if err := writeJSON(w, httpStatus, resp); err != nil {
		s.log.Warn("write health response", "event", "http_write_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
	}
}

// handleLoginPage sends legacy /login links to the terminal, which opens the
// login modal itself. Keeping the modal in the main page avoids a second window
// and keeps the feed/UI state alive across authentication.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/#login", http.StatusSeeOther)
}

// handleLogout clears every in-memory session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.a.CancelAllLogins()
	s.a.SessionManager().RevokeAll()
	if s.hsm != nil {
		s.hsm.Stop()
	}
	if s.orderFeed != nil {
		s.orderFeed.Stop()
	}
	clearAuthFlowCookie(w)
	s.log.Info("logout", "event", "auth_logout", "request_id", logging.RequestID(r.Context()))
	if err := writeJSON(w, http.StatusOK, map[string]any{"success": true}); err != nil {
		s.log.Warn("write logout response", "event", "http_write_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
	}
}

// handleTotpLogin handles TOTP-based login.
func (s *Server) handleTotpLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TOTP string `json:"totp"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	flowID, err := authFlowID(w, r, true)
	if err != nil {
		http.Error(w, "could not start login flow", http.StatusInternalServerError)
		return
	}
	if err := s.a.BeginLogin(r.Context(), flowID, req.TOTP); err != nil {
		writeAuthError(w, err)
		return
	}
	if s.mpin != "" {
		s.finishMPINLogin(w, r, flowID, s.mpin)
		return
	}
	if err := writeJSON(w, http.StatusOK, map[string]bool{"success": true}); err != nil {
		s.log.Warn("write login response", "event", "http_write_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
	}
}

// handleMPINLogin handles MPIN-based login.
func (s *Server) handleMPINLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MPIN string `json:"mpin"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	flowID, err := authFlowID(w, r, false)
	if err != nil || flowID == "" {
		writeAuthError(w, auth.ErrNoPendingLogin)
		return
	}
	s.finishMPINLogin(w, r, flowID, req.MPIN)
}

func (s *Server) finishMPINLogin(w http.ResponseWriter, r *http.Request, flowID, mpin string) {
	session, err := s.a.CompleteLogin(r.Context(), flowID, mpin)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if s.hsm != nil && s.cfg.Market.DataMode == "websocket" {
		if err := s.hsm.Start(s.cfg.Kotak.UCC, session.BrokerSID, session.FeedURL); err != nil {
			s.log.Error("start Kotak HSM feed", "event", "hsm_start_failed", "error", err,
				"request_id", logging.RequestID(r.Context()))
		}
	}
	if s.orderFeed != nil {
		if err := s.orderFeed.Start(s.feedCtx, orderfeed.Session{BaseURL: session.BaseURL, SID: session.BrokerSID, Token: session.BrokerToken}); err != nil {
			s.log.Error("start Kotak order feed", "event", "order_feed_start_failed", "error", err,
				"request_id", logging.RequestID(r.Context()))
		}
	}
	resp := struct {
		Success bool              `json:"success"`
		Session *auth.SessionData `json:"session_data,omitempty"`
	}{
		Success: true,
		Session: auth.ToSessionData(session),
	}
	clearAuthFlowCookie(w)
	if err := writeJSON(w, http.StatusOK, resp); err != nil {
		s.log.Warn("write login response", "event", "http_write_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
	}
}

const authFlowCookieName = "scalper_auth_flow"

func authFlowID(w http.ResponseWriter, r *http.Request, create bool) (string, error) {
	cookie, err := r.Cookie(authFlowCookieName)
	if err == nil && len(cookie.Value) == 64 {
		if _, decodeErr := hex.DecodeString(cookie.Value); decodeErr == nil {
			return cookie.Value, nil
		}
	}
	if !create {
		return "", nil
	}
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	value := hex.EncodeToString(id[:])
	http.SetCookie(w, &http.Cookie{
		Name:     authFlowCookieName,
		Value:    value,
		Path:     "/api/auth",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int((5 * time.Minute).Seconds()),
	}) // #nosec G124 -- localhost-only app serves over HTTP
	return value, nil
}

func clearAuthFlowCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authFlowCookieName,
		Value:    "",
		Path:     "/api/auth",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	}) // #nosec G124 -- localhost-only app serves over HTTP
}

func writeAuthError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	var inputErr *auth.InputError
	var apiErr *auth.APIError
	switch {
	case errors.Is(err, auth.ErrMissingConsumerKey):
		status = http.StatusServiceUnavailable
	case errors.Is(err, auth.ErrNoPendingLogin):
		status = http.StatusConflict
	case errors.As(err, &inputErr):
		status = http.StatusBadRequest
	case errors.As(err, &apiErr):
		if apiErr.HTTPStatus == http.StatusUnauthorized ||
			apiErr.HTTPStatus == http.StatusForbidden ||
			apiErr.HTTPStatus == http.StatusBadRequest {
			status = http.StatusUnauthorized
		}
	}
	http.Error(w, err.Error(), status)
}

// handleSessionStatus returns the current session status.
func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	status := s.a.SessionStatus()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		s.log.Warn("write session status", "event", "http_write_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
	}
}

func (s *Server) currentMode() string {
	return "live"
}

func (s *Server) healthChecks(ctx context.Context) map[string]healthCheck {
	checks := make(map[string]healthCheck, 3)

	dbCheck := healthCheck{Status: "ok"}
	if s.db == nil {
		dbCheck = healthCheck{Status: "fail", Detail: "database not configured"}
	} else {
		start := s.now()
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := s.db.Ping(pingCtx)
		cancel()
		dbCheck.LatencyMS = s.now().Sub(start).Milliseconds()
		if err != nil {
			dbCheck = healthCheck{Status: "fail", Detail: "database unreachable", LatencyMS: dbCheck.LatencyMS}
		}
	}
	checks["database"] = dbCheck

	logCheck := healthCheck{Status: "ok"}
	if s.loggingDegraded() {
		logCheck = healthCheck{Status: "degraded", Detail: "file sink failed; writing to stderr"}
	}
	checks["logging"] = logCheck

	token := false
	if s.tokenPresent != nil {
		token = s.tokenPresent()
	}
	session := healthCheck{Status: "unauthenticated", Detail: "login required"}
	if token {
		session = healthCheck{Status: "ok", Detail: "consumer key present; session not established"}
	}
	checks["session"] = session

	return checks
}

func (s *Server) loggingDegraded() bool {
	return s.logDegraded != nil && s.logDegraded()
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if recoveredErr, ok := rec.(error); ok && errors.Is(recoveredErr, http.ErrAbortHandler) {
				panic(rec)
			}
			s.log.Error("panic recovered",
				"event", "http_panic",
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
				"method", r.Method,
				"path", r.URL.Path,
				"request_id", logging.RequestID(r.Context()))
			if werr := writeJSONError(w, http.StatusInternalServerError, "internal error", logging.RequestID(r.Context())); werr != nil {
				s.log.Warn("write panic response", "event", "http_write_failed", "error", werr,
					"request_id", logging.RequestID(r.Context()))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !logging.ValidRequestID(id) {
			id = logging.NewRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		}
		s.log.Log(r.Context(), level, "http request",
			"event", "http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"query", logging.RedactQuery(r.URL.RawQuery),
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", s.now().Sub(start).Milliseconds(),
			"request_id", logging.RequestID(r.Context()))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; "+
				"font-src 'self'; "+
				"connect-src 'self' ws: wss:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		if strings.HasPrefix(r.URL.Path, "/static/") {
			h.Set("Cache-Control", "no-cache")
		} else {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func localOnly(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalHost(r.Host) {
			log.Warn("rejected non-local Host",
				"event", "http_non_local_host", "host", r.Host,
				"path", r.URL.Path, "request_id", logging.RequestID(r.Context()))
			if werr := writeJSONError(w, http.StatusForbidden, "localhost only", logging.RequestID(r.Context())); werr != nil {
				log.Warn("write forbidden response", "event", "http_write_failed", "error", werr,
					"request_id", logging.RequestID(r.Context()))
			}
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !isLocalHost(u.Host) {
				log.Warn("rejected non-local Origin",
					"event", "http_non_local_origin", "origin", origin,
					"path", r.URL.Path, "request_id", logging.RequestID(r.Context()))
				if werr := writeJSONError(w, http.StatusForbidden, "localhost only", logging.RequestID(r.Context())); werr != nil {
					log.Warn("write forbidden response", "event", "http_write_failed", "error", werr,
						"request_id", logging.RequestID(r.Context()))
				}
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	controller := http.NewResponseController(r.ResponseWriter)
	conn, rw, err := controller.Hijack()
	if err != nil {
		return nil, nil, err
	}
	r.status = http.StatusSwitchingProtocols
	r.wrote = true
	return conn, rw, nil
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return fmt.Errorf("web: encode json response: %w", err)
	}
	return nil
}

type jsonError struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

func writeJSONError(w http.ResponseWriter, status int, msg, requestID string) error {
	return writeJSON(w, status, jsonError{Error: msg, RequestID: requestID})
}
