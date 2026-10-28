package hsm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	DefaultURL        = "wss://mlhsm.kotaksecurities.com"
	maxScripsPerBatch = 100
)

type topicState struct {
	name    string
	values  map[uint8]int32
	strings map[uint8]string
}

type HSMClient struct {
	url     string
	token   string
	sid     string
	logger  *slog.Logger
	quoteCh chan<- *Quote

	mu        sync.RWMutex
	writeMu   sync.Mutex
	conn      *websocket.Conn
	connected bool
	ackLimit  uint32
	topics    map[uint32]*topicState
	subs      map[string]bool

	stopCh        chan struct{}
	connectedCh   chan struct{}
	connectErrCh  chan error
	doneCh        chan error
	stopOnce      sync.Once
	connectedOnce sync.Once
	wg            sync.WaitGroup
}

type requestField struct {
	id    uint8
	value []byte
}

type hsmNackError struct {
	packetType uint8
}

func (e *hsmNackError) Error() string {
	return fmt.Sprintf("Kotak HSM operation %d was rejected", e.packetType)
}

func NewHSMClient(endpoint string, quoteCh chan<- *Quote) *HSMClient {
	return NewHSMClientWithLogger(endpoint, quoteCh, slog.Default())
}

func NewHSMClientWithLogger(endpoint string, quoteCh chan<- *Quote, logger *slog.Logger) *HSMClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &HSMClient{
		url:          endpoint,
		logger:       logger,
		quoteCh:      quoteCh,
		topics:       make(map[uint32]*topicState),
		subs:         make(map[string]bool),
		stopCh:       make(chan struct{}),
		connectedCh:  make(chan struct{}),
		connectErrCh: make(chan error, 1),
		doneCh:       make(chan error, 1),
	}
}

func (c *HSMClient) Connect(ctx context.Context, token, sid string) error {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(sid) == "" {
		return errors.New("HSM session token and SID are required")
	}
	if err := validateHSMURL(c.url); err != nil {
		return err
	}
	c.token = token
	c.sid = sid
	parsedURL, err := url.Parse(c.url)
	if err != nil {
		return fmt.Errorf("parse Kotak HSM URL: %w", err)
	}
	originScheme := "https"
	if parsedURL.Scheme == "ws" {
		originScheme = "http"
	}
	headers := http.Header{"Origin": []string{originScheme + "://" + parsedURL.Host}}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	c.logger.Info("Kotak HSM connection attempt", "event", "hsm_connect_attempt", "url", c.url)
	conn, response, err := dialer.DialContext(ctx, c.url, headers)
	if err != nil {
		if response != nil {
			c.logger.Warn("Kotak HSM WebSocket handshake rejected", "event", "hsm_websocket_handshake_failed",
				"http_status", response.StatusCode)
		}
		return fmt.Errorf("dial Kotak HSM: %w", err)
	}
	c.logger.Info("Kotak HSM WebSocket handshake established", "event", "hsm_websocket_open",
		"http_status", response.StatusCode)
	conn.SetReadLimit(1 << 20)
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	c.wg.Add(1)
	go c.readLoop()

	authFrame, err := encodeRequest(ConnectType, []requestField{
		{id: 1, value: []byte(token)},
		{id: 2, value: []byte(sid)},
		{id: 3, value: []byte("JS_API")},
	})
	if err != nil {
		c.Stop()
		return err
	}
	if err := c.writeFrame(authFrame); err != nil {
		c.Stop()
		return fmt.Errorf("send Kotak HSM authentication: %w", err)
	}
	c.logger.Info("Kotak HSM authentication frame sent", "event", "hsm_auth_frame_sent",
		"packet_type", ConnectType, "frame_bytes", len(authFrame),
		"token_present", token != "", "sid_present", sid != "", "source", "JS_API")

	select {
	case <-c.connectedCh:
		return nil
	case err := <-c.connectErrCh:
		c.Stop()
		return err
	case <-ctx.Done():
		c.Stop()
		return fmt.Errorf("wait for Kotak HSM authentication: %w", ctx.Err())
	case <-time.After(15 * time.Second):
		c.Stop()
		return errors.New("timed out waiting for Kotak HSM authentication")
	}
}

func validateHSMURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("HSM URL is invalid")
	}
	host := strings.ToLower(u.Hostname())
	trusted := host == "kotaksecurities.com" || strings.HasSuffix(host, ".kotaksecurities.com")
	localTest := u.Scheme == "ws" && (host == "localhost" || host == "127.0.0.1")
	if (u.Scheme != "wss" && !localTest) || (!trusted && !localTest) {
		return errors.New("HSM URL is not trusted")
	}
	return nil
}

func (c *HSMClient) readLoop() {
	defer c.wg.Done()
	defer close(c.doneCh)
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	readErr := make(chan error, 1)
	go func() {
		for {
			messageType, data, err := c.readMessage()
			if err != nil {
				if !c.IsConnected() {
					handshakeErr := authenticationReadError(err)
					select {
					case c.connectErrCh <- handshakeErr:
					default:
					}
				}
				readErr <- err
				return
			}
			c.logIncomingMessage(messageType, data)
			if err := c.handleMessage(messageType, data); err != nil {
				c.logger.Warn("Kotak HSM frame rejected", "event", "hsm_frame_invalid",
					"message_type", websocketMessageTypeName(messageType), "frame_bytes", len(data),
					"error", err)
				var nack *hsmNackError
				if errors.As(err, &nack) || !c.IsConnected() {
					select {
					case c.connectErrCh <- err:
					default:
					}
					readErr <- err
					return
				}
			}
		}
	}()

	for {
		select {
		case <-c.stopCh:
			return
		case err := <-readErr:
			c.setDisconnected(err)
			return
		case <-heartbeat.C:
			value := make([]byte, 4)
			frame, err := encodeRequest(KeepaliveType, []requestField{{id: 1, value: value}})
			if err == nil {
				err = c.writeFrame(frame)
			}
			if err != nil {
				c.setDisconnected(fmt.Errorf("Kotak HSM heartbeat: %w", err))
				return
			}
		}
	}
}

func authenticationReadError(err error) error {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return fmt.Errorf("Kotak HSM closed during authentication (close code %d)", closeErr.Code)
	}
	return fmt.Errorf("read Kotak HSM authentication response: %w", err)
}

func (c *HSMClient) logIncomingMessage(messageType int, data []byte) {
	if c.IsConnected() {
		return
	}
	attrs := []any{"message_type", websocketMessageTypeName(messageType), "frame_bytes", len(data)}
	switch messageType {
	case websocket.TextMessage:
		var messages []struct {
			Type        string          `json:"type"`
			MessageCode json.RawMessage `json:"message_code"`
			Exchanges   json.RawMessage `json:"exchanges"`
		}
		trimmed := strings.TrimSpace(string(data))
		if strings.HasPrefix(trimmed, "[") {
			if err := json.Unmarshal(data, &messages); err != nil {
				attrs = append(attrs, "json_valid", false)
			}
		} else {
			var message struct {
				Type        string          `json:"type"`
				MessageCode json.RawMessage `json:"message_code"`
				Exchanges   json.RawMessage `json:"exchanges"`
			}
			if err := json.Unmarshal(data, &message); err != nil {
				attrs = append(attrs, "json_valid", false)
			} else {
				messages = append(messages, message)
			}
		}
		if len(messages) > 0 {
			message := messages[0]
			attrs = append(attrs, "response_type", message.Type)
			if len(message.MessageCode) > 0 {
				attrs = append(attrs, "message_code", string(message.MessageCode))
			}
			if len(message.Exchanges) > 0 {
				var exchanges map[string]json.RawMessage
				if err := json.Unmarshal(message.Exchanges, &exchanges); err == nil {
					attrs = append(attrs, "exchange_count", len(exchanges))
				}
			}
		}
	case websocket.BinaryMessage:
		if len(data) >= 3 {
			attrs = append(attrs, "declared_length", binary.BigEndian.Uint16(data[:2]),
				"packet_type", data[2])
		}
	}
	c.logger.Info("Kotak HSM handshake response received", append([]any{"event", "hsm_handshake_response"}, attrs...)...)
}

