// Package config loads, defaults, overrides and validates the application
// configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/akshaykhairmode/scalper/internal/errs"
)

// Config is the root of config.yaml.
type Config struct {
	App     App     `yaml:"app" json:"app"`
	Logging Logging `yaml:"logging" json:"logging"`
	Kotak   Kotak   `yaml:"kotak" json:"kotak"`
	Market  Market  `yaml:"market" json:"market"`
	Charts  Charts  `yaml:"charts" json:"charts"`
	Trading Trading `yaml:"trading" json:"trading"`
	Scrip   Scrip   `yaml:"scrip" json:"scrip"`
	Web     Web     `yaml:"web" json:"web"`
	AI      AI      `yaml:"ai" json:"ai"`
}

// App holds process-level settings.
type App struct {
	Listen    string `yaml:"listen" json:"listen"`
	DBPath    string `yaml:"db_path" json:"db_path"`
	Timezone  string `yaml:"timezone" json:"timezone"`
	ServeMode bool   `yaml:"-" json:"serve_mode"` // set at runtime by the -serve flag; not read from config
}

// Logging holds structured logging settings.
type Logging struct {
	Level             string `yaml:"level" json:"level"`
	Format            string `yaml:"format" json:"format"`
	File              string `yaml:"file" json:"file"`
	MaxSizeMB         int    `yaml:"max_size_mb" json:"max_size_mb"`
	MaxBackups        int    `yaml:"max_backups" json:"max_backups"`
	RetentionDays     int    `yaml:"retention_days" json:"retention_days"`
	TickSampleSeconds int    `yaml:"tick_sample_seconds" json:"tick_sample_seconds"`
	Redact            bool   `yaml:"redact" json:"redact"`
}

// Kotak holds the non-secret parts of the broker identity. The consumer key is
// read from KOTAK_CONSUMER_KEY only; TOTP and MPIN are never configured.
type Kotak struct {
	MobileNumber string `yaml:"mobile_number" json:"mobile_number"`
	UCC          string `yaml:"ucc" json:"ucc"`
	NeoFinKey    string `yaml:"neo_fin_key" json:"neo_fin_key"`
}

// Market holds index, chain and feed settings.
type Market struct {
	DataMode    string      `yaml:"data_mode" json:"data_mode"`
	HSMURL      string      `yaml:"hsm_url" json:"hsm_url"`
	Indices     []Index     `yaml:"indices" json:"indices"`
	Commodities []Commodity `yaml:"commodities" json:"commodities"`
	IndiaVIX    IndiaVIX    `yaml:"india_vix" json:"india_vix"`
	Chain       Chain       `yaml:"chain" json:"chain"`
	Depth       Depth       `yaml:"depth" json:"depth"`
	Holidays    []string    `yaml:"holidays" json:"holidays"`

	Background BackgroundCollection `yaml:"background_collection" json:"background_collection"`
}

// BackgroundCollection configures the TOTP-free REST poller that keeps candle
// history and paper-position tracking alive when the SFeed is unavailable.
type BackgroundCollection struct {
	Enabled              bool     `yaml:"enabled" json:"enabled"`
	IntervalSeconds      int      `yaml:"interval_seconds" json:"interval_seconds"`
	Underlyings          []string `yaml:"underlyings" json:"underlyings"`
	StrikeWindow         int      `yaml:"strike_window" json:"strike_window"`
	IncludeOpenPositions bool     `yaml:"include_open_positions" json:"include_open_positions"`
	PersistLastSelection bool     `yaml:"persist_last_selection" json:"persist_last_selection"`
}

// Commodity is an option underlying traded on a commodity exchange.
type Commodity struct {
	Key             string `yaml:"key" json:"key"`
	DisplayName     string `yaml:"display_name" json:"display_name"`
	ExchangeSegment string `yaml:"exchange_segment" json:"exchange_segment"`
	ScripName       string `yaml:"scrip_name" json:"scrip_name"`
}

