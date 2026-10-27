package config

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/errs"
)

var (
	hhmmPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

var chartTimeframes = map[string]bool{
	"1m": true, "3m": true, "5m": true, "15m": true, "1h": true,
}

var scripSegments = map[string]bool{
	"nse_fo": true, "bse_fo": true, "nse_cm": true, "bse_cm": true,
}

// Validate checks every documented rule. It returns a *errs.ConfigError with
// the field name so the message points at the exact YAML key.
func (c Config) Validate() error {
	if err := c.validateApp(); err != nil {
		return err
	}
	if err := c.validateLogging(); err != nil {
		return err
	}
	if err := c.validateKotak(); err != nil {
		return err
	}
	if err := c.validateMarket(); err != nil {
		return err
	}
	if err := c.validateCharts(); err != nil {
		return err
	}
	if err := c.validateTrading(); err != nil {
		return err
	}
	if err := c.validateScrip(); err != nil {
		return err
	}
	return nil
}

func (c Config) validateApp() error {
	host, port, err := net.SplitHostPort(c.App.Listen)
	if err != nil {
		return fieldErr("app.listen", "must be host:port: %v", err)
	}
	if !isLoopbackHost(host) {
		return fieldErr("app.listen", "must bind to localhost (127.0.0.1, ::1 or localhost), got %q", host)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fieldErr("app.listen", "invalid port %q", port)
	}
	if strings.TrimSpace(c.App.DBPath) == "" {
		return fieldErr("app.db_path", "must not be empty")
	}
	if _, err := time.LoadLocation(c.App.Timezone); err != nil {
		return fieldErr("app.timezone", "unknown location %q: %v", c.App.Timezone, err)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c Config) validateLogging() error {
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fieldErr("logging.level", "must be debug|info|warn|error, got %q", c.Logging.Level)
	}
	switch c.Logging.Format {
	case "json", "text":
	default:
		return fieldErr("logging.format", "must be json|text, got %q", c.Logging.Format)
	}
	if c.Logging.MaxSizeMB <= 0 {
		return fieldErr("logging.max_size_mb", "must be > 0")
	}
	if c.Logging.MaxBackups <= 0 {
		return fieldErr("logging.max_backups", "must be > 0")
	}
	if c.Logging.RetentionDays <= 0 {
		return fieldErr("logging.retention_days", "must be > 0")
	}
	if c.Logging.TickSampleSeconds < 0 {
		return fieldErr("logging.tick_sample_seconds", "must be >= 0 (0 disables sampled tick lines)")
	}
	if !c.Logging.Redact {
		return fieldErr("logging.redact", "must be true; the application refuses to log unredacted secrets")
	}
	return nil
}

func (c Config) validateKotak() error {
	if strings.TrimSpace(c.Kotak.MobileNumber) == "" {
		return fieldErr("kotak.mobile_number", "must not be empty")
	}
	if strings.TrimSpace(c.Kotak.UCC) == "" {
		return fieldErr("kotak.ucc", "must not be empty")
	}
	if strings.TrimSpace(c.Kotak.NeoFinKey) == "" {
		return fieldErr("kotak.neo_fin_key", "must not be empty")
	}
	return nil
}

func (c Config) validateMarket() error {
	switch c.Market.DataMode {
	case "websocket", "poll":
	default:
		return fieldErr("market.data_mode", "must be websocket|poll, got %q", c.Market.DataMode)
	}
	if len(c.Market.Indices) == 0 {
		return fieldErr("market.indices", "at least one index is required")
	}
	seen := make(map[string]bool, len(c.Market.Indices))
	scripSeen := make(map[string]bool, len(c.Market.Indices))
	for i, idx := range c.Market.Indices {
		prefix := fmt.Sprintf("market.indices[%d]", i)
		if strings.TrimSpace(idx.Key) == "" {
			return fieldErr(prefix+".key", "must not be empty")
		}
		if seen[idx.Key] {
			return fieldErr(prefix+".key", "duplicate key %q", idx.Key)
		}
		seen[idx.Key] = true
		scripName := strings.TrimSpace(idx.ScripName)
		if scripName == "" {
			scripName = idx.Key
		}
		normalized := normalizeScripName(scripName)
		if scripSeen[normalized] {
			return fieldErr(prefix+".scrip_name", "duplicate scrip name %q", scripName)
		}
		scripSeen[normalized] = true
		if strings.TrimSpace(idx.DisplayName) == "" {
			return fieldErr(prefix+".display_name", "must not be empty")
		}
		switch idx.ExchangeSegment {
		case "nse_fo", "bse_fo":
		default:
			return fieldErr(prefix+".exchange_segment", "must be nse_fo|bse_fo, got %q", idx.ExchangeSegment)
		}
		switch idx.IndexSegment {
		case "nse_cm", "bse_cm":
		default:
			return fieldErr(prefix+".index_segment", "must be nse_cm|bse_cm, got %q", idx.IndexSegment)
		}
		if strings.TrimSpace(idx.IndexSymbol) == "" {
			return fieldErr(prefix+".index_symbol", "must not be empty")
		}
		switch idx.ExpiryMode {
		case "both", "weekly", "monthly":
		default:
			return fieldErr(prefix+".expiry_mode", "must be both|weekly|monthly, got %q", idx.ExpiryMode)
		}
		switch idx.DefaultExpiry {
		case "nearest_weekly", "nearest_monthly":
		default:
			return fieldErr(prefix+".default_expiry", "must be nearest_weekly|nearest_monthly, got %q", idx.DefaultExpiry)
		}
	}
	if c.Market.IndiaVIX.Enabled {
		switch c.Market.IndiaVIX.ExchangeSegment {
		case "nse_cm", "bse_cm":
		default:
			return fieldErr("market.india_vix.exchange_segment", "must be nse_cm|bse_cm, got %q", c.Market.IndiaVIX.ExchangeSegment)
		}
		if strings.TrimSpace(c.Market.IndiaVIX.Symbol) == "" {
			return fieldErr("market.india_vix.symbol", "must not be empty when enabled")
		}
		vixScrip := strings.TrimSpace(c.Market.IndiaVIX.ScripName)
		if vixScrip == "" {
			vixScrip = c.Market.IndiaVIX.Symbol
		}
		if scripSeen[normalizeScripName(vixScrip)] {
			return fieldErr("market.india_vix.scrip_name", "duplicate scrip name %q", vixScrip)
		}
	}
	if c.Market.Chain.VisiblePollMS < 250 {
		return fieldErr("market.chain.visible_poll_ms", "must be >= 250")
	}
	if c.Market.Chain.FullPollMS < 250 {
		return fieldErr("market.chain.full_poll_ms", "must be >= 250")
	}
	if c.Market.Chain.VisiblePollMS >= c.Market.Chain.FullPollMS {
		return fieldErr("market.chain.visible_poll_ms", "must be less than full_poll_ms (%d >= %d)",
			c.Market.Chain.VisiblePollMS, c.Market.Chain.FullPollMS)
	}
	if c.Market.Chain.BatchSize < 1 || c.Market.Chain.BatchSize > 500 {
		return fieldErr("market.chain.batch_size", "must be between 1 and 500")
	}
	switch c.Market.Depth.Mode {
	case "poll", "websocket":
	default:
		return fieldErr("market.depth.mode", "must be poll|websocket, got %q", c.Market.Depth.Mode)
	}
	for i, h := range c.Market.Holidays {
		if !datePattern.MatchString(h) {
			return fieldErr(fmt.Sprintf("market.holidays[%d]", i), "must be YYYY-MM-DD, got %q", h)
		}
		if _, err := time.Parse("2006-01-02", h); err != nil {
			return fieldErr(fmt.Sprintf("market.holidays[%d]", i), "invalid date %q: %v", h, err)
		}
	}
	return nil
}

func (c Config) validateCharts() error {
	if c.Charts.BaseTimeframe != "1m" {
		return fieldErr("charts.base_timeframe", "must be 1m, got %q", c.Charts.BaseTimeframe)
	}
	if len(c.Charts.Timeframes) == 0 {
		return fieldErr("charts.timeframes", "at least one timeframe is required")
	}
	seen := make(map[string]bool, len(c.Charts.Timeframes))
	for i, tf := range c.Charts.Timeframes {
		if !chartTimeframes[tf] {
			return fieldErr(fmt.Sprintf("charts.timeframes[%d]", i), "unsupported timeframe %q", tf)
		}
		if seen[tf] {
			return fieldErr(fmt.Sprintf("charts.timeframes[%d]", i), "duplicate timeframe %q", tf)
		}
		seen[tf] = true
	}
	if !seen[c.Charts.DefaultTimeframe] {
		return fieldErr("charts.default_timeframe", "%q is not in charts.timeframes", c.Charts.DefaultTimeframe)
	}
	if c.Charts.RetentionDays < 1 || c.Charts.RetentionDays > 3650 {
		return fieldErr("charts.retention_days", "must be between 1 and 3650")
	}
	return nil
}

func (c Config) validateTrading() error {
	switch c.Trading.Mode {
	case "paper", "live":
	default:
		return fieldErr("trading.mode", "must be paper|live, got %q", c.Trading.Mode)
	}
	switch c.Trading.DefaultProduct {
	case "MIS", "NRML":
	default:
		return fieldErr("trading.default_product", "must be MIS|NRML, got %q", c.Trading.DefaultProduct)
	}
	if c.Trading.DefaultOrderType != "MARKET" {
		return fieldErr("trading.default_order_type", "must be MARKET (v1 supports market orders only), got %q", c.Trading.DefaultOrderType)
	}
	if c.Trading.MarketProtection < 0 {
		return fieldErr("trading.market_protection", "must be >= 0")
	}
	if c.Trading.Quantity.DefaultLots < 1 {
		return fieldErr("trading.quantity.default_lots", "must be >= 1")
	}
	switch c.Trading.Quantity.DefaultUnit {
	case "lots", "shares":
	default:
		return fieldErr("trading.quantity.default_unit", "must be lots|shares, got %q", c.Trading.Quantity.DefaultUnit)
	}
	if !c.Trading.Quantity.AllowShares && c.Trading.Quantity.DefaultUnit == "shares" {
		return fieldErr("trading.quantity.default_unit", "cannot be shares when allow_shares is false")
	}
	if c.Trading.Paper.StartingCash <= 0 {
		return fieldErr("trading.paper.starting_cash", "must be > 0")
	}
	if c.Trading.Paper.SlippageTicks < 0 {
		return fieldErr("trading.paper.slippage_ticks", "must be >= 0")
	}
	if c.Trading.Paper.ShortMarginPct < 0 || c.Trading.Paper.ShortMarginPct > 100 {
		return fieldErr("trading.paper.short_margin_pct", "must be between 0 and 100")
	}
	if c.Trading.Margin.RefreshMS < 1000 {
		return fieldErr("trading.margin.refresh_ms", "must be >= 1000")
	}
	if c.Trading.Margin.PreviewDebounceMS < 100 {
		return fieldErr("trading.margin.preview_debounce_ms", "must be >= 100")
	}
	open, err := parseHHMM("trading.session.open", c.Trading.Session.Open)
	if err != nil {
		return err
	}
	closeAt, err := parseHHMM("trading.session.close", c.Trading.Session.Close)
	if err != nil {
		return err
	}
	if !open.Before(closeAt) {
		return fieldErr("trading.session.open", "must be before trading.session.close")
	}
	if err := validateRisk(c.Trading.Risk); err != nil {
		return err
	}
	if c.Trading.RiskView.PayoffRangePct < 1 || c.Trading.RiskView.PayoffRangePct > 100 {
		return fieldErr("trading.risk_view.payoff_range_pct", "must be between 1 and 100")
	}
	if c.Trading.RiskView.PayoffPoints < 20 || c.Trading.RiskView.PayoffPoints > 1000 {
		return fieldErr("trading.risk_view.payoff_points", "must be between 20 and 1000")
	}
	return nil
}

func validateRisk(r Risk) error {
	values := []struct {
		name  string
		value int
	}{
		{"max_lots_per_order", r.MaxLotsPerOrder},
		{"max_open_lots_per_index", r.MaxOpenLotsPerIndex},
		{"max_order_value_inr", r.MaxOrderValueINR},
		{"max_daily_loss_inr", r.MaxDailyLossINR},
		{"max_orders_per_minute", r.MaxOrdersPerMinute},
		{"max_exit_orders_per_second", r.MaxExitOrdersPerSecond},
		{"block_on_data_stale_ms", r.BlockOnDataStaleMS},
	}
	for _, v := range values {
		if v.value < 0 {
			return fieldErr("trading.risk."+v.name, "must be >= 0 (0 disables the cap)")
		}
	}
	return nil
}

func (c Config) validateScrip() error {
	if _, err := parseHHMM("scrip.refresh_at", c.Scrip.RefreshAt); err != nil {
		return err
	}
	if len(c.Scrip.Segments) == 0 {
		return fieldErr("scrip.segments", "at least one segment is required")
	}
	seen := make(map[string]bool, len(c.Scrip.Segments))
	for i, seg := range c.Scrip.Segments {
		if !scripSegments[seg] {
			return fieldErr(fmt.Sprintf("scrip.segments[%d]", i), "unsupported segment %q", seg)
		}
		if seen[seg] {
			return fieldErr(fmt.Sprintf("scrip.segments[%d]", i), "duplicate segment %q", seg)
		}
		seen[seg] = true
	}
	for _, idx := range c.Market.Indices {
		if !seen[idx.ExchangeSegment] {
			return fieldErr("scrip.segments", "missing %q required by index %s", idx.ExchangeSegment, idx.Key)
		}
		if !seen[idx.IndexSegment] {
			return fieldErr("scrip.segments", "missing %q required by index %s spot", idx.IndexSegment, idx.Key)
		}
	}
	if c.Market.IndiaVIX.Enabled && !seen[c.Market.IndiaVIX.ExchangeSegment] {
		return fieldErr("scrip.segments", "missing %q required by INDIA VIX", c.Market.IndiaVIX.ExchangeSegment)
	}
	return nil
}

func parseHHMM(field, value string) (time.Time, error) {
	if !hhmmPattern.MatchString(value) {
		return time.Time{}, fieldErr(field, "must be HH:MM (24h), got %q", value)
	}
	t, err := time.Parse("15:04", value)
	if err != nil {
		return time.Time{}, fieldErr(field, "invalid time %q: %v", value, err)
	}
	return t, nil
}

func fieldErr(field, format string, args ...any) error {
	return &errs.ConfigError{Field: field, Err: fmt.Errorf(format, args...)}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// normalizeScripName compares pSymbolName values case- and space-insensitively.
func normalizeScripName(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), ""))
}
