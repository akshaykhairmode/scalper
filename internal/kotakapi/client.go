// Package kotakapi implements the authenticated Kotak Neo trading REST API.
package kotakapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/kotakhttp"
	"github.com/akshaykhairmode/scalper/internal/logging"
)

const maxResponseBytes = 4 << 20
const maxLoggedResponseBytes = 4 << 10

// Session holds short lived credentials copied from the in-memory login session.
type Session struct{ BaseURL, SID, Token, ConsumerKey string }

// Client calls the documented Kotak Neo order and account endpoints.
type Client struct {
	http *http.Client
	log  *slog.Logger
}

// NewClient creates a Kotak API client with an optional structured logger.
func NewClient(logger ...*slog.Logger) *Client {
	log := slog.Default()
	if len(logger) > 0 && logger[0] != nil {
		log = logger[0]
	}
	client := kotakhttp.NewClient(15 * time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		http: client,
		log:  log,
	}
}

// Order contains fields accepted by Kotak's place and modify order endpoints.
type Order struct {
	Tag             string  `json:"tag,omitempty"`
	ExchangeSegment string  `json:"exchange_segment"`
	TradingSymbol   string  `json:"trading_symbol"`
	TransactionType string  `json:"transaction_type"`
	Product         string  `json:"product"`
	OrderType       string  `json:"order_type"`
	Quantity        int64   `json:"quantity"`
	Price           float64 `json:"price"`
	TriggerPrice    float64 `json:"trigger_price"`
	Validity        string  `json:"validity"`
}

type responseEnvelope struct {
	Stat   string          `json:"stat"`
	Emsg   string          `json:"emsg"`
	ErrMsg string          `json:"errMsg"`
	Desc   string          `json:"desc"`
	StCode json.RawMessage `json:"stCode"`
}

// Orders fetches the authenticated session's order book.
func (c *Client) Orders(ctx context.Context, s Session) (json.RawMessage, error) {
	return c.get(ctx, s, "quick/user/orders")
}

// Order fetches one order by its broker order ID.
func (c *Client) Order(ctx context.Context, s Session, id string) (json.RawMessage, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\?#") {
		return nil, errors.New("invalid order id")
	}
	return c.get(ctx, s, "quick/user/orders/"+url.PathEscape(id))
}

// Trades fetches the authenticated session's trade book.
func (c *Client) Trades(ctx context.Context, s Session) (json.RawMessage, error) {
	return c.get(ctx, s, "quick/user/trades")
}

// Positions fetches the authenticated session's positions.
func (c *Client) Positions(ctx context.Context, s Session) (json.RawMessage, error) {
	return c.get(ctx, s, "portfolio/v2/positions")
}

// Holdings fetches the authenticated session's holdings.
func (c *Client) Holdings(ctx context.Context, s Session) (json.RawMessage, error) {
	return c.get(ctx, s, "portfolio/v1/holdings")
}

// Limits fetches the authenticated session's available trading limits.
func (c *Client) Limits(ctx context.Context, s Session) (json.RawMessage, error) {
	return c.post(ctx, s, "quick/user/limits", map[string]any{"seg": "ALL", "exch": "ALL", "prod": "ALL"})
}

// OrderHistory fetches status history for one broker order ID.
func (c *Client) OrderHistory(ctx context.Context, s Session, id string) (json.RawMessage, error) {
	return c.post(ctx, s, "quick/order/history", map[string]any{"nOrdNo": strings.TrimSpace(id)})
}

// CancelOrder requests cancellation of one broker order ID.
func (c *Client) CancelOrder(ctx context.Context, s Session, id string) (json.RawMessage, error) {
	return c.post(ctx, s, "quick/order/cancel", map[string]any{"on": strings.TrimSpace(id), "am": "NO"})
}

// PlaceOrder submits a validated order to Kotak.
func (c *Client) PlaceOrder(ctx context.Context, s Session, o Order) (json.RawMessage, error) {
	if err := validateOrder(o); err != nil {
		return nil, err
	}
	form := map[string]any{"am": "NO", "dq": "0", "es": o.ExchangeSegment, "mp": "0", "pc": o.Product, "pr": formatPrice(o.Price), "pt": orderType(o.OrderType), "qt": fmt.Sprint(o.Quantity), "rt": validity(o.Validity), "tp": formatPrice(o.TriggerPrice), "ts": o.TradingSymbol, "tt": strings.ToUpper(o.TransactionType), "os": "NEOTRADEAPI"}
	if strings.TrimSpace(o.Tag) != "" {
		form["ig"] = o.Tag
	}
	return c.post(ctx, s, "quick/order/rule/ms/place", form)
}

