package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultEndpoint = "wss://sfeed.kotaksecurities.com/wsfeed"
	loginURL        = "https://mis.kotaksecurities.com"
)

type credentials struct {
	accessToken string
	mobile      string
	ucc         string
	neoFinKey   string
	totp        string
	mpin        string
}

type session struct {
	token string
	sid   string
}

type apiResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  json.RawMessage `json:"error"`
}

type sessionData struct {
	Token   string `json:"token"`
	SID     string `json:"sid"`
	BaseURL string `json:"baseUrl"`
	Status  string `json:"status"`
}

type incoming struct {
	messageType int
	data        []byte
	err         error
}

type topicState struct {
	name   string
	values []int32
}

func main() {
	endpoint := flag.String("endpoint", envOr("KOTAK_HSM_URL", defaultEndpoint), "HSM WebSocket URL")
	instrument := flag.String("instrument", "if|nse_cm|Nifty 50", "index topic, e.g. if|nse_cm|Nifty 50")
	duration := flag.Duration("duration", time.Minute, "time to receive feed; 0 waits until interrupted")
	showSecrets := flag.Bool("show-secrets", false, "print TOTP, MPIN, JWT and SID values (sensitive; do not share output)")
	flag.Parse()

	if *duration < 0 {
		fail("duration must be zero or greater")
	}
	if err := validateEndpoint(*endpoint); err != nil {
		fail("%v", err)
	}
	if err := validateInstrument(*instrument); err != nil {
		fail("%v", err)
	}

	creds, err := readCredentials()
	if err != nil {
		fail("%v", err)
	}
	fmt.Printf("Credential input: TOTP=%s MPIN=%s access_token=[REDACTED]\n",
		displaySecret(creds.totp, *showSecrets), displaySecret(creds.mpin, *showSecrets))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("TOTP login...")
	view, err := login(ctx, creds)
	if err != nil {
		fail("TOTP login failed: %v", err)
	}
	fmt.Println("TOTP login: success")
	fmt.Printf("TOTP login response: JWT=%s (%d bytes) SID=%s (%d bytes)\n",
		displaySecret(view.token, *showSecrets), len(view.token),
		displaySecret(view.sid, *showSecrets), len(view.sid))

	fmt.Println("MPIN validation...")
	fmt.Printf("MPIN validation request: JWT=%s (%d bytes) SID=%s (%d bytes) MPIN=%s\n",
		displaySecret(view.token, *showSecrets), len(view.token),
		displaySecret(view.sid, *showSecrets), len(view.sid),
		displaySecret(creds.mpin, *showSecrets))
	trade, err := validateMPIN(ctx, creds, view)
	if err != nil {
		fail("MPIN validation failed: %v", err)
	}
	fmt.Println("MPIN validation: success")
	fmt.Printf("MPIN validation response: JWT=%s (%d bytes) SID=%s (%d bytes)\n",
		displaySecret(trade.token, *showSecrets), len(trade.token),
		displaySecret(trade.sid, *showSecrets), len(trade.sid))

	fmt.Printf("Connecting to HSM: %s\n", *endpoint)
	conn, response, err := dial(*endpoint)
	if err != nil {
		if response != nil {
			fail("WebSocket handshake failed (HTTP %d)", response.StatusCode)
		}
		fail("WebSocket handshake failed: %v", err)
	}
	defer closeSocket(conn)
	fmt.Println("WebSocket handshake: established")

	fmt.Printf("HSM auth request fields: type=cn Authorization(JWT)=%s (%d bytes) Sid=%s (%d bytes)\n",
		displaySecret(trade.token, *showSecrets), len(trade.token),
		displaySecret(trade.sid, *showSecrets), len(trade.sid))
	if err := writeBinary(conn, encodeConnect(trade)); err != nil {
		fail("send HSM authentication frame: %v", err)
	}
	fmt.Println("HSM authentication frame: sent")

	ackLimit, err := waitForAuthentication(ctx, conn)
	if err != nil {
		fail("HSM authentication failed: %v", err)
	}
	fmt.Println("HSM authentication: acknowledged")

	if err := writeBinary(conn, encodeSubscribe(*instrument)); err != nil {
		fail("send HSM subscription: %v", err)
	}
	fmt.Printf("HSM subscription sent: %s\n", *instrument)

	fmt.Println("Waiting for HSM subscription acknowledgement and market data...")
	if err := receiveFeed(ctx, conn, ackLimit, *instrument, *duration); err != nil {
		fail("HSM feed ended: %v", err)
	}
	fmt.Println("HSM probe finished")
}