// Index is one configurable underlying.
type Index struct {
	Key             string `yaml:"key" json:"key"`
	DisplayName     string `yaml:"display_name" json:"display_name"`
	ExchangeSegment string `yaml:"exchange_segment" json:"exchange_segment"`
	IndexSegment    string `yaml:"index_segment" json:"index_segment"`
	IndexSymbol     string `yaml:"index_symbol" json:"index_symbol"`
	// ScripName is the pSymbolName used by the Scrip Master for this
	// underlying (and its index spot row). It defaults to Key when empty.
	ScripName     string `yaml:"scrip_name" json:"scrip_name"`
	ExpiryMode    string `yaml:"expiry_mode" json:"expiry_mode"`
	DefaultExpiry string `yaml:"default_expiry" json:"default_expiry"`
}

// IndiaVIX controls the optional volatility widget.
type IndiaVIX struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`
	ExchangeSegment string `yaml:"exchange_segment" json:"exchange_segment"`
	Symbol          string `yaml:"symbol" json:"symbol"`
	// ScripName is the pSymbolName in the Scrip Master; defaults to Symbol.
	ScripName string `yaml:"scrip_name" json:"scrip_name"`
}

// Chain holds option-chain polling cadence.
type Chain struct {
	VisiblePollMS   int     `yaml:"visible_poll_ms" json:"visible_poll_ms"`
	FullPollMS      int     `yaml:"full_poll_ms" json:"full_poll_ms"`
	BatchSize       int     `yaml:"batch_size" json:"batch_size"`
	VisiblePriority bool    `yaml:"visible_priority" json:"visible_priority"`
	PremiumCutoff   float64 `yaml:"premium_cutoff" json:"premium_cutoff"`
}

// Depth holds market-depth settings.
type Depth struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Mode    string `yaml:"mode" json:"mode"`
}

// Charts holds candle settings.
type Charts struct {
	BaseTimeframe    string   `yaml:"base_timeframe" json:"base_timeframe"`
	Timeframes       []string `yaml:"timeframes" json:"timeframes"`
	DefaultTimeframe string   `yaml:"default_timeframe" json:"default_timeframe"`
	RetentionDays    int      `yaml:"retention_days" json:"retention_days"`
	Persist          bool     `yaml:"persist" json:"persist"`
}

// Trading holds quantity, margin, session and risk settings.
type Trading struct {
	DefaultProduct           string   `yaml:"default_product" json:"default_product"`
	DefaultOrderType         string   `yaml:"default_order_type" json:"default_order_type"`
	ConfirmLiveOrders        bool     `yaml:"confirm_live_orders" json:"confirm_live_orders"`
	ConfirmChartLevelChanges bool     `yaml:"confirm_chart_level_changes" json:"confirm_chart_level_changes"`
	MarketProtection         int      `yaml:"market_protection" json:"market_protection"`
	Quantity                 Quantity `yaml:"quantity" json:"quantity"`
	Margin                   Margin   `yaml:"margin" json:"margin"`
	Session                  Session  `yaml:"session" json:"session"`
	Risk                     Risk     `yaml:"risk" json:"risk"`
	RiskView                 RiskView `yaml:"risk_view" json:"risk_view"`
	Bracket                  Bracket  `yaml:"bracket" json:"bracket"`
}

// Bracket holds server-side bracket and exchange backstop defaults.
type Bracket struct {
	ExchangeStop ExchangeStop `yaml:"exchange_stop" json:"exchange_stop"`
}

// ExchangeStop configures the optional exchange-side catastrophe stop-loss.
// Mode is only a default for new orders; the per-order choice wins.
type ExchangeStop struct {
	Mode               string  `yaml:"mode" json:"mode"` // off | sl_m | sl_l
	DistanceMode       string  `yaml:"distance_mode" json:"distance_mode"`
	DistanceValue      float64 `yaml:"distance_value" json:"distance_value"`
	SLLOffsetTicks     int     `yaml:"sl_l_offset_ticks" json:"sl_l_offset_ticks"`
	PlaceOnPartialFill bool    `yaml:"place_on_partial_fill" json:"place_on_partial_fill"`
	CancelRetries      int     `yaml:"cancel_retries" json:"cancel_retries"`
	CancelRetryDelayMS int     `yaml:"cancel_retry_delay_ms" json:"cancel_retry_delay_ms"`
	OnCancelFailure    string  `yaml:"on_cancel_failure" json:"on_cancel_failure"`
	ReconcileOnStartup bool    `yaml:"reconcile_on_startup" json:"reconcile_on_startup"`
	AllowChartEdit     bool    `yaml:"allow_chart_edit" json:"allow_chart_edit"`
}

// Quantity controls the default order quantity and unit.
type Quantity struct {
	DefaultLots int    `yaml:"default_lots" json:"default_lots"`
	DefaultUnit string `yaml:"default_unit" json:"default_unit"`
	AllowShares bool   `yaml:"allow_shares" json:"allow_shares"`
}

// Margin holds pre-trade margin settings.
type Margin struct {
	CheckBeforeOrder  bool `yaml:"check_before_order" json:"check_before_order"`
	FailClosed        bool `yaml:"fail_closed" json:"fail_closed"`
	RefreshMS         int  `yaml:"refresh_ms" json:"refresh_ms"`
	PreviewDebounceMS int  `yaml:"preview_debounce_ms" json:"preview_debounce_ms"`
}

// Session holds the trading window in IST.
type Session struct {
	Open              string `yaml:"open" json:"open"`
	Close             string `yaml:"close" json:"close"`
	AllowOutsideHours bool   `yaml:"allow_outside_hours" json:"allow_outside_hours"`
}

// Risk holds self-imposed caps. Zero means "no cap" for every cap field.
type Risk struct {
	MaxLotsPerOrder        int `yaml:"max_lots_per_order" json:"max_lots_per_order"`
	MaxOpenLotsPerIndex    int `yaml:"max_open_lots_per_index" json:"max_open_lots_per_index"`
	MaxOrderValueINR       int `yaml:"max_order_value_inr" json:"max_order_value_inr"`
	MaxDailyLossINR        int `yaml:"max_daily_loss_inr" json:"max_daily_loss_inr"`
	MaxOrdersPerMinute     int `yaml:"max_orders_per_minute" json:"max_orders_per_minute"`
	MaxExitOrdersPerSecond int `yaml:"max_exit_orders_per_second" json:"max_exit_orders_per_second"`
	BlockOnDataStaleMS     int `yaml:"block_on_data_stale_ms" json:"block_on_data_stale_ms"`
}

// RiskView holds payoff chart settings.
type RiskView struct {
	Enabled        bool `yaml:"enabled" json:"enabled"`
	PayoffRangePct int  `yaml:"payoff_range_pct" json:"payoff_range_pct"`
	PayoffPoints   int  `yaml:"payoff_points" json:"payoff_points"`
	IncludePreview bool `yaml:"include_preview" json:"include_preview"`
}

// Scrip holds Scrip Master refresh settings.
type Scrip struct {
	RefreshAt string   `yaml:"refresh_at" json:"refresh_at"`
	Segments  []string `yaml:"segments" json:"segments"`
}

// Web holds browser settings.
type Web struct {
	OpenBrowser    bool      `yaml:"open_browser" json:"open_browser"`
	BasicAuth      BasicAuth `yaml:"basic_auth" json:"basic_auth"`
	AllowedOrigins []string  `yaml:"allowed_origins" json:"allowed_origins"`
	InsecureNoAuth bool      `yaml:"insecure_no_auth" json:"insecure_no_auth"`
}

// HasBasicAuth reports whether both username and password are non-empty.
func (w Web) HasBasicAuth() bool {
	return strings.TrimSpace(w.BasicAuth.Username) != "" && strings.TrimSpace(w.BasicAuth.Password) != ""
}

// BasicAuth holds optional HTTP Basic Auth credentials for serve mode.
// Leave both fields empty to disable Basic Auth.
type BasicAuth struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// AI holds configuration for TypeSafe Jev decision models and generic LLM reasoning agents.
type AI struct {
	Enabled             bool        `yaml:"enabled" json:"enabled"`
	ShadowMode          bool        `yaml:"shadow_mode" json:"shadow_mode"`
	PeriodicScan        bool        `yaml:"periodic_scan" json:"periodic_scan"`
	ScanIntervalMinutes int         `yaml:"scan_interval_minutes" json:"scan_interval_minutes"`
	ScannedUnderlyings  []string    `yaml:"scanned_underlyings,omitempty" json:"scanned_underlyings"`
	TargetPct           float64     `yaml:"target_pct" json:"target_pct"`
	StopLossPct         float64     `yaml:"stop_loss_pct" json:"stop_loss_pct"`
	MinConfidence       float64     `yaml:"min_confidence" json:"min_confidence"`
	MinScore            int         `yaml:"min_score" json:"min_score"`
	EvalCadenceSeconds  int         `yaml:"eval_cadence_seconds" json:"eval_cadence_seconds"`
	MaxHoldMinutes      int         `yaml:"max_hold_minutes" json:"max_hold_minutes"`
	CooldownMinutes     int         `yaml:"cooldown_minutes" json:"cooldown_minutes"`
	RoundTripCostPct    float64     `yaml:"round_trip_cost_pct" json:"round_trip_cost_pct"`
	Jev                 JevConfig   `yaml:"jev" json:"jev"`
	Agent               AgentConfig `yaml:"agent" json:"agent"`
}

// JevConfig holds settings for TypeSafe AI Jev decision models (System 1).
type JevConfig struct {
	BaseURL   string `yaml:"base_url" json:"base_url"`
	APIKey    string `yaml:"api_key" json:"api_key"`
	Model     string `yaml:"model" json:"model"`
	TimeoutMS int    `yaml:"timeout_ms" json:"timeout_ms"`
}

// AgentConfig holds settings for generic LLM reasoning agents (System 2: Ollama, OpenRouter, Gemini, OpenAI).
type AgentConfig struct {
	Provider  string `yaml:"provider" json:"provider"` // openrouter, ollama, gemini, openai, generic
	BaseURL   string `yaml:"base_url" json:"base_url"` // e.g. "https://openrouter.ai/api/v1", "http://localhost:11434/v1"
	APIKey    string `yaml:"api_key" json:"api_key"`
	Model     string `yaml:"model" json:"model"`
	TimeoutMS int    `yaml:"timeout_ms" json:"timeout_ms"`
}

// Default returns the documented defaults. A minimal config file can omit any
// section and inherit these values.
func Default() Config {
	return Config{
		App: App{
			Listen:    "127.0.0.1:8080",
			DBPath:    "./data/scalper.db",
			Timezone:  "Asia/Kolkata",
			ServeMode: true,
		},
		Logging: Logging{
			Level:             "info",
			Format:            "text",
			File:              "./logs/scalper.log",
			MaxSizeMB:         100,
			MaxBackups:        10,
			RetentionDays:     14,
			TickSampleSeconds: 1,
			Redact:            true,
		},
		Kotak: Kotak{NeoFinKey: "neotradeapi"},
		Market: Market{
			DataMode: "websocket",
			HSMURL:   "",
			Indices: []Index{
				{
					Key: "NIFTY", DisplayName: "NIFTY",
					ExchangeSegment: "nse_fo", IndexSegment: "nse_cm",
					IndexSymbol: "Nifty 50", ScripName: "NIFTY", ExpiryMode: "both",
					DefaultExpiry: "nearest_weekly",
				},
				{
					Key: "BANKNIFTY", DisplayName: "BANKNIFTY",
					ExchangeSegment: "nse_fo", IndexSegment: "nse_cm",
					IndexSymbol: "Nifty Bank", ScripName: "BANKNIFTY", ExpiryMode: "both",
					DefaultExpiry: "nearest_weekly",
				},
				{
					Key: "MIDCPNIFTY", DisplayName: "MIDCPNIFTY",
					ExchangeSegment: "nse_fo", IndexSegment: "nse_cm",
					IndexSymbol: "NIFTY MID SELECT", ScripName: "MIDCPNIFTY", ExpiryMode: "both",
					DefaultExpiry: "nearest_weekly",
				},
				{
					Key: "SENSEX", DisplayName: "SENSEX",
					ExchangeSegment: "bse_fo", IndexSegment: "bse_cm",
					IndexSymbol: "SENSEX", ScripName: "SENSEX", ExpiryMode: "both",
					DefaultExpiry: "nearest_weekly",
				},
			},
			Commodities: []Commodity{
				{Key: "CRUDEOIL", DisplayName: "CRUDE OIL", ExchangeSegment: "mcx_fo", ScripName: "CRUDEOIL"},
				{Key: "GOLD", DisplayName: "GOLD", ExchangeSegment: "mcx_fo", ScripName: "GOLD"},
				{Key: "NATURALGAS", DisplayName: "NATURAL GAS", ExchangeSegment: "mcx_fo", ScripName: "NATURALGAS"},
			},
			IndiaVIX: IndiaVIX{
				Enabled:         true,
				ExchangeSegment: "nse_cm",
				Symbol:          "INDIA VIX",
				ScripName:       "INDIA VIX",
			},
			Chain: Chain{
				VisiblePollMS:   1000,
				FullPollMS:      5000,
				BatchSize:       40,
				VisiblePriority: true,
				PremiumCutoff:   30,
			},
			Depth: Depth{Enabled: true, Mode: "poll"},
			Background: BackgroundCollection{
				Enabled:              false,
				IntervalSeconds:      1,
				Underlyings:          []string{"NIFTY", "SENSEX", "CRUDEOIL"},
				StrikeWindow:         1,
				IncludeOpenPositions: true,
				PersistLastSelection: true,
			},
		},
		Charts: Charts{
			BaseTimeframe:    "1m",
			Timeframes:       []string{"1m", "3m", "5m", "15m", "1h"},
			DefaultTimeframe: "5m",
			RetentionDays:    30,
			Persist:          true,
		},
		Trading: Trading{
			DefaultProduct:           "MIS",
			DefaultOrderType:         "MARKET",
			ConfirmLiveOrders:        true,
			ConfirmChartLevelChanges: true,
			Quantity: Quantity{
				DefaultLots: 1,
				DefaultUnit: "lots",
				AllowShares: true,
			},
			Margin: Margin{
				CheckBeforeOrder:  true,
				FailClosed:        true,
				RefreshMS:         5000,
				PreviewDebounceMS: 300,
			},
			Session: Session{
				Open:  "09:15",
				Close: "15:40",
			},
			Risk: Risk{
				MaxDailyLossINR:        5000,
				MaxOrdersPerMinute:     6,
				MaxExitOrdersPerSecond: 4,
				BlockOnDataStaleMS:     5000,
			},
			RiskView: RiskView{
				Enabled:        true,
				PayoffRangePct: 10,
				PayoffPoints:   121,
				IncludePreview: true,
			},
			Bracket: Bracket{
				ExchangeStop: ExchangeStop{
					Mode:               "off",
					DistanceMode:       "multiplier",
					DistanceValue:      2.5,
					SLLOffsetTicks:     2,
					PlaceOnPartialFill: true,
					CancelRetries:      3,
					CancelRetryDelayMS: 100,
					OnCancelFailure:    "alert",
					ReconcileOnStartup: true,
					AllowChartEdit:     false,
				},
			},
		},
		Scrip: Scrip{
			RefreshAt: "08:45",
			Segments:  []string{"nse_fo", "bse_fo", "nse_cm", "bse_cm", "mcx_fo"},
		},
		Web: Web{OpenBrowser: true},
		AI: AI{
			Enabled:             false,
			ShadowMode:          true,
			PeriodicScan:        true,
			ScanIntervalMinutes: 15,
			ScannedUnderlyings:  []string{"NIFTY", "SENSEX"},
			TargetPct:           8.0,
			StopLossPct:         5.0,
			MinConfidence:       0.60,
			MinScore:            3,
			EvalCadenceSeconds:  15,
			MaxHoldMinutes:      120,
			CooldownMinutes:     15,
			RoundTripCostPct:    0.10,
			Jev: JevConfig{
				BaseURL:   "https://openrouter.ai/api/v1",
				Model:     "~typesafe/jev-latest",
				TimeoutMS: 8000,
			},
			Agent: AgentConfig{
				Provider:  "openrouter",
				BaseURL:   "https://openrouter.ai/api/v1",
				Model:     "nvidia/nemotron-3.5-lightning:free",
				TimeoutMS: 90000,
			},
		},
	}
}

// Option customizes Config during loading.
type Option func(*Config)

// WithServeMode configures serve mode on the Config: binds listen to 0.0.0.0
// only if unspecified, sets ServeMode to true, and disables browser auto-open.
func WithServeMode(_ bool) Option {
	return func(c *Config) {
		host, port, err := net.SplitHostPort(c.App.Listen)
		if err == nil && port != "" {
			if host == "" {
				c.App.Listen = net.JoinHostPort("0.0.0.0", port)
			}
		} else if host == "" && port == "" {
			c.App.Listen = "0.0.0.0:8080"
		}
		c.App.ServeMode = true
		c.Web.OpenBrowser = false
	}
}

// WithInsecureNoAuth allows binding to public interfaces without basic auth.
func WithInsecureNoAuth(insecure bool) Option {
	return func(c *Config) {
		c.Web.InsecureNoAuth = insecure
	}
}

// Load reads path, applies defaults and environment overrides, runs options, then validates.
func Load(path string, opts ...Option) (Config, error) {
	if path == "" {
		return Config{}, &errs.ConfigError{Err: errors.New("config path is empty")}
	}
	// The path is supplied explicitly by the local operator via -config.
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return Config{}, &errs.ConfigError{Path: path, Err: fmt.Errorf("read: %w", err)}
	}
	cfg, err := LoadBytes(data, path)
	if err != nil {
		return Config{}, err
	}
	cfg.applyEnv()
	cfg.App.ServeMode = true
	cfg.Web.OpenBrowser = false
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if err := cfg.Validate(); err != nil {
		var ce *errs.ConfigError
		if errors.As(err, &ce) && ce.Path == "" {
			ce.Path = path
		}
		return Config{}, err
	}
	return cfg, nil
}

// LoadBytes decodes YAML into the defaults and applies environment overrides.
// It does not validate; callers that need a usable config call Validate.
func LoadBytes(data []byte, path string) (Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return cfg, &errs.ConfigError{Path: path, Err: errors.New("file is empty")}
		}
		return Config{}, &errs.ConfigError{Path: path, Err: err}
	}
	// Reject a second YAML document: silently ignoring it hides mistakes.
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, &errs.ConfigError{Path: path, Err: errors.New("multiple YAML documents are not supported")}
		}
		return Config{}, &errs.ConfigError{Path: path, Err: err}
	}
	cfg.applyEnv()
	return cfg, nil
}

func (c *Config) applyEnv() {
	if v, ok := os.LookupEnv("SCALPER_LISTEN"); ok {
		c.App.Listen = v
	}
	if v, ok := os.LookupEnv("SCALPER_LOG_LEVEL"); ok {
		c.Logging.Level = v
	}
	if v, ok := os.LookupEnv("SCALPER_LOG_FILE"); ok {
		c.Logging.File = v
	}
	if v, ok := os.LookupEnv("KOTAK_MOBILE_NUMBER"); ok && strings.TrimSpace(v) != "" {
		c.Kotak.MobileNumber = strings.TrimSpace(v)
	} else if v, ok := os.LookupEnv("KOTAK_MOBILE"); ok && strings.TrimSpace(v) != "" {
		c.Kotak.MobileNumber = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("KOTAK_UCC"); ok && strings.TrimSpace(v) != "" {
		c.Kotak.UCC = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("KOTAK_NEO_FIN_KEY"); ok && strings.TrimSpace(v) != "" {
		c.Kotak.NeoFinKey = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("SCALPER_SERVE_USERNAME"); ok && strings.TrimSpace(v) != "" {
		c.Web.BasicAuth.Username = strings.TrimSpace(v)
	} else if v, ok := os.LookupEnv("SERVE_AUTH_USERNAME"); ok && strings.TrimSpace(v) != "" {
		c.Web.BasicAuth.Username = strings.TrimSpace(v)
	} else if v, ok := os.LookupEnv("SCALPER_BASIC_AUTH_USERNAME"); ok && strings.TrimSpace(v) != "" {
		c.Web.BasicAuth.Username = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("SCALPER_SERVE_PASSWORD"); ok && strings.TrimSpace(v) != "" {
		c.Web.BasicAuth.Password = strings.TrimSpace(v)
	} else if v, ok := os.LookupEnv("SERVE_AUTH_PASSWORD"); ok && strings.TrimSpace(v) != "" {
		c.Web.BasicAuth.Password = strings.TrimSpace(v)
	} else if v, ok := os.LookupEnv("SCALPER_BASIC_AUTH_PASSWORD"); ok && strings.TrimSpace(v) != "" {
		c.Web.BasicAuth.Password = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("SCALPER_INSECURE_NO_AUTH"); ok {
		c.Web.InsecureNoAuth = strings.EqualFold(v, "true") || v == "1"
	}
	if v, ok := os.LookupEnv("SCALPER_ALLOWED_ORIGINS"); ok && strings.TrimSpace(v) != "" {
		parts := strings.Split(v, ",")
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				c.Web.AllowedOrigins = append(c.Web.AllowedOrigins, s)
			}
		}
	}
	if v, ok := os.LookupEnv("TYPESAFE_API_KEY"); ok && strings.TrimSpace(v) != "" {
		c.AI.Jev.APIKey = strings.TrimSpace(v)
		if c.AI.Jev.BaseURL == "" || strings.Contains(c.AI.Jev.BaseURL, "openrouter.ai") {
			c.AI.Jev.BaseURL = "https://api.typesafe.ai/v1"
		}
	} else if v, ok := os.LookupEnv("OPENROUTER_API_KEY"); ok && strings.TrimSpace(v) != "" {
		openRouterKey := strings.TrimSpace(v)
		c.AI.Jev.APIKey = openRouterKey
		if c.AI.Jev.BaseURL == "" || strings.Contains(c.AI.Jev.BaseURL, "typesafe.ai") {
			c.AI.Jev.BaseURL = "https://openrouter.ai/api/v1"
		}
		if c.AI.Jev.Model == "" {
			c.AI.Jev.Model = "~typesafe/jev-latest"
		} else if !strings.HasPrefix(c.AI.Jev.Model, "~") {
			switch {
			case c.AI.Jev.Model == "jev-latest" || c.AI.Jev.Model == "typesafe/jev-latest":
				// -latest is OpenRouter's tilde alias namespace.
				c.AI.Jev.Model = "~typesafe/jev-latest"
			case strings.HasPrefix(c.AI.Jev.Model, "typesafe/"):
				// Pinned TypeSafe model: keep it exactly as configured.
			case strings.Contains(c.AI.Jev.Model, "/"):
				// Another vendor's model on OpenRouter: leave it alone.
			default:
				c.AI.Jev.Model = "typesafe/" + c.AI.Jev.Model
			}
		}
	}
	if v, ok := os.LookupEnv("AI_AGENT_API_KEY"); ok && strings.TrimSpace(v) != "" {
		c.AI.Agent.APIKey = strings.TrimSpace(v)
	} else if v, ok := os.LookupEnv("OPENROUTER_API_KEY"); ok && strings.TrimSpace(v) != "" {
		c.AI.Agent.APIKey = strings.TrimSpace(v)
		if c.AI.Agent.BaseURL == "" || c.AI.Agent.BaseURL == "https://api.openai.com/v1" {
			c.AI.Agent.BaseURL = "https://openrouter.ai/api/v1"
		}
		if c.AI.Agent.Provider == "" {
			c.AI.Agent.Provider = "openrouter"
		}
		if c.AI.Agent.TimeoutMS < 90000 {
			c.AI.Agent.TimeoutMS = 90000
		}
	} else if v, ok := os.LookupEnv("GEMINI_API_KEY"); ok && strings.TrimSpace(v) != "" {
		c.AI.Agent.APIKey = strings.TrimSpace(v)
	} else if v, ok := os.LookupEnv("OPENAI_API_KEY"); ok && strings.TrimSpace(v) != "" {
		c.AI.Agent.APIKey = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AI_ENABLED"); ok {
		c.AI.Enabled = strings.EqualFold(v, "true") || v == "1"
	}
	if v, ok := os.LookupEnv("AI_SHADOW_MODE"); ok {
		c.AI.ShadowMode = strings.EqualFold(v, "true") || v == "1"
	}
	if v, ok := os.LookupEnv("AI_AGENT_PROVIDER"); ok && strings.TrimSpace(v) != "" {
		c.AI.Agent.Provider = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AI_AGENT_MODEL"); ok && strings.TrimSpace(v) != "" {
		c.AI.Agent.Model = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AI_JEV_MODEL"); ok && strings.TrimSpace(v) != "" {
		c.AI.Jev.Model = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AI_PERIODIC_SCAN"); ok {
		c.AI.PeriodicScan = strings.EqualFold(v, "true") || v == "1"
	}
	if v, ok := os.LookupEnv("AI_SCAN_INTERVAL_MINUTES"); ok {
		if val, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && val > 0 {
			c.AI.ScanIntervalMinutes = val
		}
	}
	if v, ok := os.LookupEnv("AI_SCANNED_UNDERLYINGS"); ok && strings.TrimSpace(v) != "" {
		c.AI.ScannedUnderlyings = nil
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				c.AI.ScannedUnderlyings = append(c.AI.ScannedUnderlyings, strings.ToUpper(s))
			}
		}
	}
}

// Location returns the configured timezone. It assumes Validate succeeded.
func (c Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.App.Timezone)
	if err != nil {
		// Validate has already rejected this; fall back to UTC defensively.
		return time.UTC
	}
	return loc
}

// PermissionsWarning reports a world-readable config file, which may contain
// account identifiers. It returns "" when the file is fine or the platform
// does not expose POSIX permissions.
func PermissionsWarning(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if info.Mode().Perm()&0o004 != 0 {
		return fmt.Sprintf("config file %s is world-readable; restrict it to the owner (chmod 600)", path)
	}
	return ""
}

// Warnings returns non-fatal configuration advisories for startup logging.
func (c Config) Warnings() []string {
	var out []string
	if c.App.ServeMode {
		ba := c.Web.BasicAuth
		if strings.TrimSpace(ba.Username) == "" {
			out = append(out, "serve mode is active without basic auth; the terminal is accessible by anyone who can reach the port")
		}
	}
	r := c.Trading.Risk
	if r.MaxLotsPerOrder == 0 && r.MaxOpenLotsPerIndex == 0 && r.MaxOrderValueINR == 0 {
		out = append(out, "all lot and order-value caps are disabled (0); exposure is limited only by manual entry")
	}
	if !c.Trading.Margin.CheckBeforeOrder {
		out = append(out, "live margin pre-check is disabled; entries will not be blocked for insufficient funds")
	}
	if !c.Trading.Margin.FailClosed {
		out = append(out, "margin check is not fail-closed; a failed check may allow a live entry")
	}
	if c.Market.Depth.Mode == "websocket" {
		out = append(out, "websocket depth consumes market-feed scrips and a channel; verify the quota")
	}
	if len(c.Scrip.Segments) > 0 && !contains(c.Scrip.Segments, "nse_cm") {
		out = append(out, "nse_cm is not in scrip segments; NIFTY/BANKNIFTY/MIDCPNIFTY spot metadata will be unavailable")
	}
	return out
}

// Summary returns loggable configuration without PII or secrets.
func (c Config) Summary() map[string]any {
	keys := make([]string, 0, len(c.Market.Indices))
	for _, idx := range c.Market.Indices {
		keys = append(keys, idx.Key)
	}
	return map[string]any{
		"listen":     c.App.Listen,
		"db_path":    c.App.DBPath,
		"timezone":   c.App.Timezone,
		"log_level":  c.Logging.Level,
		"log_format": c.Logging.Format,
		"log_file":   c.Logging.File,
		"data_mode":  c.Market.DataMode,
		"hsm_url":    c.Market.HSMURL,
		"indices":    keys,
		"risk_caps_disabled": c.Trading.Risk.MaxLotsPerOrder == 0 &&
			c.Trading.Risk.MaxOpenLotsPerIndex == 0 && c.Trading.Risk.MaxOrderValueINR == 0,
		"ai_enabled": c.AI.Enabled,
		"ai_shadow":  c.AI.ShadowMode,
	}
}