// ModifyOrder updates a validated existing order.
func (c *Client) ModifyOrder(ctx context.Context, s Session, id string, o Order) (json.RawMessage, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("order id is required")
	}
	if err := validateModification(o); err != nil {
		return nil, err
	}
	form := map[string]any{"mp": "0", "dq": "0", "vd": validity(o.Validity), "pr": formatPrice(o.Price), "pt": orderType(o.OrderType), "am": "NO", "tp": formatPrice(o.TriggerPrice), "qt": fmt.Sprint(o.Quantity), "no": strings.TrimSpace(id), "os": "NEOTRADEAPI"}
	return c.post(ctx, s, "quick/order/vr/modify", form)
}

func validateOrder(o Order) error {
	if strings.TrimSpace(o.ExchangeSegment) == "" || strings.TrimSpace(o.TradingSymbol) == "" {
		return errors.New("exchange segment and trading symbol are required")
	}
	if o.Quantity <= 0 {
		return errors.New("quantity must be positive")
	}
	switch strings.ToUpper(o.TransactionType) {
	case "B", "S", "BUY", "SELL":
	default:
		return errors.New("transaction_type must be B or S")
	}
	switch strings.ToUpper(o.OrderType) {
	case "MARKET", "MKT", "LIMIT", "L", "SL", "SL-M", "SLM":
	default:
		return errors.New("unsupported order_type")
	}
	switch strings.ToUpper(o.Product) {
	case "MIS", "NRML", "CNC", "MTF":
	default:
		return errors.New("unsupported product")
	}
	if o.Price < 0 || o.TriggerPrice < 0 {
		return errors.New("order prices cannot be negative")
	}
	return nil
}
func validateModification(o Order) error {
	if o.Quantity <= 0 {
		return errors.New("quantity must be positive")
	}
	switch strings.ToUpper(o.OrderType) {
	case "MARKET", "MKT", "LIMIT", "L", "SL", "SL-M", "SLM":
	default:
		return errors.New("unsupported order_type")
	}
	if o.Price < 0 || o.TriggerPrice < 0 {
		return errors.New("order prices cannot be negative")
	}
	return nil
}
func orderType(v string) string {
	switch strings.ToUpper(v) {
	case "MARKET", "MKT":
		return "MKT"
	case "LIMIT", "L":
		return "L"
	case "SL":
		return "SL"
	case "SL-M", "SLM":
		return "SL-M"
	}
	return v
}
func validity(v string) string {
	if strings.TrimSpace(v) == "" {
		return "DAY"
	}
	return strings.ToUpper(v)
}
func formatPrice(v float64) string { return fmt.Sprintf("%.2f", v) }

func (c *Client) get(ctx context.Context, s Session, path string) (json.RawMessage, error) {
	// Every Kotak request is throttled by the shared kotakhttp limiter, so no
	// per-client read spacing is needed here.
	return c.request(ctx, s, http.MethodGet, path, nil)
}

func (c *Client) post(ctx context.Context, s Session, path string, form map[string]any) (json.RawMessage, error) {
	return c.request(ctx, s, http.MethodPost, path, form)
}
func (c *Client) request(ctx context.Context, s Session, method, path string, form map[string]any) (json.RawMessage, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("kotak API client is not configured")
	}
	if s.SID == "" || s.Token == "" {
		return nil, errors.New("authenticated Kotak trading session is required")
	}
	base, err := url.Parse(strings.TrimRight(s.BaseURL, "/") + "/")
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil {
		return nil, errors.New("invalid Kotak API base URL")
	}
	host := strings.ToLower(base.Hostname())
	if host != "kotaksecurities.com" && !strings.HasSuffix(host, ".kotaksecurities.com") {
		return nil, errors.New("untrusted Kotak API base URL")
	}
	rel, err := url.Parse(strings.TrimLeft(path, "/"))
	if err != nil {
		return nil, err
	}
	target := base.ResolveReference(rel)
	var bodyBytes []byte
	if form != nil {
		payload, err := json.Marshal(form)
		if err != nil {
			return nil, fmt.Errorf("encode Kotak API request: %w", err)
		}
		bodyBytes = []byte(url.Values{"jData": {string(payload)}}.Encode())
	}
	var req *http.Request
	var resp *http.Response
	var raw []byte
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if bodyBytes != nil {
			body = bytes.NewReader(bodyBytes)
		}
		req, err = http.NewRequestWithContext(ctx, method, target.String(), body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Sid", s.SID)
		req.Header.Set("Auth", s.Token)
		// Kotak's trading APIs require these headers for report endpoints too,
		// including GET requests. The official SDK uses this key for API routing.
		req.Header.Set("neo-fin-key", "neotradeapi")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if path == "quick/order/rule/ms/place" || path == "quick/order/cancel" || path == "quick/order/vr/modify" {
			if s.ConsumerKey == "" {
				return nil, errors.New("KOTAK_CONSUMER_KEY is required for order writes")
			}
			req.Header.Set("Authorization", s.ConsumerKey)
		}
		resp, err = c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("kotak API request failed: %w", err)
		}
		raw, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read Kotak API response: %w", err)
		}
		if len(raw) > maxResponseBytes {
			return nil, errors.New("kotak API response exceeded size limit")
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 1 {
			c.logResponse(req, resp, raw, "rate_limited")
			if err := sleepContext(ctx, retryAfter(resp, attempt)); err != nil {
				return nil, err
			}
			continue
		}
		break
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.logResponse(req, resp, raw, "http_error")
		return nil, kotakResponseError(resp.StatusCode, raw)
	}
	if !json.Valid(raw) {
		return nil, errors.New("kotak API returned invalid JSON")
	}
	var envelope responseEnvelope
	if json.Unmarshal(raw, &envelope) == nil && strings.EqualFold(envelope.Stat, "Not_Ok") {
		if isEmptyReport(path, envelope) {
			c.logResponse(req, resp, raw, "no_data")
			return json.RawMessage(`{"stat":"Ok","data":[]}`), nil
		}
		c.logResponse(req, resp, raw, "api_rejected")
		return nil, kotakAPIError(envelope)
	}
	return json.RawMessage(raw), nil
}