func readCredentials() (credentials, error) {
	creds := credentials{
		accessToken: strings.TrimSpace(os.Getenv("KOTAK_ACCESS_TOKEN")),
		mobile:      strings.TrimSpace(os.Getenv("KOTAK_MOBILE_NUMBER")),
		ucc:         strings.TrimSpace(os.Getenv("KOTAK_UCC")),
		neoFinKey:   strings.TrimSpace(os.Getenv("KOTAK_NEO_FIN_KEY")),
		totp:        strings.TrimSpace(os.Getenv("KOTAK_TOTP")),
		mpin:        strings.TrimSpace(os.Getenv("KOTAK_MPIN")),
	}
	if creds.neoFinKey == "" {
		creds.neoFinKey = "neotradeapi"
	}
	switch {
	case creds.accessToken == "":
		return credentials{}, errors.New("set KOTAK_ACCESS_TOKEN")
	case creds.mobile == "":
		return credentials{}, errors.New("set KOTAK_MOBILE_NUMBER")
	case creds.ucc == "":
		return credentials{}, errors.New("set KOTAK_UCC")
	case creds.totp == "":
		return credentials{}, errors.New("set KOTAK_TOTP")
	case creds.mpin == "":
		return credentials{}, errors.New("set KOTAK_MPIN")
	}
	return creds, nil
}

func displaySecret(value string, show bool) string {
	if !show {
		if value == "" {
			return "[REDACTED] (missing)"
		}
		return "[REDACTED]"
	}
	if value == "" {
		return "[empty]"
	}
	return strconv.Quote(value)
}

func login(ctx context.Context, creds credentials) (session, error) {
	body := map[string]string{
		"mobileNumber": creds.mobile,
		"ucc":          creds.ucc,
		"totp":         creds.totp,
	}
	data, err := postJSON(ctx, loginURL+"/login/1.0/tradeApiLogin", creds, body, nil)
	if err != nil {
		return session{}, err
	}
	if data.Token == "" || data.SID == "" {
		return session{}, errors.New("response omitted view token or SID")
	}
	return session{token: data.Token, sid: data.SID}, nil
}

func validateMPIN(ctx context.Context, creds credentials, view session) (session, error) {
	headers := http.Header{}
	headers.Set("Auth", view.token)
	headers.Set("sid", view.sid)
	data, err := postJSON(ctx, loginURL+"/login/1.0/tradeApiValidate", creds,
		map[string]string{"mpin": creds.mpin}, headers)
	if err != nil {
		return session{}, err
	}
	if data.Token == "" || data.SID == "" {
		return session{}, errors.New("response omitted trade token or SID")
	}
	return session{token: data.Token, sid: data.SID}, nil
}