func websocketMessageTypeName(messageType int) string {
	switch messageType {
	case websocket.TextMessage:
		return "text"
	case websocket.BinaryMessage:
		return "binary"
	default:
		return "other"
	}
}

func (c *HSMClient) readMessage() (int, []byte, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return 0, nil, errors.New("Kotak HSM socket is not open")
	}
	return conn.ReadMessage()
}

func (c *HSMClient) handleMessage(messageType int, data []byte) error {
	switch messageType {
	case websocket.TextMessage:
		return c.handleTextMessage(data)
	case websocket.BinaryMessage:
		return c.handleFrame(data)
	default:
		return fmt.Errorf("unexpected Kotak HSM WebSocket message type %d", messageType)
	}
}

func (c *HSMClient) handleTextMessage(data []byte) error {
	type textResponse struct {
		Type        string                     `json:"type"`
		MessageCode int                        `json:"message_code"`
		Exchanges   map[string]json.RawMessage `json:"exchanges"`
	}
	var messages []textResponse
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(data, &messages); err != nil {
			return fmt.Errorf("decode Kotak HSM text response: %w", err)
		}
	} else {
		var message textResponse
		if err := json.Unmarshal(data, &message); err != nil {
			return fmt.Errorf("decode Kotak HSM text response: %w", err)
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return errors.New("Kotak HSM text response is empty")
	}
	metadataReceived := false
	for _, message := range messages {
		if strings.EqualFold(message.Type, "cn") {
			c.markConnected()
			return nil
		}
		if message.MessageCode == 1119 && len(message.Exchanges) > 0 {
			metadataReceived = true
			c.logger.Debug("Kotak HSM server metadata received", "event", "hsm_server_metadata",
				"message_code", message.MessageCode, "exchange_count", len(message.Exchanges))
			continue
		}
	}
	if metadataReceived {
		return nil
	}
	return fmt.Errorf("Kotak HSM text response did not confirm connection (message_code %d)",
		messages[0].MessageCode)
}

func (c *HSMClient) handleFrame(data []byte) error {
	frame, err := ParseFrame(data)
	if err != nil {
		return err
	}
	switch MessageType(frame.Header.Type) {
	case KeepaliveType, ACKType:
		return nil
	case ConnectType:
		return c.handleConnectResponse(frame)
	case DataType:
		return c.handleDataFrame(frame.Payload)
	case SubscribeType, UnsubscribeType, SnapshotType, 7, 8, OPCType:
		status := ""
		if value := frame.Fields[1]; len(value) > 0 {
			status = string(value)
		}
		if status == "N" {
			return &hsmNackError{packetType: frame.Header.Type}
		}
		c.logger.Debug("Kotak HSM status", "event", "hsm_status",
			"packet_type", frame.Header.Type, "status", status)
		return nil
	default:
		return fmt.Errorf("unknown Kotak HSM packet type %d", frame.Header.Type)
	}
}

func (c *HSMClient) handleConnectResponse(frame HSMFrame) error {
	status := string(frame.Fields[1])
	if status != "K" {
		err := fmt.Errorf("Kotak HSM authentication rejected (status %q)", status)
		select {
		case c.connectErrCh <- err:
		default:
		}
		return err
	}
	if threshold := frame.Fields[2]; len(threshold) > 0 {
		limit, err := parseACKThreshold(threshold)
		if err != nil {
			c.logger.Warn("invalid Kotak HSM ACK threshold; ACKs disabled",
				"event", "hsm_ack_threshold_invalid", "error", err)
			limit = 0
		}
		c.mu.Lock()
		c.ackLimit = limit
		c.mu.Unlock()
	}
	c.markConnected()
	return nil
}

func (c *HSMClient) markConnected() {
	c.mu.Lock()
	c.connected = true
	ackLimit := c.ackLimit
	c.mu.Unlock()
	c.connectedOnce.Do(func() { close(c.connectedCh) })
	c.logger.Info("Kotak HSM connected", "event", "hsm_connected", "ack_threshold", ackLimit)
}

