package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/kotakhttp"
	"github.com/akshaykhairmode/scalper/internal/logging"
)

const (
	// DefaultBaseURL is the Kotak REST API base URL.
	DefaultBaseURL = "https://mis.kotaksecurities.com"
	quotesPath     = "/script-details/1.0/quotes/neosymbol/"
	historicalPath = "/market-data/1.0/historical/details"
	maxQuoteBytes  = 4 << 20
	maxCandleBytes = 16 << 20
)

// QuoteInstrument identifies an instrument for a Kotak quote request.
type QuoteInstrument struct {
	ExchangeSegment string
	Symbol          string
}

// DepthLevel represents a single bid or ask level in market depth.
type DepthLevel struct {
	Price    float64 `json:"price"`
	Quantity int64   `json:"quantity"`
	Orders   int64   `json:"orders"`
}

// Quote contains normalized quote data returned by Kotak.
type Quote struct {
	ExchangeToken string
	DisplaySymbol string
	Exchange      string
	UpdatedAt     int64
	LTP           float64
	PreviousClose float64
	Open          float64
	High          float64
	Low           float64
	AvgPrice      float64
	Change        float64
	ChangePct     float64
	Volume        int64
	OI            int64
	Bid           float64
	Ask           float64
	Bids          []DepthLevel
	Asks          []DepthLevel
}

// HTTPDoer is the HTTP client contract used by QuotesClient.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// QuotesClient fetches quotes from the Kotak REST API.
type QuotesClient struct {
	consumerKey string
	http        HTTPDoer
	log         *slog.Logger
	rateMu      sync.Mutex
	nextRequest time.Time
	histRateMu  sync.Mutex
	nextHistReq time.Time
}

// HTTPStatusError preserves upstream status and retry advice for API handlers.
type HTTPStatusError struct {
	StatusCode int
	RetryAfter string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("kotak historical data request failed (HTTP %d)", e.StatusCode)
}

// NewQuotesClient creates a Kotak REST quotes client.
func NewQuotesClient(consumerKey string, log *slog.Logger) *QuotesClient {
	if log == nil {
		log = slog.Default()
	}
	return &QuotesClient{
		consumerKey: strings.TrimSpace(consumerKey),
		http:        kotakhttp.NewClient(15 * time.Second),
		log:         log,
	}
}

// Fetch retrieves quotes for the requested instruments.
func (c *QuotesClient) Fetch(ctx context.Context, baseURL string, instruments []QuoteInstrument) (map[string]Quote, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("kotak quotes client is not configured")
	}
	if c.consumerKey == "" {
		return nil, errors.New("KOTAK_CONSUMER_KEY is required for live quotes")
	}
	if len(instruments) == 0 {
		return map[string]Quote{}, nil
	}
	endpoint, err := quoteEndpoint(baseURL, instruments)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Kotak quotes request: %w", err)
	}
	req.Header.Set("Authorization", c.consumerKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	if err := c.waitRate(ctx); err != nil {
		return nil, fmt.Errorf("wait to send Kotak quotes request: %w", err)
	}
	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logExchange(req, nil, nil, started, err)
		return nil, fmt.Errorf("send Kotak quotes request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.Warn("close Kotak quotes response body", "event", "quotes_response_close_failed", "error", err)
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxQuoteBytes+1))
	if err != nil {
		c.logExchange(req, resp, nil, started, err)
		return nil, fmt.Errorf("read Kotak quotes response: %w", err)
	}
	if len(raw) > maxQuoteBytes {
		err := fmt.Errorf("kotak quotes response exceeds %d bytes", maxQuoteBytes)
		c.logExchange(req, resp, nil, started, err)
		return nil, err
	}
	c.logExchange(req, resp, raw, started, nil)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("kotak quotes request failed (HTTP %d)", resp.StatusCode)
	}

	quotes, err := decodeQuotes(raw)
	if err != nil {
		return nil, fmt.Errorf("decode Kotak quotes response: %w", err)
	}
	index := make(map[string]Quote, len(quotes)*3)
	for _, quote := range quotes {
		for _, alias := range []string{quote.ExchangeToken, quote.DisplaySymbol} {
			alias = normalizeSymbol(alias)
			if alias != "" {
				index[alias] = quote
			}
		}
	}
	return index, nil
}