func postJSON(ctx context.Context, endpoint string, creds credentials, body any, extra http.Header) (sessionData, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return sessionData{}, errors.New("could not encode login request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return sessionData{}, errors.New("could not create login request")
	}
	req.Header.Set("Authorization", creds.accessToken)
	req.Header.Set("neo-fin-key", creds.neoFinKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for key, values := range extra {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(req)
	if err != nil {
		return sessionData{}, fmt.Errorf("request failed (%s)", safeNetworkError(err))
	}
	defer response.Body.Close()
	const maxBody = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return sessionData{}, errors.New("could not read login response")
	}
	if len(raw) > maxBody {
		return sessionData{}, errors.New("login response exceeded size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return sessionData{}, fmt.Errorf("server returned HTTP %d", response.StatusCode)
	}

	var result apiResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return sessionData{}, errors.New("server returned invalid JSON")
	}
	dataRaw := result.Data
	if len(dataRaw) == 0 || bytes.Equal(dataRaw, []byte("null")) {
		dataRaw = raw
	}
	var data sessionData
	if err := json.Unmarshal(dataRaw, &data); err != nil {
		return sessionData{}, errors.New("login response omitted session data")
	}
	status := data.Status
	if status == "" {
		status = result.Status
	}
	if status != "" && !strings.EqualFold(status, "success") && !strings.EqualFold(status, "ok") {
		return sessionData{}, fmt.Errorf("server rejected login (status %q)", status)
	}
	return data, nil
}

func dial(endpoint string) (*websocket.Conn, *http.Response, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, nil, errors.New("invalid HSM endpoint")
	}
	originScheme := "https"
	if parsed.Scheme == "ws" {
		originScheme = "http"
	}
	headers := http.Header{}
	headers.Set("Origin", originScheme+"://"+parsed.Host)
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	return dialer.Dial(endpoint, headers)
}

func waitForAuthentication(ctx context.Context, conn *websocket.Conn) (uint32, error) {
	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		readDeadline := time.Now().Add(time.Second)
		if readDeadline.After(deadline) {
			readDeadline = deadline
		}
		if err := conn.SetReadDeadline(readDeadline); err != nil {
			return 0, fmt.Errorf("set WebSocket read deadline: %w", err)
		}
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			var timeout interface{ Timeout() bool }
			if errors.As(err, &timeout) && timeout.Timeout() {
				if time.Now().Before(deadline) {
					continue
				}
				return 0, errors.New("timed out waiting for authentication acknowledgement")
			}
			return 0, fmt.Errorf("socket closed before authentication acknowledgement (%s)",
				safeNetworkError(err))
		}
		switch messageType {
		case websocket.TextMessage:
			kind, code, err := parseTextMessage(data)
			if err != nil {
				return 0, err
			}
			if code == 1119 {
				fmt.Println("HSM initialization metadata received")
			}
			if kind == "cn" {
				_ = conn.SetReadDeadline(time.Time{})
				return 0, nil
			}
		case websocket.BinaryMessage:
			frame, err := parseStatusFrame(data)
			if err != nil {
				return 0, err
			}
			if frame.packetType != 1 {
				return 0, fmt.Errorf("unexpected binary packet type %d before authentication", frame.packetType)
			}
			status := string(frame.fields[1])
			if status != "K" {
				return 0, fmt.Errorf("server rejected authentication (status %q)", status)
			}
			threshold, err := parseThreshold(frame.fields[2])
			if err != nil {
				return 0, fmt.Errorf("invalid ACK threshold: %w", err)
			}
			_ = conn.SetReadDeadline(time.Time{})
			return threshold, nil
		default:
			return 0, fmt.Errorf("unexpected WebSocket message type %d", messageType)
		}
	}
}

