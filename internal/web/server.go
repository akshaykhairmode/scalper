// Package web serves the localhost UI, health endpoint and static assets.
package web

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"

	"github.com/akshaykhairmode/scalper/internal/auth"
	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/orderfeed"
	"github.com/akshaykhairmode/scalper/internal/scrip"
	"github.com/akshaykhairmode/scalper/internal/version"
)

//go:embed templates/*
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
	AggregateCandlesFrom1m(ctx context.Context, instrumentKey, targetTf string) ([]market.Candle, error)
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
	Logger        *slog.Logger
	Config        config.Config
	ConsumerKey   string
	MPIN          string
	AuthClient    auth.KotakAuthenticator
	Market        MarketAPI
	HSM           *market.HSMFeed
	Kotak         *kotakapi.Client
	OrderFeed     *orderfeed.Client
	DB            DBPinger
	CandleStore   CandleStore
	BracketStore  bracket.ActiveStore
	BracketEngine *bracket.Engine
	LogDegraded   func() bool
	TokenPresent  func() bool
	Started       time.Time
	Now           func() time.Time
	ServeMode     bool
	ConfigPath    string
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
	settingsTmpl *template.Template
	handler      http.Handler
	bootstrap    template.JS

	// Auth is the authentication handler.
	a             *auth.Server
	market        MarketAPI
	hsm           *market.HSMFeed
	kotak         *kotakapi.Client
	orderFeed     *orderfeed.Client
	bracketEngine *bracket.Engine
	consumerKey   string
	books         *booksCache
	risk          *riskState

	feedMu      sync.RWMutex
	feedClients map[*feedClient]struct{}
	feedCtx     context.Context
	feedCancel  context.CancelFunc

	indicesMu           sync.Mutex
	indicesRaw          json.RawMessage
	indicesAt           time.Time
	serveMode           bool
	serveSessionsMu     sync.RWMutex
	serveSessions       map[string]time.Time
	serveUnlockMu       sync.Mutex
	serveUnlockFailures map[string]serveUnlockFailure

	configPath string
	configMu   sync.RWMutex
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
	settingsTmpl, err := template.ParseFS(templateFS, "templates/settings.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse settings template: %w", err)
	}
	cfgPath := strings.TrimSpace(opts.ConfigPath)
	if cfgPath == "" {
		cfgPath = "config.yaml"
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
		log:                 opts.Logger,
		cfg:                 opts.Config,
		mpin:                strings.TrimSpace(opts.MPIN),
		db:                  opts.DB,
		candleStore:         opts.CandleStore,
		logDegraded:         opts.LogDegraded,
		tokenPresent:        opts.TokenPresent,
		started:             opts.Started,
		now:                 opts.Now,
		tmpl:                tmpl,
		settingsTmpl:        settingsTmpl,
		bootstrap:           bootstrap,
		a:                   authServer,
		market:              opts.Market,
		hsm:                 opts.HSM,
		kotak:               opts.Kotak,
		orderFeed:           opts.OrderFeed,
		consumerKey:         strings.TrimSpace(opts.ConsumerKey),
		books:               newBooksCache(),
		risk:                newRiskState(),
		feedClients:         make(map[*feedClient]struct{}),
		feedCtx:             feedCtx,
		feedCancel:          feedCancel,
		serveMode:           true,
		serveSessions:       make(map[string]time.Time),
		serveUnlockFailures: make(map[string]serveUnlockFailure),
		configPath:          cfgPath,
	}

	if opts.BracketEngine != nil {
		s.bracketEngine = opts.BracketEngine
	} else {
		placeOrder := func(ctx context.Context, o kotakapi.Order) (string, error) {
			session, ok := s.brokerSessionValue()
			if !ok {
				return "", errors.New("kotak session unavailable or not authenticated")
			}
			raw, err := s.kotak.PlaceOrder(ctx, session, o)
			if err != nil {
				return "", err
			}
			s.books.expire(bookOrders, bookPositions, bookLimits)
			var resp struct {
				NOrdNo  string `json:"nOrdNo"`
				OrderID string `json:"order_id"`
				ID      string `json:"id"`
			}
			_ = json.Unmarshal(raw, &resp)
			orderID := resp.NOrdNo
			if orderID == "" {
				orderID = resp.OrderID
			}
			if orderID == "" {
				orderID = resp.ID
			}
			return orderID, nil
		}
		cancelOrder := func(ctx context.Context, id string) error {
			session, ok := s.brokerSessionValue()
			if !ok {
				return errors.New("kotak session unavailable or not authenticated")
			}
			_, err := s.kotak.CancelOrder(ctx, session, id)
			if err != nil {
				return err
			}
			s.books.expire(bookOrders, bookPositions, bookLimits)
			return nil
		}
		var bStore bracket.Store
		if opts.BracketStore != nil {
			bStore = opts.BracketStore
		} else if as, ok := opts.DB.(bracket.ActiveStore); ok {
			bStore = as
		} else if bs, ok := opts.DB.(bracket.Store); ok {
			bStore = bs
		}
		s.bracketEngine = bracket.NewEngine(bStore, placeOrder, cancelOrder, s.log)
	}

	if s.bracketEngine != nil {
		s.bracketEngine.SetOnChange(func(b bracket.Bracket) {
			s.broadcast(map[string]any{
				"type": "bracket_update",
				"data": b,
			})
		})
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("web: static assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.Handle("GET /static/", s.newStaticHandler(http.StripPrefix("/static/", http.FileServerFS(static))))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(static, "sw.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(static, "manifest.webmanifest")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(static, "manifest.json")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(data)
	})

	// Auth routes
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /api/auth/totp", s.handleTotpLogin)
	mux.HandleFunc("POST /api/auth/mpin", s.handleMPINLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/auth/serve-unlock", s.handleServeUnlock)
	mux.HandleFunc("GET /api/session", s.handleSessionStatus)
	mux.HandleFunc("GET /api/session/status", s.handleSessionStatus)
	mux.HandleFunc("GET /api/version/check", s.handleVersionCheck)
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
	mux.HandleFunc("POST /api/broker/square-off-all", s.handleBrokerSquareOffAll)
	mux.HandleFunc("GET /api/broker/holdings", s.handleBrokerHoldings)
	mux.HandleFunc("GET /api/broker/limits", s.handleBrokerLimits)
	mux.HandleFunc("GET /api/brackets", s.handleListBrackets)
	mux.HandleFunc("POST /api/brackets", s.handleCreateBracket)
	mux.HandleFunc("POST /api/brackets/{id}/cancel", s.handleCancelBracket)
	mux.HandleFunc("POST /api/brackets/{id}/exit", s.handleExitBracket)
	mux.HandleFunc("GET /api/candles", s.handleCandles)
	mux.HandleFunc("GET /api/candles/batch", s.handleCandlesBatch)
	mux.HandleFunc("GET /ws", s.handleWebSocket)
	mux.HandleFunc("GET /logs", s.handleLogsView)
	mux.HandleFunc("GET /api/logs", s.handleLogsAPI)
	mux.HandleFunc("GET /settings", s.handleSettingsView)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/config", s.handleUpdateConfig)
	mux.HandleFunc("PUT /api/config", s.handleUpdateConfig)

	var h http.Handler = mux
	h = s.serveAuthMiddleware(h)
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

// configSnapshot returns the active configuration under the same lock used by
// runtime updates so request handlers never race with config saves.
func (s *Server) configSnapshot() config.Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.cfg
}

