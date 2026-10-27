// Package config loads, defaults, overrides and validates the application
// configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/akshaykhairmode/scalper/internal/errs"
)

// Config is the root of config.yaml.
type Config struct {
	App     App     `yaml:"app"`
	Logging Logging `yaml:"logging"`
	Kotak   Kotak   `yaml:"kotak"`
	Market  Market  `yaml:"market"`
	Charts  Charts  `yaml:"charts"`
	Trading Trading `yaml:"trading"`
	Scrip   Scrip   `yaml:"scrip"`
	Web     Web     `yaml:"web"`
}

// App holds process-level settings.
type App struct {
	Listen   string `yaml:"listen"`
	DBPath   string `yaml:"db_path"`
	Timezone string `yaml:"timezone"`
}

// Logging holds structured logging settings.
type Logging struct {
	Level             string `yaml:"level"`
	Format            string `yaml:"format"`
	File              string `yaml:"file"`
	MaxSizeMB         int    `yaml:"max_size_mb"`
	MaxBackups        int    `yaml:"max_backups"`
	RetentionDays     int    `yaml:"retention_days"`
	TickSampleSeconds int    `yaml:"tick_sample_seconds"`
	Redact            bool   `yaml:"redact"`
}

// Kotak holds the non-secret parts of the broker identity. The access token is
// read from KOTAK_ACCESS_TOKEN only; TOTP and MPIN are never configured.
type Kotak struct {
	MobileNumber string `yaml:"mobile_number"`
	UCC          string `yaml:"ucc"`
	NeoFinKey    string `yaml:"neo_fin_key"`
}

// Market holds index, chain and feed settings.
type Market struct {
	DataMode string   `yaml:"data_mode"`
	Indices  []Index  `yaml:"indices"`
	IndiaVIX IndiaVIX `yaml:"india_vix"`
	Chain    Chain    `yaml:"chain"`
	Depth    Depth    `yaml:"depth"`
	Holidays []string `yaml:"holidays"`
}

// Index is one configurable underlying.
type Index struct {
	Key             string `yaml:"key"`
	DisplayName     string `yaml:"display_name"`
	ExchangeSegment string `yaml:"exchange_segment"`
	IndexSegment    string `yaml:"index_segment"`
	IndexSymbol     string `yaml:"index_symbol"`
	// ScripName is the pSymbolName used by the Scrip Master for this
	// underlying (and its index spot row). It defaults to Key when empty.
	ScripName     string `yaml:"scrip_name"`
	ExpiryMode    string `yaml:"expiry_mode"`
	DefaultExpiry string `yaml:"default_expiry"`
}

// IndiaVIX controls the optional volatility widget.
type IndiaVIX struct {
	Enabled         bool   `yaml:"enabled"`
	ExchangeSegment string `yaml:"exchange_segment"`
	Symbol          string `yaml:"symbol"`
	// ScripName is the pSymbolName in the Scrip Master; defaults to Symbol.
	ScripName string `yaml:"scrip_name"`
}

// Chain holds option-chain polling cadence.
type Chain struct {
	VisiblePollMS   int  `yaml:"visible_poll_ms"`
	FullPollMS      int  `yaml:"full_poll_ms"`
	BatchSize       int  `yaml:"batch_size"`
	VisiblePriority bool `yaml:"visible_priority"`
}

// Depth holds market-depth settings.
type Depth struct {
	Enabled bool   `yaml:"enabled"`
	Mode    string `yaml:"mode"`
}

// Charts holds candle settings.
type Charts struct {
	BaseTimeframe    string   `yaml:"base_timeframe"`
	Timeframes       []string `yaml:"timeframes"`
	DefaultTimeframe string   `yaml:"default_timeframe"`
	RetentionDays    int      `yaml:"retention_days"`
	Persist          bool     `yaml:"persist"`
}

// Trading holds mode, quantity, margin, session and risk settings.
type Trading struct {
	Mode              string   `yaml:"mode"`
	DefaultProduct    string   `yaml:"default_product"`
	DefaultOrderType  string   `yaml:"default_order_type"`
	ConfirmLiveOrders bool     `yaml:"confirm_live_orders"`
	MarketProtection  int      `yaml:"market_protection"`
	Quantity          Quantity `yaml:"quantity"`
	Paper             Paper    `yaml:"paper"`
	Margin            Margin   `yaml:"margin"`
	Session           Session  `yaml:"session"`
	Risk              Risk     `yaml:"risk"`
	RiskView          RiskView `yaml:"risk_view"`
}

// Quantity controls the default order quantity and unit.
type Quantity struct {
	DefaultLots int    `yaml:"default_lots"`
	DefaultUnit string `yaml:"default_unit"`
	AllowShares bool   `yaml:"allow_shares"`
}

// Paper holds simulated-account settings.
type Paper struct {
	StartingCash   float64 `yaml:"starting_cash"`
	SlippageTicks  int     `yaml:"slippage_ticks"`
	ShortMarginPct float64 `yaml:"short_margin_pct"`
}

// Margin holds pre-trade margin settings.
type Margin struct {
	CheckBeforeOrder  bool `yaml:"check_before_order"`
	FailClosed        bool `yaml:"fail_closed"`
	RefreshMS         int  `yaml:"refresh_ms"`
	PreviewDebounceMS int  `yaml:"preview_debounce_ms"`
}

