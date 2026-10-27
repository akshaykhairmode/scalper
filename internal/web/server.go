// Package web serves the localhost UI, health endpoint and static assets.
package web

import (
	"context"
	"embed"
	"encoding/json"
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
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/logging"
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

// Options configures the HTTP server.
type Options struct {
	Logger       *slog.Logger
	Config       config.Config
	DB           DBPinger
	LogDegraded  func() bool
	TokenPresent func() bool
	Started      time.Time
	Now          func() time.Time
}

// Server holds the HTTP handler and its dependencies.
type Server struct {
	log          *slog.Logger
	cfg          config.Config
	db           DBPinger
	logDegraded  func() bool
	tokenPresent func() bool
	started      time.Time
	now          func() time.Time
	tmpl         *template.Template
	handler      http.Handler
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
	s := &Server{
		log:          opts.Logger,
		cfg:          opts.Config,
		db:           opts.DB,
		logDegraded:  opts.LogDegraded,
		tokenPresent: opts.TokenPresent,
		started:      opts.Started,
		now:          opts.Now,
		tmpl:         tmpl,
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

	var h http.Handler = mux
	h = localOnly(s.log, h)
	h = securityHeaders(h)
	h = s.accessLog(h)
	h = requestID(h)
	h = s.recoverPanic(h)
	s.handler = h
	return s, nil
}

// Handler returns the complete middleware chain.
func (s *Server) Handler() http.Handler { return s.handler }

type pageData struct {
	Title        string
	Mode         string
	Version      string
	Commit       string
	Indices      []string
	Uptime       string
	Now          string
	TokenPresent bool
	LogDegraded  bool
	DBOK         bool
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
	indices := make([]string, 0, len(s.cfg.Market.Indices))
	for _, idx := range s.cfg.Market.Indices {
		indices = append(indices, idx.Key)
	}
	data := pageData{
		Title:        "Scalper",
		Mode:         strings.ToUpper(s.cfg.Trading.Mode),
		Version:      version.Version,
		Commit:       version.Commit,
		Indices:      indices,
		Uptime:       s.now().Sub(s.started).Round(time.Second).String(),
		Now:          s.now().In(s.cfg.Location()).Format("15:04:05 IST"),
		TokenPresent: token,
		LogDegraded:  s.loggingDegraded(),
		DBOK:         dbOK,
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
		Mode:          s.cfg.Trading.Mode,
		Version:       version.Info(),
		Checks:        checks,
	}
	if err := writeJSON(w, httpStatus, resp); err != nil {
		s.log.Warn("write health response", "event", "http_write_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
	}
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
		session = healthCheck{Status: "ok", Detail: "access token present; session not established"}
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
			if rec == http.ErrAbortHandler {
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
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"connect-src 'self' ws: wss:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		if !strings.HasPrefix(r.URL.Path, "/static/") {
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

// Unwrap lets http.ResponseController reach the underlying writer (needed for
// WebSocket upgrades later).
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