// retryAfter returns how long to wait before retrying a rate-limited request.
// One retry only: retrying inside the same second multiplies the burst that
// triggered the 429 in the first place.
func retryAfter(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds > 0 && seconds <= 5 {
			return time.Duration(seconds) * time.Second
		}
	}
	jitter := time.Duration(time.Now().UnixNano()%250) * time.Millisecond
	return 750*time.Millisecond + time.Duration(attempt)*500*time.Millisecond + jitter
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isEmptyReport(path string, envelope responseEnvelope) bool {
	if path != "quick/user/orders" && path != "quick/user/trades" {
		return false
	}
	var code int
	if json.Unmarshal(envelope.StCode, &code) != nil {
		var text string
		if json.Unmarshal(envelope.StCode, &text) == nil {
			_, _ = fmt.Sscan(text, &code)
		}
	}
	message := strings.ToLower(strings.TrimSpace(envelope.ErrMsg + " " + envelope.Emsg + " " + envelope.Desc))
	return code == 5203 || strings.Contains(message, "no data") || strings.Contains(message, "data not found")
}

func kotakAPIError(envelope responseEnvelope) error {
	message := strings.TrimSpace(envelope.Emsg)
	if message == "" {
		message = strings.TrimSpace(envelope.ErrMsg)
	}
	if desc := strings.TrimSpace(envelope.Desc); desc != "" && !strings.EqualFold(desc, message) {
		if message == "" {
			message = desc
		} else {
			message += " (" + desc + ")"
		}
	}
	var code any
	if len(envelope.StCode) > 0 {
		if json.Unmarshal(envelope.StCode, &code) != nil {
			code = string(envelope.StCode)
		}
	}
	if message == "" && code != nil {
		return fmt.Errorf("kotak API rejected the request (code %v)", code)
	}
	if code != nil {
		return fmt.Errorf("kotak API rejected the request: %s (code %v)", message, code)
	}
	if message != "" {
		return fmt.Errorf("kotak API rejected the request: %s", message)
	}
	return errors.New("kotak API rejected the request")
}

func (c *Client) logResponse(req *http.Request, resp *http.Response, body []byte, outcome string) {
	if c == nil || c.log == nil || req == nil || resp == nil {
		return
	}
	safeBody := "[non-JSON response omitted]"
	if redacted, err := logging.RedactJSON(body); err == nil {
		safeBody = string(redacted)
		if len(safeBody) > maxLoggedResponseBytes {
			safeBody = safeBody[:maxLoggedResponseBytes] + "…[truncated]"
		}
	}
	log := c.log.Error
	if outcome == "no_data" {
		log = c.log.Info
	}
	log("Kotak trading API response",
		"event", "kotak_trading_api_response",
		"method", req.Method,
		"url", logging.RedactURL(req.URL.String()),
		"status", resp.StatusCode,
		"outcome", outcome,
		"response_headers", logging.RedactHeaders(resp.Header),
		"response_body", safeBody,
	)
}

func kotakResponseError(status int, raw []byte) error {
	var envelope struct {
		Emsg string `json:"emsg"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Emsg != "" {
		return fmt.Errorf("kotak API request failed (HTTP %d): %s", status, envelope.Emsg)
	}
	return fmt.Errorf("kotak API request failed (HTTP %d)", status)
}