// Historical fetches candles for one instrument. Kotak identifies historical
// instruments by exchange segment and the numeric Scrip Master pSymbol.
func (c *QuotesClient) Historical(
	ctx context.Context,
	baseURL, neosymbol, interval, fromDate, toDate string,
) ([]Candle, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("kotak historical data client is not configured")
	}
	if c.consumerKey == "" {
		return nil, errors.New("KOTAK_CONSUMER_KEY is required for historical candles")
	}
	endpoint, err := historicalEndpoint(baseURL, neosymbol, interval, fromDate, toDate)
	if err != nil {
		return nil, err
	}

	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("create Kotak historical data request: %w", err)
		}
		req.Header.Set("Authorization", c.consumerKey)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		if err := c.waitHistoricalRate(ctx); err != nil {
			return nil, fmt.Errorf("wait to send Kotak historical request: %w", err)
		}
		started := time.Now()
		resp, err := c.http.Do(req)
		if err != nil {
			c.logExchange(req, nil, nil, started, err)
			return nil, fmt.Errorf("send Kotak historical data request: %w", err)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCandleBytes+1))
		if cerr := resp.Body.Close(); cerr != nil && c.log != nil {
			c.log.Debug("failed to close Kotak historical response body", "error", cerr)
		}
		if err != nil {
			c.logExchange(req, resp, nil, started, err)
			return nil, fmt.Errorf("read Kotak historical data response: %w", err)
		}
		if len(raw) > maxCandleBytes {
			err := fmt.Errorf("kotak historical response exceeds %d bytes", maxCandleBytes)
			c.logExchange(req, resp, nil, started, err)
			return nil, err
		}
		c.logExchange(req, resp, nil, started, nil)
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts {
			retryDelay := time.Second
			if retryHeader := resp.Header.Get("Retry-After"); retryHeader != "" {
				if seconds, parseErr := strconv.Atoi(retryHeader); parseErr == nil && seconds > 0 {
					retryDelay = time.Duration(seconds) * time.Second
				}
			}
			select {
			case <-time.After(retryDelay):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, &HTTPStatusError{StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
		}
		candles, err := decodeHistoricalCandles(raw)
		if err != nil {
			return nil, fmt.Errorf("decode Kotak historical data response: %w", err)
		}
		return candles, nil
	}
	return nil, errors.New("kotak historical data request exceeded retry attempts")
}

// waitHistoricalRate spaces historical REST requests to avoid 429 rate limit triggers.
func (c *QuotesClient) waitHistoricalRate(ctx context.Context) error {
	const spacing = 1000 * time.Millisecond
	c.histRateMu.Lock()
	now := time.Now()
	wait := c.nextHistReq.Sub(now)
	if wait < 0 {
		wait = 0
	}
	c.nextHistReq = now.Add(wait + spacing)
	c.histRateMu.Unlock()
	if wait == 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// waitRate spaces market-data REST requests to stay below Kotak's documented
// shared request-per-second ceiling while still honoring caller cancellation.
func (c *QuotesClient) waitRate(ctx context.Context) error {
	const spacing = 125 * time.Millisecond
	c.rateMu.Lock()
	now := time.Now()
	wait := c.nextRequest.Sub(now)
	if wait < 0 {
		wait = 0
	}
	c.nextRequest = now.Add(wait + spacing)
	c.rateMu.Unlock()
	if wait == 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func quoteEndpoint(baseURL string, instruments []QuoteInstrument) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("kotak quotes base URL is invalid")
	}
	host := strings.ToLower(base.Hostname())
	secureKotakHost := host == "kotaksecurities.com" || strings.HasSuffix(host, ".kotaksecurities.com")
	localTestHost := (host == "localhost" || host == "127.0.0.1") && base.Scheme == "http"
	if (base.Scheme != "https" && !localTestHost) || (!secureKotakHost && !localTestHost) {
		return "", errors.New("kotak quotes base URL is not trusted")
	}
	queries := make([]string, 0, len(instruments))
	for _, item := range instruments {
		segment := strings.TrimSpace(item.ExchangeSegment)
		symbol := strings.TrimSpace(item.Symbol)
		if segment == "" || symbol == "" || strings.ContainsAny(segment+symbol, "\r\n") {
			return "", errors.New("kotak quotes instrument has an empty or invalid segment/symbol")
		}
		escaped := url.PathEscape(segment + "|" + symbol)
		escaped = strings.ReplaceAll(escaped, "%7C", "|")
		queries = append(queries, escaped)
	}
	return strings.TrimRight(base.String(), "/") + quotesPath + strings.Join(queries, ",") + "/all", nil
}