func receiveFeed(ctx context.Context, conn *websocket.Conn, ackLimit uint32, instrument string, duration time.Duration) error {
	var timeout <-chan time.Time
	var timer *time.Timer
	if duration > 0 {
		timer = time.NewTimer(duration)
		defer timer.Stop()
		timeout = timer.C
	}
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	frames := make(chan incoming, 1)
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			messageType, data, err := conn.ReadMessage()
			select {
			case frames <- incoming{messageType: messageType, data: data, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		_ = conn.Close()
		readerWG.Wait()
	}()

	topics := make(map[uint32]*topicState)
	var dataCount uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			if dataCount == 0 {
				return errors.New("timed out without receiving a market data frame")
			}
			fmt.Printf("Duration elapsed; received %d market data frame(s)\n", dataCount)
			return nil
		case <-heartbeat.C:
			if err := writeBinary(conn, encodeRequest(2, []field{{id: 1, value: []byte{0, 0, 0, 0}}})); err != nil {
				return fmt.Errorf("send HSM heartbeat: %w", err)
			}
		case received := <-frames:
			if received.err != nil {
				return fmt.Errorf("WebSocket read failed (%s)", safeNetworkError(received.err))
			}
			switch received.messageType {
			case websocket.TextMessage:
				kind, code, err := parseTextMessage(received.data)
				if err != nil {
					return err
				}
				if code == 1119 {
					fmt.Println("HSM initialization metadata received")
				} else if kind != "" {
					fmt.Printf("HSM text response: type=%s\n", kind)
				}
			case websocket.BinaryMessage:
				packetType, fields, payload, err := parseFrame(received.data)
				if err != nil {
					return fmt.Errorf("decode HSM binary frame: %w", err)
				}
				switch packetType {
				case 3:
					fmt.Println("HSM acknowledgement received")
				case 4, 5, 7, 8, 9, 10:
					status := string(fields[1])
					if status == "N" {
						return fmt.Errorf("server rejected HSM operation (packet type %d)", packetType)
					}
					fmt.Printf("HSM operation acknowledgement: packet_type=%d status=%q\n", packetType, status)
				case 6:
					count, ackNumber, err := parseDataFrame(payload, ackLimit, topics, instrument)
					if err != nil {
						return fmt.Errorf("decode HSM market data: %w", err)
					}
					dataCount += uint64(count)
					if ackLimit > 0 && ackNumber > 0 {
						ack := make([]byte, 4)
						binary.BigEndian.PutUint32(ack, ackNumber)
						if err := writeBinary(conn, encodeRequest(3, []field{{id: 1, value: ack}})); err != nil {
							return fmt.Errorf("send HSM data acknowledgement: %w", err)
						}
					}
					if count == 0 {
						fmt.Printf("HSM data frame received (%d bytes; no matching decoded index quote)\n", len(received.data))
					}
				default:
					fmt.Printf("HSM binary frame received: packet_type=%d bytes=%d\n", packetType, len(received.data))
				}
			default:
				return fmt.Errorf("unexpected WebSocket message type %d", received.messageType)
			}
		}
	}
}

type field struct {
	id    byte
	value []byte
}

func encodeConnect(trade session) []byte {
	return encodeRequest(1, []field{
		{id: 1, value: []byte(trade.token)},
		{id: 2, value: []byte(trade.sid)},
		{id: 3, value: []byte("JS_API")},
	})
}

func encodeSubscribe(instrument string) []byte {
	scrip := make([]byte, 2+1+len(instrument))
	binary.BigEndian.PutUint16(scrip[:2], 1)
	scrip[2] = byte(len(instrument))
	copy(scrip[3:], instrument)
	return encodeRequest(4, []field{
		{id: 1, value: scrip},
		{id: 2, value: []byte{1}},
	})
}

func encodeRequest(packetType byte, fields []field) []byte {
	size := 2 + 2
	for _, field := range fields {
		size += 3 + len(field.value)
	}
	frame := make([]byte, size)
	binary.BigEndian.PutUint16(frame[:2], uint16(size-2))
	frame[2] = packetType
	frame[3] = byte(len(fields))
	offset := 4
	for _, field := range fields {
		frame[offset] = field.id
		binary.BigEndian.PutUint16(frame[offset+1:offset+3], uint16(len(field.value)))
		copy(frame[offset+3:], field.value)
		offset += 3 + len(field.value)
	}
	return frame
}

func parseFrame(data []byte) (byte, map[byte][]byte, []byte, error) {
	if len(data) < 3 {
		return 0, nil, nil, errors.New("frame is too short")
	}
	length := int(binary.BigEndian.Uint16(data[:2]))
	if length != len(data)-2 {
		return 0, nil, nil, errors.New("frame length does not match payload")
	}
	packetType := data[2]
	if packetType == 6 {
		return packetType, nil, data[3:], nil
	}
	if len(data) < 4 {
		return 0, nil, nil, errors.New("status frame is missing its field count")
	}
	count := int(data[3])
	fields := make(map[byte][]byte, count)
	offset := 4
	for i := 0; i < count; i++ {
		if len(data)-offset < 3 {
			return 0, nil, nil, errors.New("truncated field header")
		}
		id := data[offset]
		n := int(binary.BigEndian.Uint16(data[offset+1 : offset+3]))
		offset += 3
		if len(data)-offset < n {
			return 0, nil, nil, errors.New("truncated field body")
		}
		fields[id] = append([]byte(nil), data[offset:offset+n]...)
		offset += n
	}
	if offset != len(data) {
		return 0, nil, nil, errors.New("unexpected trailing bytes in frame")
	}
	return packetType, fields, nil, nil
}