func parseACKThreshold(raw []byte) (uint32, error) {
	if len(raw) == 0 || len(raw) > 4 {
		return 0, errors.New("ACK threshold must contain 1 to 4 bytes")
	}
	if len(raw) > 1 {
		ascii := true
		for _, b := range raw {
			if b < '0' || b > '9' {
				ascii = false
				break
			}
		}
		if ascii {
			value, err := strconv.ParseUint(string(raw), 10, 32)
			return uint32(value), err
		}
	}
	var value uint32
	for _, b := range raw {
		value = (value << 8) | uint32(b)
	}
	return value, nil
}

func (c *HSMClient) handleDataFrame(payload []byte) error {
	offset := 0
	c.mu.RLock()
	ackLimit := c.ackLimit
	c.mu.RUnlock()
	var messageNumber uint32
	if ackLimit > 0 {
		if len(payload) < 4 {
			return errors.New("HSM data frame is missing its message number")
		}
		messageNumber = binary.BigEndian.Uint32(payload[:4])
		offset += 4
	}
	if len(payload)-offset < 2 {
		return errors.New("HSM data frame is missing its topic count")
	}
	packetCount := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	offset += 2
	for packet := 0; packet < packetCount; packet++ {
		if len(payload)-offset < 2 {
			return errors.New("HSM data frame has a truncated packet length")
		}
		packetLength := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if packetLength < 5 || len(payload)-offset < packetLength {
			return errors.New("HSM data frame has a truncated topic packet")
		}
		start := offset
		packetType := payload[offset]
		offset++
		topicID := binary.BigEndian.Uint32(payload[offset : offset+4])
		offset += 4
		var quote *Quote
		var err error
		switch packetType {
		case 'S':
			quote, err = c.parseSnapshot(topicID, payload, &offset)
		case 'U':
			quote, err = c.parseUpdate(topicID, payload, &offset)
		default:
			err = fmt.Errorf("unknown HSM topic packet type 0x%x", packetType)
		}
		if err != nil {
			return err
		}
		if offset-start != packetLength {
			return fmt.Errorf("HSM topic packet length mismatch: header=%d consumed=%d", packetLength, offset-start)
		}
		if quote != nil {
			c.publish(quote)
		}
	}
	if offset != len(payload) {
		return fmt.Errorf("HSM data frame has %d trailing bytes", len(payload)-offset)
	}
	if ackLimit > 0 && messageNumber > 0 {
		value := make([]byte, 4)
		binary.BigEndian.PutUint32(value, messageNumber)
		frame, err := encodeRequest(ACKType, []requestField{{id: 1, value: value}})
		if err != nil {
			return err
		}
		return c.writeFrame(frame)
	}
	return nil
}

func (c *HSMClient) parseSnapshot(id uint32, data []byte, offset *int) (*Quote, error) {
	if *offset >= len(data) {
		return nil, errors.New("HSM snapshot is missing topic name length")
	}
	nameLength := int(data[*offset])
	*offset++
	if len(data)-*offset < nameLength+1 {
		return nil, errors.New("HSM snapshot topic name is truncated")
	}
	name := string(data[*offset : *offset+nameLength])
	*offset += nameLength
	valueCount := int(data[*offset])
	*offset++
	if len(data)-*offset < valueCount*4+1 {
		return nil, errors.New("HSM snapshot values are truncated")
	}
	state := &topicState{name: name, values: make(map[uint8]int32), strings: make(map[uint8]string)}
	for field := 0; field < valueCount; field++ {
		state.values[uint8(field)] = int32(binary.BigEndian.Uint32(data[*offset : *offset+4]))
		*offset += 4
	}
	stringCount := int(data[*offset])
	*offset++
	for i := 0; i < stringCount; i++ {
		if len(data)-*offset < 2 {
			return nil, errors.New("HSM snapshot string field is truncated")
		}
		fieldID := data[*offset]
		stringLength := int(data[*offset+1])
		*offset += 2
		if len(data)-*offset < stringLength {
			return nil, errors.New("HSM snapshot string value is truncated")
		}
		state.strings[fieldID] = string(data[*offset : *offset+stringLength])
		*offset += stringLength
	}
	c.mu.Lock()
	c.topics[id] = state
	c.mu.Unlock()
	return quoteFromTopic(state), nil
}