func historicalEndpoint(baseURL, neosymbol, interval, fromDate, toDate string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("kotak historical data base URL is invalid")
	}
	host := strings.ToLower(base.Hostname())
	secureKotakHost := host == "kotaksecurities.com" || strings.HasSuffix(host, ".kotaksecurities.com")
	localTestHost := (host == "localhost" || host == "127.0.0.1") && base.Scheme == "http"
	if (base.Scheme != "https" && !localTestHost) || (!secureKotakHost && !localTestHost) {
		return "", errors.New("kotak historical data base URL is not trusted")
	}
	neosymbol = strings.TrimSpace(neosymbol)
	if neosymbol == "" || strings.Count(neosymbol, "|") != 1 || strings.ContainsAny(neosymbol, "\r\n") {
		return "", errors.New("kotak historical instrument is invalid")
	}
	if _, ok := historicalIntervals[interval]; !ok {
		return "", fmt.Errorf("kotak historical interval %q is unsupported", interval)
	}
	if _, err := time.Parse("2006-01-02", fromDate); err != nil {
		return "", errors.New("kotak historical from_date must use YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", toDate); err != nil {
		return "", errors.New("kotak historical to_date must use YYYY-MM-DD")
	}
	if fromDate > toDate {
		return "", errors.New("kotak historical from_date is after to_date")
	}
	query := url.Values{
		"neosymbol": {neosymbol},
		"interval":  {interval},
		"fromdate":  {fromDate},
		"todate":    {toDate},
	}
	return strings.TrimRight(base.String(), "/") + historicalPath + "?" + query.Encode(), nil
}

var historicalIntervals = map[string]bool{
	"1min": true, "3min": true, "5min": true, "10min": true,
	"15min": true, "30min": true, "60min": true, "D": true, "W": true,
}