// Session holds the trading window in IST.
type Session struct {
	Open              string `yaml:"open"`
	Close             string `yaml:"close"`
	AllowOutsideHours bool   `yaml:"allow_outside_hours"`
}

// Risk holds self-imposed caps. Zero means "no cap" for every cap field.
type Risk struct {
	MaxLotsPerOrder        int `yaml:"max_lots_per_order"`
	MaxOpenLotsPerIndex    int `yaml:"max_open_lots_per_index"`
	MaxOrderValueINR       int `yaml:"max_order_value_inr"`
	MaxDailyLossINR        int `yaml:"max_daily_loss_inr"`
	MaxOrdersPerMinute     int `yaml:"max_orders_per_minute"`
	MaxExitOrdersPerSecond int `yaml:"max_exit_orders_per_second"`
	BlockOnDataStaleMS     int `yaml:"block_on_data_stale_ms"`
}

// RiskView holds payoff chart settings.
type RiskView struct {
	Enabled        bool `yaml:"enabled"`
	PayoffRangePct int  `yaml:"payoff_range_pct"`
	PayoffPoints   int  `yaml:"payoff_points"`
	IncludePreview bool `yaml:"include_preview"`
}

// Scrip holds Scrip Master refresh settings.
type Scrip struct {
	RefreshAt string   `yaml:"refresh_at"`
	Segments  []string `yaml:"segments"`
}

// Web holds browser settings.
type Web struct {
	OpenBrowser bool `yaml:"open_browser"`
}

// Default returns the documented defaults. A minimal config file can omit any
// section and inherit these values.
func Default() Config {
	return Config{
		App: App{
			Listen:   "127.0.0.1:8080",
			DBPath:   "./data/scalper.db",
			Timezone: "Asia/Kolkata",
		},
		Logging: Logging{
			Level:             "info",
			Format:            "json",
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
					IndexSymbol: "Nifty Midcap Select", ScripName: "MIDCPNIFTY", ExpiryMode: "both",
					DefaultExpiry: "nearest_weekly",
				},
				{
					Key: "SENSEX", DisplayName: "SENSEX",
					ExchangeSegment: "bse_fo", IndexSegment: "bse_cm",
					IndexSymbol: "SENSEX", ScripName: "SENSEX", ExpiryMode: "both",
					DefaultExpiry: "nearest_weekly",
				},
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
				BatchSize:       50,
				VisiblePriority: true,
			},
			Depth: Depth{Enabled: true, Mode: "poll"},
		},
		Charts: Charts{
			BaseTimeframe:    "1m",
			Timeframes:       []string{"1m", "3m", "5m", "15m", "1h"},
			DefaultTimeframe: "5m",
			RetentionDays:    30,
			Persist:          true,
		},
		Trading: Trading{
			Mode:              "paper",
			DefaultProduct:    "MIS",
			DefaultOrderType:  "MARKET",
			ConfirmLiveOrders: true,
			Quantity: Quantity{
				DefaultLots: 1,
				DefaultUnit: "lots",
				AllowShares: true,
			},
			Paper: Paper{
				StartingCash:   1_000_000,
				SlippageTicks:  1,
				ShortMarginPct: 15,
			},
			Margin: Margin{
				CheckBeforeOrder:  true,
				FailClosed:        true,
				RefreshMS:         5000,
				PreviewDebounceMS: 300,
			},
			Session: Session{
				Open:  "09:15",
				Close: "15:30",
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
		},
		Scrip: Scrip{
			RefreshAt: "08:45",
			Segments:  []string{"nse_fo", "bse_fo", "nse_cm", "bse_cm"},
		},
		Web: Web{OpenBrowser: true},
	}
}

// Load reads path, applies defaults and environment overrides, then validates.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, &errs.ConfigError{Err: errors.New("config path is empty")}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, &errs.ConfigError{Path: path, Err: fmt.Errorf("read: %w", err)}
	}
	cfg, err := LoadBytes(data, path)
	if err != nil {
		return Config{}, err
	}
	cfg.applyEnv()
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
	if err := dec.Decode(&extra); err != io.EOF {
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
}

// IsLive reports whether the startup mode is live trading.
func (c Config) IsLive() bool { return c.Trading.Mode == "live" }

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
	r := c.Trading.Risk
	if r.MaxLotsPerOrder == 0 && r.MaxOpenLotsPerIndex == 0 && r.MaxOrderValueINR == 0 {
		out = append(out, "all lot and order-value caps are disabled (0); exposure is limited only by manual entry")
	}
	if !c.Trading.Margin.CheckBeforeOrder && c.IsLive() {
		out = append(out, "live margin pre-check is disabled; entries will not be blocked for insufficient funds")
	}
	if !c.Trading.Margin.FailClosed && c.IsLive() {
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
		"mode":       c.Trading.Mode,
		"data_mode":  c.Market.DataMode,
		"indices":    keys,
		"paper_cash": c.Trading.Paper.StartingCash,
		"risk_caps_disabled": c.Trading.Risk.MaxLotsPerOrder == 0 &&
			c.Trading.Risk.MaxOpenLotsPerIndex == 0 && c.Trading.Risk.MaxOrderValueINR == 0,
	}
}
