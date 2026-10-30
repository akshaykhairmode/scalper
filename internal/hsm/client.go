// Package hsm implements the Kotak SFeed WebSocket client and decoder.
package hsm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// DefaultURL is Kotak's default SFeed endpoint.
	DefaultURL        = "wss://sfeed.kotaksecurities.com/apifeed"
	maxSFeedTokens    = 3000
	sfeedPingInterval = 20 * time.Second
)

//nolint:revive // HSM distinguishes this feed client from other market clients.
type HSMClient struct {
	url     string
	user    string
	auth    string
	logger  *slog.Logger
	quoteCh chan<- *Quote

	mu             sync.RWMutex
	writeMu        sync.Mutex
	subscriptionMu sync.Mutex
	conn           *websocket.Conn
	connected      bool
	dividers       map[int8]int
	tradingSymbols map[string]string
	subs           map[string]struct{}
	ackPending     bool
	pendingQuotes  map[string]*Quote

	stopCh         chan struct{}
	connectedCh    chan struct{}
	connectErrCh   chan error
	doneCh         chan error
	subscribeAckCh chan struct{}
	stopOnce       sync.Once
	connectedOnce  sync.Once
	wg             sync.WaitGroup
}

// NewHSMClientWithLogger creates an SFeed client with an explicit logger.
func NewHSMClientWithLogger(endpoint string, quoteCh chan<- *Quote, logger *slog.Logger) *HSMClient {
	if logger == nil {
		logger = slog.Default()
	}
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultURL
	}
	return &HSMClient{
		url:            endpoint,
		logger:         logger,
		quoteCh:        quoteCh,
		dividers:       make(map[int8]int),
		tradingSymbols: make(map[string]string),
		subs:           make(map[string]struct{}),
		pendingQuotes:  make(map[string]*Quote),
		stopCh:         make(chan struct{}),
		connectedCh:    make(chan struct{}),
		connectErrCh:   make(chan error, 1),
		doneCh:         make(chan error, 1),
		subscribeAckCh: make(chan struct{}, 1),
	}
}

// Connect authenticates the client and starts its read loop.
func (c *HSMClient) Connect(ctx context.Context, user, auth string) error {
	if strings.TrimSpace(user) == "" || strings.TrimSpace(auth) == "" {
		return errors.New("SFeed user and auth values are required")
	}
	if err := validateSFeedURL(c.url); err != nil {
		return err
	}
	c.user, c.auth = user, auth
	c.logger.Info("Kotak SFeed connection attempt", "event", "sfeed_connect_attempt", "url", c.url)
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, response, err := dialer.DialContext(ctx, c.url, http.Header{})
	if response != nil && response.Body != nil {
		defer func() {
			if cerr := response.Body.Close(); cerr != nil && c.logger != nil {
				c.logger.Debug("failed to close SFeed handshake response body", "error", cerr)
			}
		}()
	}
	if err != nil {
		if response != nil {
			c.logger.Warn("Kotak SFeed WebSocket handshake rejected",
				"event", "sfeed_websocket_handshake_failed", "http_status", response.StatusCode)
		}
		return fmt.Errorf("dial Kotak SFeed: %w", err)
	}
	conn.SetReadLimit(1 << 20)
	if err := conn.SetReadDeadline(time.Now().Add(45 * time.Second)); err != nil {
		if cerr := conn.Close(); cerr != nil && c.logger != nil {
			c.logger.Debug("failed to close connection after read deadline error", "error", cerr)
		}
		return fmt.Errorf("set SFeed initial read deadline: %w", err)
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	})
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	authFrame := map[string]any{
		"user":              user,
		"auth":              auth,
		"format":            "native_batch",
		"source":            "NEOTRADEAPI",
		"platform":          "Web",
		"version":           "1.2.3",
		"sdk_version":       2,
		"sdk_date":          "2026-08-07T09:41:17.667Z",
		"conn_req_time":     time.Now().UnixMilli(),
		"sessionValidation": false,
	}
	if err := c.writeJSON(authFrame); err != nil {
		c.closeConnection()
		return fmt.Errorf("send Kotak SFeed authentication: %w", err)
	}

	c.wg.Add(2)
	go c.readLoop()
	go c.pingLoop()
	select {
	case <-c.connectedCh:
		return nil
	case err := <-c.connectErrCh:
		c.Stop()
		return err
	case <-ctx.Done():
		c.Stop()
		return fmt.Errorf("wait for Kotak SFeed authentication: %w", ctx.Err())
	case <-time.After(10 * time.Second):
		c.Stop()
		return errors.New("timed out waiting for Kotak SFeed authentication")
	}
}

func validateSFeedURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("SFeed URL is invalid")
	}
	host := strings.ToLower(u.Hostname())
	trusted := host == "kotaksecurities.com" || strings.HasSuffix(host, ".kotaksecurities.com")
	localTest := u.Scheme == "ws" && (host == "localhost" || host == "127.0.0.1")
	if (u.Scheme != "wss" && !localTest) || (!trusted && !localTest) {
		return errors.New("SFeed URL is not trusted")
	}
	return nil
}

func (c *HSMClient) readLoop() {
	defer c.wg.Done()
	defer close(c.doneCh)
	for {
		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()
		if conn == nil {
			return
		}
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if !c.IsConnected() {
				connectErr := sanitizedConnectionError(err)
				select {
				case c.connectErrCh <- connectErr:
				default:
				}
			}
			c.setDisconnected(err)
			return
		}
		if err := c.handleMessage(messageType, data); err != nil {
			if !c.IsConnected() {
				select {
				case c.connectErrCh <- err:
				default:
				}
				c.closeConnection()
				return
			}
			c.logger.Warn("Kotak SFeed message rejected",
				"event", "sfeed_message_invalid", "message_type", messageType, "error", err)
		}
	}
}

func (c *HSMClient) pingLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(sfeedPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			if !c.IsConnected() {
				continue
			}
			c.writeMu.Lock()
			c.mu.RLock()
			conn := c.conn
			c.mu.RUnlock()
			var err error
			if conn == nil {
				err = errors.New("kotak SFeed socket is not connected")
			} else {
				err = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
			c.writeMu.Unlock()
			if err != nil {
				c.logger.Warn("Kotak SFeed ping failed", "event", "sfeed_ping_failed", "error", err)
				c.closeConnection()
				return
			}
		}
	}
}

func (c *HSMClient) handleMessage(messageType int, data []byte) error {
	switch messageType {
	case websocket.TextMessage:
		return c.handleTextMessage(data)
	case websocket.BinaryMessage:
		if !c.IsConnected() {
			return nil
		}
		return c.handleBinaryMessage(data)
	default:
		return fmt.Errorf("unexpected Kotak SFeed WebSocket message type %d", messageType)
	}
}

func (c *HSMClient) handleTextMessage(data []byte) error {
	var message struct {
		MessageCode json.RawMessage `json:"message_code"`
		Format      string          `json:"format"`
		Exchanges   map[string]struct {
			Value   json.RawMessage `json:"value"`
			Divider json.RawMessage `json:"divider"`
		} `json:"exchanges"`
		TradingSymbols map[string]json.RawMessage `json:"trading_symbols"`
	}
	if err := json.Unmarshal(data, &message); err != nil {
		if c.IsConnected() {
			return nil
		}
		return errors.New("decode Kotak SFeed authentication response")
	}
	code, _ := parseJSONInt(message.MessageCode)
	if code == 1109 {
		c.mu.Lock()
		for key, rawSymbol := range message.TradingSymbols {
			var symbol string
			if key != "" && json.Unmarshal(rawSymbol, &symbol) == nil && symbol != "" {
				c.tradingSymbols[key] = symbol
			}
		}
		hasPendingAck := c.ackPending
		c.ackPending = false
		pending := c.takePendingQuotesLocked()
		c.mu.Unlock()
		if hasPendingAck {
			select {
			case c.subscribeAckCh <- struct{}{}:
			default:
			}
			c.publishPending(pending)
		}
		return nil
	}
	if code != 1117 && code != 1119 {
		if c.IsConnected() {
			return nil
		}
		return errors.New("unexpected Kotak SFeed authentication response")
	}
	if message.Format == "native_fallback" {
		return errors.New("kotak SFeed rejected native_batch authentication")
	}

	dividers := make(map[int8]int, len(message.Exchanges))
	for name, info := range message.Exchanges {
		id := exchangeID(name)
		if parsed, ok := parseJSONInt(info.Value); ok {
			if parsed < -128 || parsed > 127 {
				return errors.New("kotak SFeed authentication response contains an invalid exchange ID")
			}
			id = int8(parsed)
		}
		if divider, ok := parseJSONInt(info.Divider); ok && divider > 0 {
			dividers[id] = divider
		}
	}
	c.mu.Lock()
	c.dividers = dividers
	c.connected = true
	c.mu.Unlock()
	c.connectedOnce.Do(func() { close(c.connectedCh) })
	c.logger.Info("Kotak SFeed authenticated",
		"event", "sfeed_authenticated", "exchange_count", len(dividers))
	return nil
}