func decodeHistoricalCandles(raw []byte) ([]Candle, error) {
	var response struct {
		Status string `json:"status"`
		Fault  *struct {
			Message string `json:"message"`
		} `json:"fault"`
		Data *struct {
			Candles json.RawMessage `json:"candles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Status != "" && !strings.EqualFold(response.Status, "success") {
		if response.Fault != nil && strings.TrimSpace(response.Fault.Message) != "" {
			return nil, fmt.Errorf("kotak returned status %q: %s", response.Status, response.Fault.Message)
		}
		return nil, fmt.Errorf("kotak returned status %q", response.Status)
	}
	if response.Data == nil {
		return nil, errors.New("historical response has no data")
	}
	rawCandles := strings.TrimSpace(string(response.Data.Candles))
	if rawCandles == "" || rawCandles == "null" {
		return nil, errors.New("historical response has no candles array")
	}
	var rows [][]json.RawMessage
	if err := json.Unmarshal(response.Data.Candles, &rows); err != nil {
		return nil, fmt.Errorf("historical candles are not an array: %w", err)
	}
	if rows == nil {
		return nil, errors.New("historical response has no candles array")
	}
	candles := make([]Candle, 0, len(rows))
	for _, row := range rows {
		candle, err := decodeHistoricalCandle(row)
		if err != nil {
			// Do not discard hundreds of valid candles if a single upstream bar has bad values
			// (e.g. index glitch where high < open or rounding anomaly). Clamp if fixable or skip.
			continue
		}
		candles = append(candles, candle)
	}
	if len(candles) == 0 && len(rows) > 0 {
		return nil, errors.New("all historical candles were malformed")
	}
	return candles, nil
}

func decodeHistoricalCandle(row []json.RawMessage) (Candle, error) {
	if len(row) < 6 || len(row) > 7 {
		return Candle{}, fmt.Errorf("got %d values, want 6 or 7", len(row))
	}
	var timestamp string
	if err := json.Unmarshal(row[0], &timestamp); err != nil {
		return Candle{}, fmt.Errorf("timestamp: %w", err)
	}
	at, err := parseCandleTimestamp(timestamp)
	if err != nil {
		return Candle{}, fmt.Errorf("timestamp: %w", err)
	}
	values := make([]float64, 4)
	for i := range values {
		values[i], err = decodeCandleNumber(row[i+1])
		if err != nil {
			return Candle{}, fmt.Errorf("value %d: %w", i+1, err)
		}
	}
	volume, err := decodeCandleNumber(row[5])
	if err != nil || volume < 0 || volume > math.MaxInt64 || math.Trunc(volume) != volume {
		return Candle{}, errors.New("volume is not a non-negative integer")
	}
	candle := Candle{
		Time:   at.Unix(),
		Open:   values[0],
		High:   values[1],
		Low:    values[2],
		Close:  values[3],
		Volume: int64(volume),
	}
	if len(row) == 7 && strings.TrimSpace(string(row[6])) != "null" {
		oi, err := decodeCandleNumber(row[6])
		if err != nil || oi < 0 || oi > math.MaxInt64 || math.Trunc(oi) != oi {
			return Candle{}, errors.New("open interest is not a non-negative integer")
		}
		candle.OpenInterest = int64(oi)
	}
	if candle.Open <= 0 && candle.High <= 0 && candle.Low <= 0 && candle.Close <= 0 {
		return Candle{}, errors.New("candle prices are all non-positive")
	}
	// Sanitize zero or non-positive prices from upstream broker glitch
	if candle.Low <= 0 {
		pos := []float64{}
		if candle.Open > 0 {
			pos = append(pos, candle.Open)
		}
		if candle.Close > 0 {
			pos = append(pos, candle.Close)
		}
		if candle.High > 0 {
			pos = append(pos, candle.High)
		}
		if len(pos) > 0 {
			minVal := pos[0]
			for _, v := range pos {
				if v < minVal {
					minVal = v
				}
			}
			candle.Low = minVal
		}
	}
	if candle.Open <= 0 {
		candle.Open = candle.Low
	}
	if candle.Close <= 0 {
		candle.Close = candle.Open
	}
	if candle.High <= 0 || candle.High < math.Max(candle.Open, math.Max(candle.Close, candle.Low)) {
		candle.High = math.Max(candle.Open, math.Max(candle.Close, candle.Low))
	}
	if candle.Low > candle.High {
		candle.Low, candle.High = candle.High, candle.Low
	}
	if candle.Open < candle.Low {
		candle.Low = candle.Open
	}
	if candle.Open > candle.High {
		candle.High = candle.Open
	}
	if candle.Close < candle.Low {
		candle.Low = candle.Close
	}
	if candle.Close > candle.High {
		candle.High = candle.Close
	}
	return candle, nil
}

func parseCandleTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at, nil
		}
	}
	return time.Time{}, errors.New("unsupported ISO 8601 timestamp")
}

func decodeCandleNumber(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return 0, errors.New("missing numeric value")
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			return value, nil
		}
		return 0, errors.New("numeric value is not finite")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, errors.New("value is not a number")
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("value is not a finite number")
	}
	return value, nil
}

type rawKotakDepthLevel struct {
	Price      json.RawMessage `json:"price"`
	Quantity   json.RawMessage `json:"quantity"`
	Qty        json.RawMessage `json:"qty"`
	Orders     json.RawMessage `json:"orders"`
	NoOfOrders json.RawMessage `json:"no_of_orders"`
}

type rawKotakQuoteRow struct {
	ExchangeToken     json.RawMessage `json:"exchange_token"`
	PSymbol           json.RawMessage `json:"p_symbol"`
	PSymbolCamel      json.RawMessage `json:"pSymbol"`
	DisplaySymbol     json.RawMessage `json:"display_symbol"`
	Symbol            json.RawMessage `json:"symbol"`
	Exchange          json.RawMessage `json:"exchange"`
	ExchangeSegment   json.RawMessage `json:"exchange_segment"`
	LstupTime         json.RawMessage `json:"lstup_time"`
	Timestamp         json.RawMessage `json:"timestamp"`
	LTP               json.RawMessage `json:"ltp"`
	LastPrice         json.RawMessage `json:"last_price"`
	Change            json.RawMessage `json:"change"`
	PerChange         json.RawMessage `json:"per_change"`
	ChangePct         json.RawMessage `json:"change_pct"`
	LastVolume        json.RawMessage `json:"last_volume"`
	Volume            json.RawMessage `json:"volume"`
	OI                json.RawMessage `json:"oi"`
	OpenInterest      json.RawMessage `json:"open_interest"`
	OpenInterestCamel json.RawMessage `json:"openInterest"`
	OpenInt           json.RawMessage `json:"open_int"`
	AvgCost           json.RawMessage `json:"avg_cost"`
	Open              json.RawMessage `json:"open"`
	High              json.RawMessage `json:"high"`
	Low               json.RawMessage `json:"low"`
	Close             json.RawMessage `json:"close"`
	PreviousClose     json.RawMessage `json:"previous_close"`
	OHLC              *struct {
		Open          json.RawMessage `json:"open"`
		High          json.RawMessage `json:"high"`
		Low           json.RawMessage `json:"low"`
		Close         json.RawMessage `json:"close"`
		PreviousClose json.RawMessage `json:"previous_close"`
	} `json:"ohlc"`
	Depth *struct {
		Buy  []rawKotakDepthLevel `json:"buy"`
		Sell []rawKotakDepthLevel `json:"sell"`
	} `json:"depth"`
}

func parseRawFloat(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	s := strings.TrimSpace(string(raw))
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if s == "" || s == "null" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func parseRawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	s := strings.TrimSpace(string(raw))
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	if s == "null" {
		return ""
	}
	return s
}

func firstRawString(fields ...json.RawMessage) string {
	for _, f := range fields {
		if s := parseRawString(f); s != "" {
			return s
		}
	}
	return ""
}

func firstRawFloat(fields ...json.RawMessage) (float64, bool) {
	for _, f := range fields {
		if val, ok := parseRawFloat(f); ok {
			return val, true
		}
	}
	return 0, false
}

func firstRawInt(fields ...json.RawMessage) int64 {
	val, _ := firstRawFloat(fields...)
	if val > math.MaxInt64 || val < math.MinInt64 {
		return 0
	}
	return int64(val)
}

func decodeQuotes(raw []byte) ([]Quote, error) {
	var payload json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if len(payload) == 0 || string(payload) == "null" {
		return nil, errors.New("empty quotes response")
	}
	if payload[0] == '{' {
		var wrapper struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(payload, &wrapper); err != nil {
			return nil, err
		}
		payload = wrapper.Data
	}
	var rows []rawKotakQuoteRow
	if err := json.Unmarshal(payload, &rows); err != nil {
		return nil, fmt.Errorf("quotes data is not an array: %w", err)
	}
	quotes := make([]Quote, 0, len(rows))
	for i := range rows {
		quote, err := decodeRawQuoteRow(&rows[i])
		if err != nil {
			return nil, fmt.Errorf("quote %d: %w", i, err)
		}
		quotes = append(quotes, quote)
	}
	return quotes, nil
}

func decodeRawQuoteRow(row *rawKotakQuoteRow) (Quote, error) {
	var quote Quote
	quote.ExchangeToken = firstRawString(row.ExchangeToken, row.PSymbol, row.PSymbolCamel)
	quote.DisplaySymbol = firstRawString(row.DisplaySymbol, row.Symbol)
	quote.Exchange = firstRawString(row.Exchange, row.ExchangeSegment)
	quote.UpdatedAt = firstRawInt(row.LstupTime, row.Timestamp)
	if len(row.LTP) > 0 || len(row.LastPrice) > 0 {
		ltp, ok := firstRawFloat(row.LTP, row.LastPrice)
		if !ok {
			return Quote{}, errors.New("last traded price is invalid")
		}
		quote.LTP = ltp
	}
	quote.Change, _ = firstRawFloat(row.Change)
	quote.ChangePct, _ = firstRawFloat(row.PerChange, row.ChangePct)
	quote.Volume = firstRawInt(row.LastVolume, row.Volume)
	quote.OI = firstRawInt(row.OI, row.OpenInterest, row.OpenInterestCamel, row.OpenInt)
	quote.AvgPrice, _ = firstRawFloat(row.AvgCost)
	quote.Open, _ = firstRawFloat(row.Open)
	quote.High, _ = firstRawFloat(row.High)
	quote.Low, _ = firstRawFloat(row.Low)

	if row.OHLC != nil {
		quote.PreviousClose, _ = firstRawFloat(row.OHLC.Close, row.OHLC.PreviousClose)
		if v, ok := firstRawFloat(row.OHLC.Open); ok {
			quote.Open = v
		}
		if v, ok := firstRawFloat(row.OHLC.High); ok {
			quote.High = v
		}
		if v, ok := firstRawFloat(row.OHLC.Low); ok {
			quote.Low = v
		}
	}
	if quote.PreviousClose == 0 {
		quote.PreviousClose, _ = firstRawFloat(row.Close, row.PreviousClose)
	}

	if row.Depth != nil {
		if len(row.Depth.Buy) > 0 {
			quote.Bids = make([]DepthLevel, 0, len(row.Depth.Buy))
			for _, b := range row.Depth.Buy {
				p, _ := parseRawFloat(b.Price)
				q := firstRawInt(b.Quantity, b.Qty)
				o := firstRawInt(b.Orders, b.NoOfOrders)
				if p > 0 || q > 0 {
					quote.Bids = append(quote.Bids, DepthLevel{Price: p, Quantity: q, Orders: o})
				}
			}
		}
		if len(row.Depth.Sell) > 0 {
			quote.Asks = make([]DepthLevel, 0, len(row.Depth.Sell))
			for _, s := range row.Depth.Sell {
				p, _ := parseRawFloat(s.Price)
				q := firstRawInt(s.Quantity, s.Qty)
				o := firstRawInt(s.Orders, s.NoOfOrders)
				if p > 0 || q > 0 {
					quote.Asks = append(quote.Asks, DepthLevel{Price: p, Quantity: q, Orders: o})
				}
			}
		}
		if len(quote.Bids) > 0 {
			quote.Bid = quote.Bids[0].Price
		}
		if len(quote.Asks) > 0 {
			quote.Ask = quote.Asks[0].Price
		}
	}
	if math.IsNaN(quote.LTP) || math.IsInf(quote.LTP, 0) || quote.LTP < 0 {
		return Quote{}, errors.New("last traded price is invalid")
	}
	return quote, nil
}

func normalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func (c *QuotesClient) logExchange(req *http.Request, resp *http.Response, body []byte, started time.Time, err error) {
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	args := []any{
		"event", "kotak_api_exchange",
		"method", req.Method,
		"url", logging.RedactURL(req.URL.String()),
		"status", status,
		"bytes", len(body),
		"duration_ms", time.Since(started).Milliseconds(),
	}

	failed := err != nil || (resp != nil && (status < http.StatusOK || status >= http.StatusMultipleChoices))
	if failed {
		args = append(args, "request_headers", logging.RedactHeaders(req.Header))
		if resp != nil {
			args = append(args, "response_headers", logging.RedactHeaders(resp.Header))
		}
		if len(body) > 0 {
			safeBody := "[non-JSON body omitted]"
			if redacted, redactErr := logging.RedactJSON(body); redactErr == nil {
				safeBody = string(redacted)
			}
			args = append(args, "response_body", safeBody)
		}
		if err != nil {
			args = append(args, "error", err)
		}
		c.log.Error("Kotak market API exchange", args...)
		return
	}

	// Successful calls are high-volume; keep the Info line metadata-only.
	c.log.Info("Kotak market API exchange", args...)
	if !c.log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	args = append(args, "request_headers", logging.RedactHeaders(req.Header))
	if resp != nil {
		args = append(args, "response_headers", logging.RedactHeaders(resp.Header))
	}
	if len(body) > 0 {
		safeBody := "[non-JSON body omitted]"
		if redacted, redactErr := logging.RedactJSON(body); redactErr == nil {
			safeBody = string(redacted)
		}
		args = append(args, "response_body", safeBody)
	}
	c.log.Debug("Kotak market API exchange detail", args...)
}