func parseStatusFrame(data []byte) (struct {
	packetType byte
	fields     map[byte][]byte
}, error) {
	packetType, fields, _, err := parseFrame(data)
	return struct {
		packetType byte
		fields     map[byte][]byte
	}{packetType: packetType, fields: fields}, err
}

func parseTextMessage(data []byte) (string, int, error) {
	type textMessage struct {
		Type        string `json:"type"`
		MessageCode int    `json:"message_code"`
		Status      string `json:"status"`
	}
	var messages []textMessage
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "", 0, errors.New("empty HSM text message")
	}
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &messages); err != nil {
			return "", 0, errors.New("invalid HSM text message")
		}
	} else {
		var message textMessage
		if err := json.Unmarshal(trimmed, &message); err != nil {
			return "", 0, errors.New("invalid HSM text message")
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return "", 0, errors.New("HSM text response contained no messages")
	}
	for _, message := range messages {
		if strings.EqualFold(message.Type, "cn") {
			if message.Status != "" && !strings.EqualFold(message.Status, "K") &&
				!strings.EqualFold(message.Status, "success") && !strings.EqualFold(message.Status, "ok") {
				return "", message.MessageCode, errors.New("server rejected HSM connection")
			}
			return "cn", message.MessageCode, nil
		}
	}
	return "", messages[0].MessageCode, nil
}