func parseJSONInt(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var value int
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	value, err := strconv.Atoi(text)
	return value, err == nil
}

func exchangeID(name string) int8 {
	for id, candidate := range exchangeNames {
		if candidate == name {
			return id
		}
	}
	return -1
}

func (c *HSMClient) handleBinaryMessage(data []byte) error {
	packets, err := splitSFeedBatch(data)
	if err != nil {
		return err
	}
	c.mu.RLock()
	dividers := make(map[int8]int, len(c.dividers))
	for id, divider := range c.dividers {
		dividers[id] = divider
	}
	symbols := make(map[string]string, len(c.tradingSymbols))
	for key, symbol := range c.tradingSymbols {
		symbols[key] = symbol
	}
	c.mu.RUnlock()
	for _, packet := range packets {
		quote, err := decodeSFeedPacket(packet, dividers, symbols)
		if err != nil {
			return err
		}
		c.publish(quote)
	}
	return nil
}

func (c *HSMClient) publish(quote *Quote) {
	if quote == nil || c.quoteCh == nil {
		return
	}
	c.mu.Lock()
	if c.ackPending && quote.Identifier == "" {
		c.pendingQuotes[quote.InstrumentKey] = quote
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.enqueueQuote(quote)
}

func (c *HSMClient) enqueueQuote(quote *Quote) {
	select {
	case c.quoteCh <- quote:
	case <-c.stopCh:
	}
}

func (c *HSMClient) takePendingQuotesLocked() []*Quote {
	pending := make([]*Quote, 0, len(c.pendingQuotes))
	for key, quote := range c.pendingQuotes {
		if quote.Identifier == "" {
			symbolKey := strings.TrimPrefix(strings.TrimPrefix(key, "sf|"), "if|")
			quote.Identifier = c.tradingSymbols[symbolKey]
		}
		pending = append(pending, quote)
	}
	c.pendingQuotes = make(map[string]*Quote)
	return pending
}

func (c *HSMClient) publishPending(quotes []*Quote) {
	for _, quote := range quotes {
		c.enqueueQuote(quote)
	}
}

// SubscribeScrips subscribes to option and futures instruments.
func (c *HSMClient) SubscribeScrips(tokens []string) error {
	return c.writeSubscription("subscribeScrips", "scrips", tokens, true)
}

// UnsubscribeScrips removes option and futures subscriptions.
func (c *HSMClient) UnsubscribeScrips(tokens []string) error {
	return c.writeSubscription("unsubscribeScrips", "scrips", tokens, false)
}

// SubscribeDepth subscribes to market depth for option and futures instruments.
func (c *HSMClient) SubscribeDepth(tokens []string) error {
	return c.writeSubscription("subscribeDepth", "depth", tokens, true)
}

// UnsubscribeDepth removes market depth subscriptions.
func (c *HSMClient) UnsubscribeDepth(tokens []string) error {
	return c.writeSubscription("unsubscribeDepth", "depth", tokens, false)
}

// SubscribeIndices subscribes to index instruments.
func (c *HSMClient) SubscribeIndices(tokens []string) error {
	return c.writeSubscription("subscribeIndices", "indices", tokens, true)
}

// UnsubscribeIndices removes index subscriptions.
func (c *HSMClient) UnsubscribeIndices(tokens []string) error {
	return c.writeSubscription("unsubscribeIndices", "indices", tokens, false)
}

func (c *HSMClient) writeSubscription(event, intent string, tokens []string, subscribe bool) error {
	if len(tokens) == 0 {
		return nil
	}
	c.subscriptionMu.Lock()
	defer c.subscriptionMu.Unlock()
	for _, token := range tokens {
		if err := validateSFeedToken(token); err != nil {
			return err
		}
	}
	if !c.IsConnected() {
		return errors.New("kotak SFeed socket is not authenticated")
	}
	keys := make([]string, 0, len(tokens))
	inputTokens := make([]string, 0, len(tokens))
	c.mu.Lock()
	for _, token := range tokens {
		key := intent + "|" + token
		if subscribe {
			if _, exists := c.subs[key]; exists {
				continue
			}
			c.subs[key] = struct{}{}
			keys = append(keys, key)
			inputTokens = append(inputTokens, token)
		} else {
			if _, exists := c.subs[key]; !exists {
				continue
			}
			delete(c.subs, key)
			keys = append(keys, key)
			inputTokens = append(inputTokens, token)
		}
	}
	if subscribe && len(c.subs) > maxSFeedTokens {
		for _, key := range keys {
			delete(c.subs, key)
		}
		c.mu.Unlock()
		return fmt.Errorf("SFeed subscription limit of %d instruments exceeded", maxSFeedTokens)
	}
	c.mu.Unlock()
	if len(inputTokens) == 0 {
		return nil
	}
	if subscribe {
		c.mu.Lock()
		c.ackPending = true
		c.mu.Unlock()
	}
	payload := map[string]any{
		"event":      event,
		"inputtoken": strings.Join(inputTokens, ","),
	}
	if subscribe {
		payload["ack_symbol"] = true
	}
	if err := c.writeJSON(payload); err != nil {
		c.mu.Lock()
		for _, key := range keys {
			if subscribe {
				delete(c.subs, key)
			} else {
				c.subs[key] = struct{}{}
			}
		}
		c.ackPending = false
		pending := c.takePendingQuotesLocked()
		c.mu.Unlock()
		go c.publishPending(pending)
		return fmt.Errorf("send Kotak SFeed %s request: %w", event, err)
	}
	if subscribe {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-c.subscribeAckCh:
		case <-timer.C:
			c.logger.Warn("Kotak SFeed subscription acknowledgement timed out",
				"event", "sfeed_subscribe_ack_timeout", "subscription_event", event)
			c.mu.Lock()
			c.ackPending = false
			pending := c.takePendingQuotesLocked()
			c.mu.Unlock()
			go c.publishPending(pending)
		case <-c.stopCh:
			return errors.New("kotak SFeed client stopped while waiting for subscription acknowledgement")
		case err := <-c.doneCh:
			if err == nil {
				return errors.New("kotak SFeed connection closed while waiting for subscription acknowledgement")
			}
			return fmt.Errorf("kotak SFeed connection closed while waiting for subscription acknowledgement: %w", err)
		}
	}
	return nil
}