func (c *HSMClient) parseUpdate(id uint32, data []byte, offset *int) (*Quote, error) {
	if *offset >= len(data) {
		return nil, errors.New("HSM update is missing value count")
	}
	valueCount := int(data[*offset])
	*offset++
	if len(data)-*offset < valueCount*4 {
		return nil, errors.New("HSM update values are truncated")
	}
	c.mu.Lock()
	state := c.topics[id]
	if state == nil {
		c.mu.Unlock()
		c.logger.Warn("ignoring update for unknown HSM topic", "event", "hsm_unknown_topic", "topic_id", id)
		*offset += valueCount * 4
		return nil, nil
	}
	for field := 0; field < valueCount; field++ {
		value := int32(binary.BigEndian.Uint32(data[*offset : *offset+4]))
		*offset += 4
		if value != trashValue {
			state.values[uint8(field)] = value
		}
	}
	quote := quoteFromTopic(state)
	c.mu.Unlock()
	return quote, nil
}

func quoteFromTopic(topic *topicState) *Quote {
	if topic == nil {
		return nil
	}
	parts := strings.SplitN(topic.name, "|", 2)
	if len(parts) != 2 {
		return nil
	}
	prefix := parts[0]
	ltpField, closeField, multiplierField, precisionField := uint8(5), uint8(21), uint8(23), uint8(24)
	timestampField := uint8(3)
	if prefix == "if" {
		ltpField, closeField, multiplierField, precisionField = 2, 3, 8, 9
		timestampField = 0
	}
	ltp, ltpOK := fixedPrice(topic.values, ltpField, multiplierField, precisionField)
	if !ltpOK {
		return nil
	}
	close, _ := fixedPrice(topic.values, closeField, multiplierField, precisionField)
	change := ltp - close
	changePct := 0.0
	if close > 0 {
		changePct = change / close * 100
	}
	quote := &Quote{
		InstrumentKey: topic.name,
		LTP:           ltp,
		PreviousClose: close,
		Change:        change,
		ChangePct:     changePct,
		Identifier:    topic.strings[52],
	}
	if ts, ok := topic.values[timestampField]; ok && ts != trashValue {
		quote.Timestamp = int64(ts)
	}
	if prefix == "sf" {
		quote.Volume = nonNegative(topic.values[4])
		quote.OI = nonNegative(topic.values[22])
		quote.Bid, _ = fixedPrice(topic.values, 9, multiplierField, precisionField)
		quote.Ask, _ = fixedPrice(topic.values, 10, multiplierField, precisionField)
	}
	return quote
}

func fixedPrice(values map[uint8]int32, field, multiplierField, precisionField uint8) (float64, bool) {
	raw, ok := values[field]
	if !ok || raw == trashValue {
		return 0, false
	}
	multiplier := float64(1)
	if value, ok := values[multiplierField]; ok && value > 0 && value != trashValue {
		multiplier = float64(value)
	}
	precision := int32(2)
	if value, ok := values[precisionField]; ok && value >= 0 && value <= 9 && value != trashValue {
		precision = value
	}
	price := float64(raw) / (multiplier * math.Pow10(int(precision)))
	return price, !math.IsNaN(price) && !math.IsInf(price, 0)
}

func nonNegative(value int32) int64 {
	if value < 0 || value == trashValue {
		return 0
	}
	return int64(value)
}

func (c *HSMClient) publish(quote *Quote) {
	if quote == nil || c.quoteCh == nil {
		return
	}
	select {
	case c.quoteCh <- quote:
	case <-c.stopCh:
	}
}

func (c *HSMClient) Subscribe(instruments []string) error {
	return c.writeSubscription(SubscribeType, instruments)
}

func (c *HSMClient) Unsubscribe(instruments []string) error {
	return c.writeSubscription(UnsubscribeType, instruments)
}