func parseThreshold(raw []byte) (uint32, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	if len(raw) > 4 {
		return 0, errors.New("threshold must be at most four bytes")
	}
	if len(raw) > 1 {
		ascii := true
		for _, value := range raw {
			if value < '0' || value > '9' {
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
		value = value<<8 | uint32(b)
	}
	return value, nil
}

func parseDataFrame(payload []byte, ackLimit uint32, topics map[uint32]*topicState, requested string) (int, uint32, error) {
	offset := 0
	var messageNumber uint32
	if ackLimit > 0 {
		if len(payload) < 4 {
			return 0, 0, errors.New("missing message number")
		}
		messageNumber = binary.BigEndian.Uint32(payload[:4])
		offset += 4
	}
	if len(payload)-offset < 2 {
		return 0, 0, errors.New("missing topic packet count")
	}
	count := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	offset += 2
	matched := 0
	for packet := 0; packet < count; packet++ {
		if len(payload)-offset < 2 {
			return matched, messageNumber, errors.New("truncated topic packet length")
		}
		packetLength := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if packetLength < 5 || len(payload)-offset < packetLength {
			return matched, messageNumber, errors.New("truncated topic packet")
		}
		end := offset + packetLength
		kind := payload[offset]
		topicID := binary.BigEndian.Uint32(payload[offset+1 : offset+5])
		offset += 5
		switch kind {
		case 'S':
			state, err := parseSnapshot(payload, &offset)
			if err != nil {
				return matched, messageNumber, err
			}
			topics[topicID] = state
			if state.name == requested {
				if printIndexQuote(state) {
					matched++
				} else {
					fmt.Printf("HSM index snapshot received: %s (waiting for valid LTP)\n", state.name)
				}
			}
		case 'U':
			state, exists := topics[topicID]
			if len(payload)-offset < 1 {
				return matched, messageNumber, errors.New("update is missing value count")
			}
			valueCount := int(payload[offset])
			offset++
			if len(payload)-offset < valueCount*4 {
				return matched, messageNumber, errors.New("update values are truncated")
			}
			values := make([]int32, valueCount)
			for i := range values {
				values[i] = int32(binary.BigEndian.Uint32(payload[offset : offset+4]))
				offset += 4
			}
			if exists {
				for i, value := range values {
					if value != math.MinInt32 {
						if i >= len(state.values) {
							state.values = append(state.values, make([]int32, i+1-len(state.values))...)
						}
						state.values[i] = value
					}
				}
				if state.name == requested && printIndexQuote(state) {
					matched++
				}
			}
		default:
			return matched, messageNumber, fmt.Errorf("unknown topic data kind 0x%x", kind)
		}
		if offset != end {
			return matched, messageNumber, errors.New("topic packet length mismatch")
		}
	}
	if offset != len(payload) {
		return matched, messageNumber, errors.New("unexpected trailing bytes in data frame")
	}
	return matched, messageNumber, nil
}

func parseSnapshot(data []byte, offset *int) (*topicState, error) {
	if len(data)-*offset < 1 {
		return nil, errors.New("snapshot missing topic name length")
	}
	nameLen := int(data[*offset])
	*offset++
	if len(data)-*offset < nameLen+1 {
		return nil, errors.New("snapshot topic name is truncated")
	}
	state := &topicState{name: string(data[*offset : *offset+nameLen])}
	*offset += nameLen
	valueCount := int(data[*offset])
	*offset++
	if len(data)-*offset < valueCount*4+1 {
		return nil, errors.New("snapshot values are truncated")
	}
	state.values = make([]int32, valueCount)
	for i := range state.values {
		state.values[i] = int32(binary.BigEndian.Uint32(data[*offset : *offset+4]))
		*offset += 4
	}
	stringCount := int(data[*offset])
	*offset++
	for i := 0; i < stringCount; i++ {
		if len(data)-*offset < 2 {
			return nil, errors.New("snapshot string field is truncated")
		}
		length := int(data[*offset+1])
		*offset += 2
		if len(data)-*offset < length {
			return nil, errors.New("snapshot string is truncated")
		}
		*offset += length
	}
	return state, nil
}

func printIndexQuote(state *topicState) bool {
	if len(state.values) <= 3 {
		return false
	}
	ltp, ok := fixedIndexPrice(state.values, 2)
	if !ok {
		return false
	}
	close, _ := fixedIndexPrice(state.values, 3)
	change := ltp - close
	changePct := 0.0
	if close != 0 {
		changePct = change / close * 100
	}
	fmt.Printf("INDEX %s | LTP %.2f | close %.2f | change %+.2f (%+.2f%%)\n",
		state.name, ltp, close, change, changePct)
	return true
}

func fixedIndexPrice(values []int32, field int) (float64, bool) {
	if field >= len(values) || values[field] == math.MinInt32 {
		return 0, false
	}
	multiplier := float64(1)
	if len(values) > 8 && values[8] > 0 && values[8] != math.MinInt32 {
		multiplier = float64(values[8])
	}
	precision := int32(2)
	if len(values) > 9 && values[9] >= 0 && values[9] <= 9 && values[9] != math.MinInt32 {
		precision = values[9]
	}
	value := float64(values[field]) / (multiplier * math.Pow10(int(precision)))
	return value, !math.IsNaN(value) && !math.IsInf(value, 0)
}

func writeBinary(conn *websocket.Conn, data []byte) error {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

func closeSocket(conn *websocket.Conn) {
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "probe finished"),
		time.Now().Add(time.Second))
	_ = conn.Close()
}

func validateEndpoint(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid HSM endpoint")
	}
	if parsed.Scheme != "wss" {
		return errors.New("HSM endpoint must use wss://")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "kotaksecurities.com" && !strings.HasSuffix(host, ".kotaksecurities.com") {
		return errors.New("HSM endpoint must use a kotaksecurities.com host")
	}
	return nil
}

func validateInstrument(instrument string) error {
	parts := strings.SplitN(instrument, "|", 3)
	if len(instrument) == 0 || len(instrument) > 255 || len(parts) != 3 ||
		parts[0] != "if" ||
		parts[1] == "" || parts[2] == "" {
		return errors.New("instrument must be an index topic in prefix|exchange|symbol form, e.g. if|nse_cm|Nifty 50")
	}
	return nil
}

func safeNetworkError(err error) string {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op + " " + opErr.Net
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return fmt.Sprintf("WebSocket close code %d", closeErr.Code)
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	if errors.Is(err, io.EOF) {
		return "connection closed"
	}
	return err.Error()
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