// Close stops the server's feed event forwarding and WebSocket clients.
func (s *Server) Close() {
	if s.orderFeed != nil {
		s.orderFeed.Stop()
	}
	if s.feedCancel != nil {
		s.feedCancel()
	}
	if s.a != nil {
		s.a.Close()
	}
	s.feedMu.Lock()
	for client := range s.feedClients {
		if cerr := client.conn.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close client connection during shutdown", "error", cerr)
		}
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
	ServeMode      bool
	BasicAuth      bool
	ServeLocked    bool
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
	s.configMu.RLock()
	cfg := s.cfg
	bootstrap := s.bootstrap
	s.configMu.RUnlock()

	underlyings := make([]pageUnderlying, 0, len(cfg.Market.Indices)+len(cfg.Market.Commodities))
	for _, idx := range cfg.Market.Indices {
		underlyings = append(underlyings, pageUnderlying{
			Key: idx.Key, Label: underlyingLabel(idx.DisplayName, idx.ExchangeSegment),
		})
	}
	for _, commodity := range cfg.Market.Commodities {
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
		Now:            s.now().In(cfg.Location()).Format("15:04:05 IST"),
		TokenPresent:   token,
		LogDegraded:    s.loggingDegraded(),
		DBOK:           dbOK,
		MPINConfigured: s.mpin != "",
		ServeMode:      s.serveMode,
		BasicAuth:      strings.TrimSpace(cfg.Web.BasicAuth.Username) != "",
		ServeLocked:    s.serveMode && strings.TrimSpace(cfg.Web.BasicAuth.Username) != "" && !s.isServeAuthenticated(r),
		Bootstrap:      bootstrap,
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
	Status        string `json:"status"`
	Detail        string `json:"detail,omitempty"`
	LatencyMS     int64  `json:"latency_ms,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	LastEvent     string `json:"last_event,omitempty"`
	Authenticated bool   `json:"authenticated,omitempty"`
	SessionActive bool   `json:"session_active,omitempty"`
	UCC           string `json:"ucc,omitempty"`
	TokenPresent  bool   `json:"token_present,omitempty"`
	LoginMethod   string `json:"login_method,omitempty"`
	ExpiresIn     int    `json:"expires_in,omitempty"`
	Loaded        bool   `json:"loaded,omitempty"`
	Count         int    `json:"count,omitempty"`
	Source        string `json:"source,omitempty"`
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
	s.revokeCurrentServeSession(r)
	s.clearServeSessionCookie(w, r)
	if s.hsm != nil {
		s.hsm.Stop()
	}
	if s.orderFeed != nil {
		s.orderFeed.Stop()
	}
	s.risk.reset()
	clearAuthFlowCookie(w)
	s.log.Info("logout", "event", "auth_logout", "request_id", logging.RequestID(r.Context()))
	if err := writeJSON(w, http.StatusOK, map[string]any{"success": true}); err != nil {
		s.log.Warn("write logout response", "event", "http_write_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
	}
}

func tailLogFile(logPath string, maxLines int) ([]string, error) {
	cleanPath := filepath.Clean(logPath)
	f, err := os.Open(cleanPath) // #nosec G304
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := stat.Size()
	if size == 0 {
		return []string{}, nil
	}

	readSize := int64(2 * 1024 * 1024)
	if readSize > size {
		readSize = size
	}
	buf := make([]byte, readSize)
	_, err = f.ReadAt(buf, size-readSize)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	rawLines := strings.Split(string(buf), "\n")
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}
	if maxLines > 0 && len(rawLines) > maxLines {
		rawLines = rawLines[len(rawLines)-maxLines:]
	}
	return rawLines, nil
}

func (s *Server) handleLogsAPI(w http.ResponseWriter, r *http.Request) {
	logPath := s.configSnapshot().Logging.File
	if logPath == "" {
		_ = writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"path":  "stderr",
			"lines": []string{"Logging is configured to stderr only; no log file on disk."},
		})
		return
	}

	if r.URL.Query().Get("download") == "1" {
		cleanPath := filepath.Clean(logPath)
		w.Header().Set("Content-Disposition", "attachment; filename=scalper.log")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeFile(w, r, cleanPath)
		return
	}

	linesLimit := 500
	if l := r.URL.Query().Get("lines"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 5000 {
			linesLimit = n
		}
	}

	lines, err := tailLogFile(logPath, linesLimit)
	if err != nil {
		_ = writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"path":  logPath,
			"lines": []string{fmt.Sprintf("Log file %s could not be opened: %v", logPath, err)},
		})
		return
	}

	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"path":  logPath,
		"lines": lines,
	})
}

func (s *Server) handleLogsView(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	const html = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Scalper — Server Logs</title>
<style>
  :root {
    --bg: #0b0e14;
    --card: #161b22;
    --border: #30363d;
    --text: #e6edf3;
    --text-dim: #8b949e;
    --accent: #f59e0b;
    --err: #ff7b72;
    --warn: #d29922;
    --info: #58a6ff;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background: var(--bg);
    color: var(--text);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    height: 100vh;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  header {
    background: var(--card);
    border-bottom: 1px solid var(--border);
    padding: 10px 16px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }
  .title-group {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .title-group h1 {
    font-size: 15px;
    font-weight: 600;
    letter-spacing: 0.5px;
  }
  .status-badge {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: 12px;
    background: rgba(46, 160, 67, 0.2);
    color: #3fb950;
    font-weight: 600;
  }
  .controls {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  input[type="text"], select {
    background: #0d1117;
    border: 1px solid var(--border);
    color: var(--text);
    padding: 6px 10px;
    border-radius: 6px;
    font-size: 12px;
    outline: none;
  }
  input[type="text"]:focus, select:focus {
    border-color: var(--accent);
  }
  button, a.btn {
    background: #21262d;
    border: 1px solid var(--border);
    color: var(--text);
    padding: 6px 12px;
    border-radius: 6px;
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
    text-decoration: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    transition: background 0.15s;
  }
  button:hover, a.btn:hover {
    background: #30363d;
  }
  .toggle-label {
    font-size: 12px;
    color: var(--text-dim);
    display: flex;
    align-items: center;
    gap: 6px;
    cursor: pointer;
    user-select: none;
  }
  #log-container {
    flex: 1;
    overflow-y: auto;
    padding: 12px 16px;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 12px;
    line-height: 1.5;
    background: #07090e;
  }
  .log-line {
    white-space: pre-wrap;
    word-break: break-all;
    padding: 2px 0;
    border-bottom: 1px solid rgba(255, 255, 255, 0.02);
  }
  .log-error { color: var(--err); font-weight: 600; }
  .log-warn { color: var(--warn); }
  .log-info { color: var(--text); }
  .line-num {
    color: var(--text-dim);
    user-select: none;
    display: inline-block;
    width: 48px;
    text-align: right;
    margin-right: 12px;
    opacity: 0.5;
  }
  footer {
    background: var(--card);
    border-top: 1px solid var(--border);
    padding: 6px 16px;
    font-size: 11px;
    color: var(--text-dim);
    display: flex;
    justify-content: space-between;
  }
</style>
</head>
<body>
<header>
  <div class="title-group">
    <h1>⚡ Scalper Logs</h1>
    <span class="status-badge" id="live-indicator">● LIVE</span>
    <span id="log-path" style="font-size:11px; color:var(--text-dim);"></span>
  </div>
  <div class="controls">
    <input type="text" id="filter-input" placeholder="Filter logs (e.g. error, order, hsm)..." size="28">
    <select id="lines-select">
      <option value="200">200 lines</option>
      <option value="500" selected>500 lines</option>
      <option value="1000">1000 lines</option>
      <option value="2500">2500 lines</option>
    </select>
    <label class="toggle-label">
      <input type="checkbox" id="autoscroll-toggle" checked> Auto-scroll
    </label>
    <label class="toggle-label">
      <input type="checkbox" id="autorefresh-toggle" checked> Auto-refresh (3s)
    </label>
    <button type="button" id="refresh-btn">Refresh</button>
    <a href="/api/logs?download=1" class="btn" download>Download</a>
  </div>
</header>
<div id="log-container">
  <div style="color:var(--text-dim); text-align:center; padding:40px;">Loading server logs…</div>
</div>
<footer>
  <span id="log-summary">Showing 0 lines</span>
  <span id="last-updated">—</span>
</footer>
<script src="/static/logs.js"></script>
</body>
</html>`
	_, _ = w.Write([]byte(html))
}

// handleTotpLogin handles TOTP-based login.
func (s *Server) handleTotpLogin(w http.ResponseWriter, r *http.Request) {
	if !requireJSONContentType(r) {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
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
		select {
		case <-time.After(1200 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
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
	if !requireJSONContentType(r) {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
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
	cfg := s.configSnapshot()
	if s.hsm != nil && cfg.Market.DataMode == "websocket" {
		if err := s.hsm.Start(cfg.Kotak.UCC, session.BrokerSID, session.FeedURL); err != nil {
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
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is set dynamically for HTTPS
		Name:     authFlowCookieName,
		Value:    value,
		Path:     "/api/auth",
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int((5 * time.Minute).Seconds()),
	})
	return value, nil
}

func clearAuthFlowCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124
		Name:     authFlowCookieName,
		Value:    "",
		Path:     "/api/auth",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
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
	checks := make(map[string]healthCheck, 6)

	dbCheck := healthCheck{Status: "ok", Detail: "connected"}
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
		} else {
			dbCheck.Detail = fmt.Sprintf("connected (%d ms)", dbCheck.LatencyMS)
		}
	}
	checks["database"] = dbCheck

	logCheck := healthCheck{Status: "ok", Detail: "file and console sinks active"}
	if s.loggingDegraded() {
		logCheck = healthCheck{Status: "degraded", Detail: "file sink failed; writing to stderr"}
	}
	checks["logging"] = logCheck

	token := false
	if s.tokenPresent != nil {
		token = s.tokenPresent()
	}
	sessionStatus := "unauthenticated"
	sessionDetail := "login required"
	if token {
		sessionStatus = "ok"
		sessionDetail = "consumer key present; session not established"
	}
	sessionActive := false
	loginMethod := ""
	expiresIn := 0
	if s.a != nil {
		if sess := s.a.SessionStatus(); sess != nil && sess.Authenticated {
			sessionActive = true
			sessionStatus = "ok"
			loginMethod = sess.LoginMethod
			expiresIn = sess.ExpiresIn
			sessionDetail = fmt.Sprintf("session active via %s (expires in %dm)", sess.LoginMethod, sess.ExpiresIn)
		}
	}
	uccMasked := s.configSnapshot().Kotak.UCC
	if len(uccMasked) > 2 {
		uccMasked = uccMasked[:2] + strings.Repeat("*", len(uccMasked)-2)
	}
	sessionCheck := healthCheck{
		Status:        sessionStatus,
		Detail:        sessionDetail,
		Authenticated: sessionActive,
		SessionActive: sessionActive,
		UCC:           uccMasked,
		TokenPresent:  token,
		LoginMethod:   loginMethod,
		ExpiresIn:     expiresIn,
	}
	checks["session"] = sessionCheck
	checks["broker"] = sessionCheck

	marketCheck := healthCheck{
		Status: "stopped",
		Detail: "market feed not configured",
	}
	if s.hsm != nil {
		hsmSt := s.hsm.Status()
		lastEv := ""
		if !hsmSt.LastEvent.IsZero() {
			lastEv = hsmSt.LastEvent.UTC().Format(time.RFC3339)
		}
		detail := hsmSt.Status
		if hsmSt.Endpoint != "" {
			detail += fmt.Sprintf(" (%s)", hsmSt.Endpoint)
		}
		if lastEv != "" {
			detail += fmt.Sprintf(", last event %s", lastEv)
		}
		marketCheck = healthCheck{
			Status:    hsmSt.Status,
			Detail:    detail,
			Endpoint:  hsmSt.Endpoint,
			LastEvent: lastEv,
		}
	}
	checks["market_feed"] = marketCheck

	orderCheck := healthCheck{
		Status: "disconnected",
		Detail: "order feed not configured",
	}
	if s.orderFeed != nil {
		ofSt := s.orderFeed.Status()
		lastEv := ""
		if !ofSt.LastEvent.IsZero() {
			lastEv = ofSt.LastEvent.UTC().Format(time.RFC3339)
		}
		detail := ofSt.Status
		if ofSt.Endpoint != "" {
			detail += fmt.Sprintf(" (%s)", ofSt.Endpoint)
		}
		if lastEv != "" {
			detail += fmt.Sprintf(", last event %s", lastEv)
		}
		orderCheck = healthCheck{
			Status:    ofSt.Status,
			Detail:    detail,
			Endpoint:  ofSt.Endpoint,
			LastEvent: lastEv,
		}
	}
	checks["order_feed"] = orderCheck

	scripCheck := healthCheck{
		Status: "ok",
		Detail: "scrip master not configured",
	}
	type scripStatusReporter interface {
		ScripStatus() scrip.Status
	}
	var scripStat *scrip.Status
	if rep, ok := s.market.(scripStatusReporter); ok {
		st := rep.ScripStatus()
		scripStat = &st
	} else if s.hsm != nil {
		st := s.hsm.ScripStatus()
		scripStat = &st
	}
	if scripStat != nil {
		total := scripStat.Options + scripStat.Spots
		detail := fmt.Sprintf("%d contracts loaded (%s)", total, scripStat.Source)
		if !scripStat.Loaded {
			if scripStat.LastError != "" {
				detail = fmt.Sprintf("cache load error: %s", scripStat.LastError)
			} else {
				detail = "cache not loaded"
			}
		}
		scripCheck = healthCheck{
			Status: "ok",
			Detail: detail,
			Loaded: scripStat.Loaded,
			Count:  total,
			Source: scripStat.Source,
		}
	}
	checks["scrip_master"] = scripCheck

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
				"connect-src 'self' ws: wss: https://api.github.com; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
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

type staticBrAsset struct {
	data  []byte
	etag  string
	ctype string
}

func (s *Server) newStaticHandler(fallback http.Handler) http.Handler {
	cache := make(map[string]staticBrAsset)

	entries, err := fs.ReadDir(staticFS, "static")
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".br") {
				continue
			}
			ext := filepath.Ext(name)
			switch ext {
			case ".js", ".css", ".html", ".svg", ".json", ".txt", ".webmanifest":
			default:
				continue
			}

			ctype := mime.TypeByExtension(ext)
			if ctype == "" {
				switch ext {
				case ".js":
					ctype = "application/javascript; charset=utf-8"
				case ".css":
					ctype = "text/css; charset=utf-8"
				case ".webmanifest":
					ctype = "application/manifest+json"
				case ".json":
					ctype = "application/json; charset=utf-8"
				case ".svg":
					ctype = "image/svg+xml"
				default:
					ctype = "application/octet-stream"
				}
			}

			// 1. Try reading pre-compressed .br file from staticFS
			brData, err := staticFS.ReadFile(path.Join("static", name+".br"))
			if err != nil {
				// 2. Fallback: compress in-memory at startup if raw file exists (e.g. during go run)
				raw, readErr := staticFS.ReadFile(path.Join("static", name))
				if readErr == nil && len(raw) > 0 {
					var buf bytes.Buffer
					w := brotli.NewWriterOptions(&buf, brotli.WriterOptions{
						Quality: brotli.BestCompression,
					})
					if _, copyErr := w.Write(raw); copyErr == nil && w.Close() == nil {
						brData = buf.Bytes()
					}
				}
			}

			if len(brData) > 0 {
				cache[name] = staticBrAsset{
					data:  brData,
					etag:  fmt.Sprintf(`"%x"`, sha256.Sum256(brData)),
					ctype: ctype,
				}
			}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		if name == "" || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}

		if strings.Contains(r.Header.Get("Accept-Encoding"), "br") {
			if asset, ok := cache[name]; ok {
				w.Header().Set("Content-Type", asset.ctype)
				w.Header().Set("Content-Encoding", "br")
				w.Header().Set("Vary", "Accept-Encoding")
				w.Header().Set("ETag", asset.etag)

				if match := r.Header.Get("If-None-Match"); match == asset.etag {
					w.WriteHeader(http.StatusNotModified)
					return
				}

				w.WriteHeader(http.StatusOK)
				if _, err := w.Write(asset.data); err != nil {
					slog.Debug("failed to write compressed asset", "asset", name, "error", err)
				}
				return
			}
		}

		fallback.ServeHTTP(w, r)
	})
}
