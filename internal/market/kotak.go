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
	"time"

	"github.com/akshaykhairmode/scalper/internal/logging"
)

const (
	DefaultBaseURL = "https://mis.kotaksecurities.com"
	quotesPath     = "/script-details/1.0/quotes/neosymbol/"
	maxQuoteBytes  = 4 << 20
)

type QuoteInstrument struct {
	ExchangeSegment string
	Symbol          string
}

type Quote struct {
	ExchangeToken string
	DisplaySymbol string
	Exchange      string
	UpdatedAt     int64
	LTP           float64
	PreviousClose float64
	Change        float64
	ChangePct     float64
	Volume        int64
	OI            int64
	Bid           float64
	Ask           float64
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type QuotesClient struct {
	token string
	http  HTTPDoer
	log   *slog.Logger
}

func NewQuotesClient(token string, log *slog.Logger) *QuotesClient {
	if log == nil {
		log = slog.Default()
	}
	return &QuotesClient{
		token: strings.TrimSpace(token),
		http:  &http.Client{Timeout: 15 * time.Second},
		log:   log,
	}
}

func (c *QuotesClient) Fetch(ctx context.Context, baseURL string, instruments []QuoteInstrument) (map[string]Quote, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("Kotak quotes client is not configured")
	}
	if c.token == "" {
		return nil, errors.New("KOTAK_ACCESS_TOKEN is required for live quotes")
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
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logExchange(req, nil, nil, started, err)
		return nil, fmt.Errorf("send Kotak quotes request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxQuoteBytes+1))
	if err != nil {
		c.logExchange(req, resp, nil, started, err)
		return nil, fmt.Errorf("read Kotak quotes response: %w", err)
	}
	if len(raw) > maxQuoteBytes {
		err := fmt.Errorf("Kotak quotes response exceeds %d bytes", maxQuoteBytes)
		c.logExchange(req, resp, nil, started, err)
		return nil, err
	}
	c.logExchange(req, resp, raw, started, nil)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Kotak quotes request failed (HTTP %d)", resp.StatusCode)
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

func quoteEndpoint(baseURL string, instruments []QuoteInstrument) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("Kotak quotes base URL is invalid")
	}
	host := strings.ToLower(base.Hostname())
	secureKotakHost := host == "kotaksecurities.com" || strings.HasSuffix(host, ".kotaksecurities.com")
	localTestHost := (host == "localhost" || host == "127.0.0.1") && base.Scheme == "http"
	if (base.Scheme != "https" && !localTestHost) || (!secureKotakHost && !localTestHost) {
		return "", errors.New("Kotak quotes base URL is not trusted")
	}
	queries := make([]string, 0, len(instruments))
	for _, item := range instruments {
		segment := strings.TrimSpace(item.ExchangeSegment)
		symbol := strings.TrimSpace(item.Symbol)
		if segment == "" || symbol == "" || strings.ContainsAny(segment+symbol, "\r\n") {
			return "", errors.New("Kotak quotes instrument has an empty or invalid segment/symbol")
		}
		queries = append(queries, url.PathEscape(segment+"|"+symbol))
	}
	return strings.TrimRight(base.String(), "/") + quotesPath + strings.Join(queries, ",") + "/all", nil
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
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(payload, &rows); err != nil {
		return nil, fmt.Errorf("quotes data is not an array: %w", err)
	}
	quotes := make([]Quote, 0, len(rows))
	for i, row := range rows {
		quote, err := decodeQuote(row)
		if err != nil {
			return nil, fmt.Errorf("quote %d: %w", i, err)
		}
		quotes = append(quotes, quote)
	}
	return quotes, nil
}

func decodeQuote(row map[string]json.RawMessage) (Quote, error) {
	var quote Quote
	quote.ExchangeToken = stringField(row, "exchange_token", "p_symbol", "pSymbol")
	quote.DisplaySymbol = stringField(row, "display_symbol", "symbol")
	quote.Exchange = stringField(row, "exchange", "exchange_segment")
	quote.UpdatedAt = intField(row, "lstup_time", "timestamp")
	quote.LTP, _ = floatField(row, "ltp", "last_price")
	quote.Change, _ = floatField(row, "change")
	quote.ChangePct, _ = floatField(row, "per_change", "change_pct")
	quote.Volume = intField(row, "last_volume", "volume")
	quote.OI = intField(row, "oi", "open_interest")

	var ohlc map[string]json.RawMessage
	if raw := row["ohlc"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &ohlc); err != nil {
			return Quote{}, fmt.Errorf("decode OHLC: %w", err)
		}
		quote.PreviousClose, _ = floatField(ohlc, "close", "previous_close")
	}
	if quote.PreviousClose == 0 {
		quote.PreviousClose, _ = floatField(row, "close", "previous_close")
	}

	var depth map[string][]map[string]json.RawMessage
	if raw := row["depth"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &depth); err != nil {
			return Quote{}, fmt.Errorf("decode depth: %w", err)
		}
		if len(depth["buy"]) > 0 {
			quote.Bid, _ = floatField(depth["buy"][0], "price")
		}
		if len(depth["sell"]) > 0 {
			quote.Ask, _ = floatField(depth["sell"][0], "price")
		}
	}
	if math.IsNaN(quote.LTP) || math.IsInf(quote.LTP, 0) || quote.LTP < 0 {
		return Quote{}, errors.New("last traded price is invalid")
	}
	return quote, nil
}

func stringField(row map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw := row[key]
		if len(raw) == 0 {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return strings.TrimSpace(value)
		}
		var number json.Number
		if json.Unmarshal(raw, &number) == nil {
			return number.String()
		}
	}
	return ""
}

func floatField(row map[string]json.RawMessage, keys ...string) (float64, bool) {
	for _, key := range keys {
		raw := row[key]
		if len(raw) == 0 {
			continue
		}
		var value json.Number
		if err := json.Unmarshal(raw, &value); err == nil {
			n, err := value.Float64()
			return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			n, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
			return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
		}
	}
	return 0, false
}

func intField(row map[string]json.RawMessage, keys ...string) int64 {
	n, ok := floatField(row, keys...)
	if !ok || n > math.MaxInt64 || n < math.MinInt64 {
		return 0
	}
	return int64(n)
}

func normalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func (c *QuotesClient) logExchange(req *http.Request, resp *http.Response, body []byte, started time.Time, err error) {
	safeBody := "[non-JSON body omitted]"
	if redacted, redactErr := logging.RedactJSON(body); redactErr == nil {
		safeBody = string(redacted)
	}
	args := []any{
		"event", "kotak_api_exchange",
		"method", req.Method,
		"url", logging.RedactURL(req.URL.String()),
		"request_headers", logging.RedactHeaders(req.Header),
		"request_body", "",
		"duration_ms", time.Since(started).Milliseconds(),
	}
	if resp != nil {
		args = append(args,
			"status", resp.StatusCode,
			"response_headers", logging.RedactHeaders(resp.Header),
			"response_body", safeBody)
	}
	if err != nil {
		args = append(args, "error", err)
		c.log.Error("Kotak quotes API exchange", args...)
		return
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		c.log.Error("Kotak quotes API exchange", args...)
		return
	}
	c.log.Info("Kotak quotes API exchange", args...)
}
