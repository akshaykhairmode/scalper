package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/errs"
)

func validConfig() Config {
	c := Default()
	c.Kotak.MobileNumber = "+919999999999"
	c.Kotak.UCC = "TESTUCC"
	return c
}

func clearScalperEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SCALPER_LISTEN", "SCALPER_LOG_LEVEL", "SCALPER_LOG_FILE"} {
		key := key
		old, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if ok {
				if err := os.Setenv(key, old); err != nil {
					t.Errorf("restore %s: %v", key, err)
				}
			} else if err := os.Unsetenv(key); err != nil {
				t.Errorf("clean %s: %v", key, err)
			}
		})
	}
}

func TestLoadExample(t *testing.T) {
	clearScalperEnv(t)
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("Load example: %v", err)
	}
	if cfg.App.Listen != "127.0.0.1:8080" {
		t.Fatalf("listen = %q", cfg.App.Listen)
	}
	if len(cfg.Market.Indices) != 4 {
		t.Fatalf("indices = %d, want 4", len(cfg.Market.Indices))
	}
	if cfg.Market.Indices[3].Key != "SENSEX" || cfg.Market.Indices[3].ExchangeSegment != "bse_fo" {
		t.Fatalf("unexpected SENSEX config: %+v", cfg.Market.Indices[3])
	}
	if !cfg.Logging.Redact {
		t.Fatal("redact must be true")
	}
	if cfg.Trading.Mode != "paper" {
		t.Fatalf("mode = %q", cfg.Trading.Mode)
	}
}

func TestLoadBytesDefaults(t *testing.T) {
	clearScalperEnv(t)
	cfg, err := LoadBytes([]byte("{}"), "inline")
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Kotak.MobileNumber != "" {
		t.Fatalf("default mobile = %q, want empty", cfg.Kotak.MobileNumber)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("defaults without kotak identity should fail validation")
	}
	cfg.Kotak.MobileNumber = "+919999999999"
	cfg.Kotak.UCC = "UCC"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validated defaults: %v", err)
	}
}

func TestEnvOverrides(t *testing.T) {
	clearScalperEnv(t)
	t.Setenv("SCALPER_LISTEN", "localhost:9090")
	t.Setenv("SCALPER_LOG_LEVEL", "debug")
	t.Setenv("SCALPER_LOG_FILE", "")
	cfg, err := LoadBytes([]byte("{}"), "inline")
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.App.Listen != "localhost:9090" {
		t.Fatalf("listen = %q", cfg.App.Listen)
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("level = %q", cfg.Logging.Level)
	}
	if cfg.Logging.File != "" {
		t.Fatalf("log file = %q, want empty", cfg.Logging.File)
	}
}