func (c *HSMClient) writeSubscription(packetType MessageType, instruments []string) error {
	if len(instruments) == 0 {
		return nil
	}
	for start := 0; start < len(instruments); start += maxScripsPerBatch {
		end := min(start+maxScripsPerBatch, len(instruments))
		batch := instruments[start:end]
		blob, err := encodeScripBlob(batch)
		if err != nil {
			return err
		}
		frame, err := encodeRequest(packetType, []requestField{
			{id: 1, value: blob},
			{id: 2, value: []byte{1}},
		})
		if err != nil {
			return err
		}
		if err := c.writeFrame(frame); err != nil {
			return err
		}
		c.mu.Lock()
		for _, instrument := range batch {
			if packetType == SubscribeType {
				c.subs[instrument] = true
			} else {
				delete(c.subs, instrument)
			}
		}
		c.mu.Unlock()
	}
	return nil
}

func encodeScripBlob(instruments []string) ([]byte, error) {
	if len(instruments) == 0 || len(instruments) > maxScripsPerBatch {
		return nil, fmt.Errorf("HSM subscription count must be between 1 and %d", maxScripsPerBatch)
	}
	size := 2
	for _, instrument := range instruments {
		parts := strings.SplitN(instrument, "|", 3)
		if len(instrument) == 0 || len(instrument) > 255 || len(parts) != 3 ||
			(parts[0] != "sf" && parts[0] != "if" && parts[0] != "dp") ||
			parts[1] == "" || parts[2] == "" {
			return nil, fmt.Errorf("invalid HSM scrip %q", instrument)
		}
		size += 1 + len(instrument)
	}
	blob := make([]byte, size)
	binary.BigEndian.PutUint16(blob[:2], uint16(len(instruments)))
	offset := 2
	for _, instrument := range instruments {
		blob[offset] = byte(len(instrument))
		offset++
		copy(blob[offset:], instrument)
		offset += len(instrument)
	}
	return blob, nil
}

func encodeRequest(packetType MessageType, fields []requestField) ([]byte, error) {
	if len(fields) > 255 {
		return nil, errors.New("HSM request has too many fields")
	}
	size := 2 + 2
	for _, field := range fields {
		if len(field.value) > 65535 {
			return nil, errors.New("HSM request field is too long")
		}
		size += 3 + len(field.value)
	}
	if size-2 > 65535 {
		return nil, errors.New("HSM request frame is too long")
	}
	frame := make([]byte, size)
	binary.BigEndian.PutUint16(frame[:2], uint16(size-2))
	frame[2] = byte(packetType)
	frame[3] = byte(len(fields))
	offset := 4
	for _, field := range fields {
		frame[offset] = field.id
		binary.BigEndian.PutUint16(frame[offset+1:offset+3], uint16(len(field.value)))
		copy(frame[offset+3:], field.value)
		offset += 3 + len(field.value)
	}
	return frame, nil
}

func (c *HSMClient) writeFrame(frame []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return errors.New("Kotak HSM socket is not connected")
	}
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (c *HSMClient) setDisconnected(err error) {
	c.mu.Lock()
	c.connected = false
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	if err != nil {
		select {
		case c.doneCh <- err:
		default:
		}
	}
	if err != nil {
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) {
			c.logger.Warn("Kotak HSM disconnected", "event", "hsm_disconnected",
				"close_code", closeErr.Code, "close_reason_present", closeErr.Text != "")
			return
		}
	}
	c.logger.Warn("Kotak HSM disconnected", "event", "hsm_disconnected", "error", err)
}

func (c *HSMClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

func (c *HSMClient) WaitForConnection() <-chan struct{} {
	return c.connectedCh
}

func (c *HSMClient) Done() <-chan error {
	return c.doneCh
}

func (c *HSMClient) Stop() {
	c.stopOnce.Do(func() { close(c.stopCh) })
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.connected = false
	c.mu.Unlock()
	if conn != nil {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "logout"),
			time.Now().Add(time.Second))
		_ = conn.Close()
	}
	c.wg.Wait()
}