func validateSFeedToken(token string) error {
	segment, instrument, found := strings.Cut(token, "|")
	if !found || segment == "" || instrument == "" ||
		strings.ContainsAny(instrument, ",|\r\n") || strings.ContainsAny(segment, ",|\r\n") {
		return fmt.Errorf("invalid SFeed instrument token %q", token)
	}
	return nil
}

func (c *HSMClient) writeJSON(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Kotak SFeed request: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return errors.New("kotak SFeed socket is not connected")
	}
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, raw)
}

func (c *HSMClient) setDisconnected(err error) {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.connected = false
	c.mu.Unlock()
	if conn != nil {
		if cerr := conn.Close(); cerr != nil && c.logger != nil {
			c.logger.Debug("failed to close SFeed connection", "error", cerr)
		}
	}
	if err == nil {
		return
	}
	safeErr := sanitizedConnectionError(err)
	select {
	case c.doneCh <- safeErr:
	default:
	}
	closeErr := &websocket.CloseError{}
	if errors.As(err, &closeErr) {
		c.logger.Warn("Kotak SFeed disconnected",
			"event", "sfeed_disconnected", "close_code", closeErr.Code,
			"close_reason_present", closeErr.Text != "")
		return
	}
	c.logger.Warn("Kotak SFeed disconnected", "event", "sfeed_disconnected", "error", safeErr)
}

func sanitizedConnectionError(err error) error {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return fmt.Errorf("kotak SFeed closed (close code %d)", closeErr.Code)
	}
	return fmt.Errorf("kotak SFeed connection failed: %w", err)
}

func (c *HSMClient) closeConnection() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.connected = false
	c.mu.Unlock()
	if conn != nil {
		if cerr := conn.Close(); cerr != nil && c.logger != nil {
			c.logger.Debug("failed to close SFeed connection", "error", cerr)
		}
	}
}

// IsConnected reports whether the SFeed connection is authenticated.
func (c *HSMClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Done returns a channel that receives the terminal connection error.
func (c *HSMClient) Done() <-chan error {
	return c.doneCh
}

// Stop closes the WebSocket and waits for the read loop to exit.
func (c *HSMClient) Stop() {
	c.stopOnce.Do(func() { close(c.stopCh) })
	c.closeConnection()
	c.wg.Wait()
}