func TestLoadErrors(t *testing.T) {
	clearScalperEnv(t)
	t.Run("empty path", func(t *testing.T) {
		if _, err := Load(""); !errors.Is(err, errs.ErrConfig) {
			t.Fatalf("err = %v, want ErrConfig", err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
		if !errors.Is(err, errs.ErrConfig) {
			t.Fatalf("err = %v, want ErrConfig", err)
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		_, err := LoadBytes([]byte("app:\n  listen: \"127.0.0.1:1\"\n  nope: 1\n"), "bad.yaml")
		if !errors.Is(err, errs.ErrConfig) {
			t.Fatalf("err = %v, want ErrConfig", err)
		}
		if !strings.Contains(err.Error(), "nope") {
			t.Fatalf("error should name the unknown field: %v", err)
		}
	})
	t.Run("empty file", func(t *testing.T) {
		if _, err := LoadBytes(nil, "empty.yaml"); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("multiple documents", func(t *testing.T) {
		_, err := LoadBytes([]byte("---\n{}\n---\n{}\n"), "multi.yaml")
		if err == nil || !strings.Contains(err.Error(), "multiple") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("validation carries path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		data := []byte("app:\n  listen: \"0.0.0.0:8080\"\n")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		var ce *errs.ConfigError
		if !errors.As(err, &ce) {
			t.Fatalf("err = %v, want ConfigError", err)
		}
		if ce.Path != path || ce.Field != "app.listen" {
			t.Fatalf("ConfigError = %+v", ce)
		}
	})
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantField string
	}{
		{name: "valid defaults", mutate: func(*Config) {}},
		{name: "localhost host", mutate: func(c *Config) { c.App.Listen = "localhost:8080" }},
		{name: "ipv6 loopback", mutate: func(c *Config) { c.App.Listen = "[::1]:8080" }},
		{name: "missing port", mutate: func(c *Config) { c.App.Listen = "127.0.0.1" }, wantField: "app.listen"},
		{name: "non local host", mutate: func(c *Config) { c.App.Listen = "0.0.0.0:8080" }, wantField: "app.listen"},
		{name: "bad port", mutate: func(c *Config) { c.App.Listen = "127.0.0.1:0" }, wantField: "app.listen"},
		{name: "empty db", mutate: func(c *Config) { c.App.DBPath = " " }, wantField: "app.db_path"},
		{name: "bad timezone", mutate: func(c *Config) { c.App.Timezone = "Mars/Olympus" }, wantField: "app.timezone"},
		{name: "bad log level", mutate: func(c *Config) { c.Logging.Level = "trace" }, wantField: "logging.level"},
		{name: "bad log format", mutate: func(c *Config) { c.Logging.Format = "xml" }, wantField: "logging.format"},
		{name: "bad log size", mutate: func(c *Config) { c.Logging.MaxSizeMB = 0 }, wantField: "logging.max_size_mb"},
		{name: "bad log backups", mutate: func(c *Config) { c.Logging.MaxBackups = 0 }, wantField: "logging.max_backups"},
		{name: "bad log retention", mutate: func(c *Config) { c.Logging.RetentionDays = 0 }, wantField: "logging.retention_days"},
		{name: "bad tick sample", mutate: func(c *Config) { c.Logging.TickSampleSeconds = -1 }, wantField: "logging.tick_sample_seconds"},
		{name: "redact disabled", mutate: func(c *Config) { c.Logging.Redact = false }, wantField: "logging.redact"},
		{name: "missing mobile", mutate: func(c *Config) { c.Kotak.MobileNumber = "" }, wantField: "kotak.mobile_number"},
		{name: "missing ucc", mutate: func(c *Config) { c.Kotak.UCC = "" }, wantField: "kotak.ucc"},
		{name: "missing fin key", mutate: func(c *Config) { c.Kotak.NeoFinKey = "" }, wantField: "kotak.neo_fin_key"},
		{name: "bad data mode", mutate: func(c *Config) { c.Market.DataMode = "magic" }, wantField: "market.data_mode"},
		{name: "no indices", mutate: func(c *Config) { c.Market.Indices = nil }, wantField: "market.indices"},
		{name: "duplicate index", mutate: func(c *Config) { c.Market.Indices[1].Key = c.Market.Indices[0].Key }, wantField: "market.indices[1].key"},
		{name: "empty index key", mutate: func(c *Config) { c.Market.Indices[0].Key = "" }, wantField: "market.indices[0].key"},
		{name: "empty display", mutate: func(c *Config) { c.Market.Indices[0].DisplayName = "" }, wantField: "market.indices[0].display_name"},
		{name: "bad exchange", mutate: func(c *Config) { c.Market.Indices[0].ExchangeSegment = "nse_cm" }, wantField: "market.indices[0].exchange_segment"},
		{name: "bad index segment", mutate: func(c *Config) { c.Market.Indices[0].IndexSegment = "nse_fo" }, wantField: "market.indices[0].index_segment"},
		{name: "empty index symbol", mutate: func(c *Config) { c.Market.Indices[0].IndexSymbol = "" }, wantField: "market.indices[0].index_symbol"},
		{name: "bad expiry mode", mutate: func(c *Config) { c.Market.Indices[0].ExpiryMode = "weeklyonly" }, wantField: "market.indices[0].expiry_mode"},
		{name: "bad default expiry", mutate: func(c *Config) { c.Market.Indices[0].DefaultExpiry = "nearest" }, wantField: "market.indices[0].default_expiry"},
		{name: "bad vix segment", mutate: func(c *Config) { c.Market.IndiaVIX.ExchangeSegment = "nse_fo" }, wantField: "market.india_vix.exchange_segment"},
		{name: "empty vix symbol", mutate: func(c *Config) { c.Market.IndiaVIX.Symbol = "" }, wantField: "market.india_vix.symbol"},
		{name: "visible poll too small", mutate: func(c *Config) { c.Market.Chain.VisiblePollMS = 100 }, wantField: "market.chain.visible_poll_ms"},
		{name: "full poll too small", mutate: func(c *Config) { c.Market.Chain.FullPollMS = 100 }, wantField: "market.chain.full_poll_ms"},
		{name: "visible not before full", mutate: func(c *Config) { c.Market.Chain.VisiblePollMS = 5000 }, wantField: "market.chain.visible_poll_ms"},
		{name: "bad batch", mutate: func(c *Config) { c.Market.Chain.BatchSize = 0 }, wantField: "market.chain.batch_size"},
		{name: "bad depth mode", mutate: func(c *Config) { c.Market.Depth.Mode = "magic" }, wantField: "market.depth.mode"},
		{name: "bad holiday", mutate: func(c *Config) { c.Market.Holidays = []string{"2026/10/02"} }, wantField: "market.holidays[0]"},
		{name: "invalid holiday date", mutate: func(c *Config) { c.Market.Holidays = []string{"2026-13-02"} }, wantField: "market.holidays[0]"},
		{name: "bad base timeframe", mutate: func(c *Config) { c.Charts.BaseTimeframe = "5m" }, wantField: "charts.base_timeframe"},
		{name: "no timeframes", mutate: func(c *Config) { c.Charts.Timeframes = nil }, wantField: "charts.timeframes"},
		{name: "unsupported timeframe", mutate: func(c *Config) { c.Charts.Timeframes = []string{"1m", "2h"} }, wantField: "charts.timeframes[1]"},
		{name: "duplicate timeframe", mutate: func(c *Config) { c.Charts.Timeframes = []string{"1m", "1m"} }, wantField: "charts.timeframes[1]"},
		{name: "default timeframe missing", mutate: func(c *Config) { c.Charts.DefaultTimeframe = "2h" }, wantField: "charts.default_timeframe"},
		{name: "bad retention", mutate: func(c *Config) { c.Charts.RetentionDays = 0 }, wantField: "charts.retention_days"},
		{name: "bad trading mode", mutate: func(c *Config) { c.Trading.Mode = "demo" }, wantField: "trading.mode"},
		{name: "bad product", mutate: func(c *Config) { c.Trading.DefaultProduct = "CNC" }, wantField: "trading.default_product"},
		{name: "bad order type", mutate: func(c *Config) { c.Trading.DefaultOrderType = "LIMIT" }, wantField: "trading.default_order_type"},
		{name: "bad market protection", mutate: func(c *Config) { c.Trading.MarketProtection = -1 }, wantField: "trading.market_protection"},
		{name: "zero lots", mutate: func(c *Config) { c.Trading.Quantity.DefaultLots = 0 }, wantField: "trading.quantity.default_lots"},
		{name: "bad unit", mutate: func(c *Config) { c.Trading.Quantity.DefaultUnit = "kg" }, wantField: "trading.quantity.default_unit"},
		{name: "shares disallowed", mutate: func(c *Config) {
			c.Trading.Quantity.AllowShares = false
			c.Trading.Quantity.DefaultUnit = "shares"
		}, wantField: "trading.quantity.default_unit"},
		{name: "bad cash", mutate: func(c *Config) { c.Trading.Paper.StartingCash = 0 }, wantField: "trading.paper.starting_cash"},
		{name: "bad slippage", mutate: func(c *Config) { c.Trading.Paper.SlippageTicks = -1 }, wantField: "trading.paper.slippage_ticks"},
		{name: "bad short margin", mutate: func(c *Config) { c.Trading.Paper.ShortMarginPct = 101 }, wantField: "trading.paper.short_margin_pct"},
		{name: "bad margin refresh", mutate: func(c *Config) { c.Trading.Margin.RefreshMS = 999 }, wantField: "trading.margin.refresh_ms"},
		{name: "bad preview debounce", mutate: func(c *Config) { c.Trading.Margin.PreviewDebounceMS = 99 }, wantField: "trading.margin.preview_debounce_ms"},
		{name: "bad session open", mutate: func(c *Config) { c.Trading.Session.Open = "9:15" }, wantField: "trading.session.open"},
		{name: "bad session close", mutate: func(c *Config) { c.Trading.Session.Close = "25:99" }, wantField: "trading.session.close"},
		{name: "session order", mutate: func(c *Config) { c.Trading.Session.Open = "15:30" }, wantField: "trading.session.open"},
		{name: "negative risk", mutate: func(c *Config) { c.Trading.Risk.MaxDailyLossINR = -1 }, wantField: "trading.risk.max_daily_loss_inr"},
		{name: "bad payoff range", mutate: func(c *Config) { c.Trading.RiskView.PayoffRangePct = 0 }, wantField: "trading.risk_view.payoff_range_pct"},
		{name: "bad payoff points", mutate: func(c *Config) { c.Trading.RiskView.PayoffPoints = 10 }, wantField: "trading.risk_view.payoff_points"},
		{name: "bad refresh at", mutate: func(c *Config) { c.Scrip.RefreshAt = "8:45" }, wantField: "scrip.refresh_at"},
		{name: "no segments", mutate: func(c *Config) { c.Scrip.Segments = nil }, wantField: "scrip.segments"},
		{name: "bad segment", mutate: func(c *Config) { c.Scrip.Segments[0] = "nse_eq" }, wantField: "scrip.segments[0]"},
		{name: "duplicate segment", mutate: func(c *Config) { c.Scrip.Segments = []string{"nse_fo", "nse_fo", "bse_fo", "nse_cm", "bse_cm"} }, wantField: "scrip.segments[1]"},
		{name: "missing index segment", mutate: func(c *Config) { c.Scrip.Segments = []string{"nse_fo", "nse_cm"} }, wantField: "scrip.segments"},
		{name: "missing vix segment", mutate: func(c *Config) {
			c.Market.Indices = c.Market.Indices[:1]
			c.Scrip.Segments = []string{"nse_fo", "nse_cm"}
			c.Market.IndiaVIX.Enabled = true
			c.Market.IndiaVIX.ExchangeSegment = "bse_cm"
		}, wantField: "scrip.segments"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(&c)
			err := c.Validate()
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate succeeded, want field %q", tc.wantField)
			}
			var ce *errs.ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %T %v, want ConfigError", err, err)
			}
			if ce.Field != tc.wantField {
				t.Fatalf("field = %q, want %q (err: %v)", ce.Field, tc.wantField, err)
			}
			if !errors.Is(err, errs.ErrConfig) {
				t.Fatalf("errors.Is ErrConfig = false: %v", err)
			}
		})
	}
}

func TestWarnings(t *testing.T) {
	c := validConfig()
	warnings := c.Warnings()
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "caps are disabled") {
		t.Fatalf("warnings = %v, want disabled caps", warnings)
	}
	c.Trading.Risk.MaxLotsPerOrder = 5
	c.Trading.Margin.CheckBeforeOrder = false
	c.Trading.Margin.FailClosed = false
	c.Market.Depth.Mode = "websocket"
	c.Trading.Mode = "live"
	c.Scrip.Segments = []string{"nse_fo", "bse_fo"}
	warnings = c.Warnings()
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"pre-check is disabled", "not fail-closed", "websocket depth", "nse_cm is not"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings missing %q: %v", want, warnings)
		}
	}
}

func TestSummaryHasNoPII(t *testing.T) {
	c := validConfig()
	c.Kotak.MobileNumber = "SECRET-MOBILE"
	c.Kotak.UCC = "SECRET-UCC"
	raw, err := json.Marshal(c.Summary())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("summary leaked PII: %s", raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["listen"] != "127.0.0.1:8080" {
		t.Fatalf("summary listen = %v", m["listen"])
	}
}

func TestPermissionsWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if warning := PermissionsWarning(path); warning == "" {
		t.Fatal("expected a world-readable warning")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if warning := PermissionsWarning(path); warning != "" {
		t.Fatalf("unexpected warning: %s", warning)
	}
	if warning := PermissionsWarning(filepath.Join(dir, "missing")); warning != "" {
		t.Fatalf("missing file warning = %q", warning)
	}
}

func TestLocation(t *testing.T) {
	c := validConfig()
	if got := c.Location().String(); got != "Asia/Kolkata" {
		t.Fatalf("location = %q", got)
	}
	c.App.Timezone = "Not/AZone"
	if got := c.Location(); got != nil && got.String() != "UTC" {
		t.Fatalf("fallback location = %q, want UTC", got)
	}
}
