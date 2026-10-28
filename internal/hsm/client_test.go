package hsm

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHSMClientAuthenticatesSubscribesAndDecodesTicks(t *testing.T) {
	quoteCh := make(chan *Quote, 4)
	upgrader := websocket.Upgrader{}
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		expectedOrigin := "http://" + r.Host
		if got := r.Header.Get("Origin"); got != expectedOrigin {
			t.Errorf("Origin = %q, want %q", got, expectedOrigin)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(
			`{"message_code":1119,"id":"SFD_EPROD_702","version":1,"exchanges":{"nse_cm":{"value":1,"divider":100}},"timestamp":"2026-10-28T14:37:24.569205019+05:30"}`,
		)); err != nil {
			t.Errorf("write server metadata: %v", err)
			return
		}
		_, request, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read connect request: %v", err)
			return
		}
		frame, err := ParseFrame(request)
		if err != nil || frame.Header.Type != byte(ConnectType) ||
			string(frame.Fields[1]) != "trade-token" || string(frame.Fields[2]) != "trade-sid" ||
			string(frame.Fields[3]) != "JS_API" {
			t.Errorf("connect frame = %+v, %v", frame, err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`[{"type":"cn"}]`)); err != nil {
			t.Errorf("write connect response: %v", err)
			return
		}

		_, request, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read subscription: %v", err)
			return
		}
		frame, err = ParseFrame(request)
		if err != nil || frame.Header.Type != byte(SubscribeType) || string(frame.Fields[2]) != "\x01" {
			t.Errorf("subscribe frame = %+v, %v", frame, err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, testDataFrame(true, 12_345)); err != nil {
			t.Errorf("write snapshot: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, testDataFrame(false, 12_555)); err != nil {
			t.Errorf("write update: %v", err)
			return
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewHSMClientWithLogger(endpoint, quoteCh,
		slog.New(slog.NewTextHandler(testDiscard{}, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Connect(ctx, "trade-token", "trade-sid"); err != nil {
		t.Fatal(err)
	}
	if err := client.Subscribe([]string{"sf|nse_fo|12345"}); err != nil {
		t.Fatal(err)
	}
	first := receiveQuote(t, quoteCh)
	second := receiveQuote(t, quoteCh)
	if first.InstrumentKey != "sf|nse_fo|12345" || first.LTP != 123.45 ||
		first.PreviousClose != 120 || first.Volume != 100 || first.OI != 500 ||
		first.Bid != 123.4 || first.Ask != 123.5 || first.Timestamp != 1_761_566_400 {
		t.Fatalf("snapshot quote = %+v", first)
	}
	if second.LTP != 125.55 || second.PreviousClose != 120 {
		t.Fatalf("update quote = %+v", second)
	}
	client.Stop()
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("HSM test server did not stop")
	}
}

func TestParseFrameRejectsTruncationAndLengthMismatch(t *testing.T) {
	if _, err := ParseFrame([]byte{0, 3, 1}); err == nil {
		t.Fatal("accepted truncated frame")
	}
	if _, err := ParseFrame([]byte{0, 5, 1, 0}); err == nil {
		t.Fatal("accepted frame length mismatch")
	}
}

func TestEncodeConnectionRequestMatchesSDKWireFormat(t *testing.T) {
	frame, err := encodeRequest(ConnectType, []requestField{
		{id: 1, value: []byte("T")},
		{id: 2, value: []byte("S")},
		{id: 3, value: []byte("JS_API")},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x00, 0x13, 0x01, 0x03,
		0x01, 0x00, 0x01, 'T',
		0x02, 0x00, 0x01, 'S',
		0x03, 0x00, 0x06, 'J', 'S', '_', 'A', 'P', 'I',
	}
	if !bytes.Equal(frame, want) {
		t.Fatalf("connection frame = % x, want % x", frame, want)
	}
}

func TestHSMClientAcceptsTextConnectionAcknowledgements(t *testing.T) {
	for _, response := range []string{
		`[{"type":"cn"}]`,
		`{"type":"cn","status":"success"}`,
	} {
		t.Run(response, func(t *testing.T) {
			client := NewHSMClientWithLogger(DefaultURL, nil,
				slog.New(slog.NewTextHandler(testDiscard{}, nil)))
			if err := client.handleMessage(websocket.TextMessage, []byte(response)); err != nil {
				t.Fatal(err)
			}
			if !client.IsConnected() {
				t.Fatal("text connection acknowledgement did not mark client connected")
			}
			select {
			case <-client.WaitForConnection():
			default:
				t.Fatal("connection acknowledgement did not notify waiters")
			}
		})
	}
}

func TestHSMClientAcceptsServerMetadataBeforeConnectionAcknowledgement(t *testing.T) {
	client := NewHSMClientWithLogger(DefaultURL, nil,
		slog.New(slog.NewTextHandler(testDiscard{}, nil)))
	metadata := []byte(
		`{"message_code":1119,"id":"SFD_EPROD_702","version":1,"exchanges":{"nse_cm":{"value":1,"divider":100},"nse_fo":{"value":2,"divider":100}},"timestamp":"2026-10-28T14:37:24.569205019+05:30"}`,
	)
	if err := client.handleMessage(websocket.TextMessage, metadata); err != nil {
		t.Fatalf("handle server metadata: %v", err)
	}
	if client.IsConnected() {
		t.Fatal("server metadata incorrectly marked client connected")
	}
	select {
	case <-client.WaitForConnection():
		t.Fatal("server metadata notified connection waiters")
	default:
	}
	if err := client.handleMessage(websocket.TextMessage, []byte(`[{"type":"cn"}]`)); err != nil {
		t.Fatalf("handle connection acknowledgement after metadata: %v", err)
	}
	if !client.IsConnected() {
		t.Fatal("connection acknowledgement did not mark client connected")
	}
}

func TestHSMClientHandshakeLogsSafeResponseSummary(t *testing.T) {
	var output bytes.Buffer
	client := NewHSMClientWithLogger(DefaultURL, nil,
		slog.New(slog.NewTextHandler(&output, nil)))
	client.logIncomingMessage(websocket.TextMessage, []byte(
		`{"message_code":1119,"exchanges":{"nse_cm":{}},"token":"sensitive-token","sid":"sensitive-sid"}`,
	))
	log := output.String()
	for _, expected := range []string{
		"event=hsm_handshake_response",
		"message_code=1119",
		"exchange_count=1",
	} {
		if !strings.Contains(log, expected) {
			t.Errorf("log does not contain %q: %s", expected, log)
		}
	}
	for _, secret := range []string{"sensitive-token", "sensitive-sid"} {
		if strings.Contains(log, secret) {
			t.Errorf("log contains sensitive value %q", secret)
		}
	}
}

func TestHSMClientReturnsSanitizedCloseDuringAuthentication(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"message_code":1119,"exchanges":{"nse_cm":{}}}`)); err != nil {
			t.Errorf("write metadata: %v", err)
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read auth frame: %v", err)
			return
		}
		if err := conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "sensitive server detail"),
			time.Now().Add(time.Second)); err != nil {
			t.Errorf("send close: %v", err)
		}
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewHSMClientWithLogger(endpoint, nil,
		slog.New(slog.NewTextHandler(testDiscard{}, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := client.Connect(ctx, "trade-token", "trade-sid")
	if err == nil {
		t.Fatal("expected authentication connection to fail after server close")
	}
	if !strings.Contains(err.Error(), "close code 1008") {
		t.Fatalf("error does not identify close code: %v", err)
	}
	if strings.Contains(err.Error(), "sensitive server detail") {
		t.Fatalf("error includes untrusted close reason: %v", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("HSM test server did not stop")
	}
}

func TestHSMClientRejectsTextConnectionAcknowledgementFailure(t *testing.T) {
	client := NewHSMClientWithLogger(DefaultURL, nil,
		slog.New(slog.NewTextHandler(testDiscard{}, nil)))
	err := client.handleMessage(websocket.TextMessage,
		[]byte(`[{"type":"error","message":"trade-token"}]`))
	if err == nil {
		t.Fatal("accepted failed text connection acknowledgement")
	}
	if strings.Contains(err.Error(), "trade-token") {
		t.Fatal("text response error included a credential-bearing message")
	}
	if client.IsConnected() {
		t.Fatal("failed text connection acknowledgement marked the client connected")
	}
}

func TestEncodeScripBlobLimitsAndFormat(t *testing.T) {
	blob, err := encodeScripBlob([]string{"if|nse_cm|Nifty 50", "sf|nse_fo|12345"})
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(blob[:2]) != 2 || string(blob[3:3+len("if|nse_cm|Nifty 50")]) != "if|nse_cm|Nifty 50" {
		t.Fatalf("scrip blob = %x", blob)
	}
	if _, err := encodeScripBlob([]string{"nse_fo|12345"}); err == nil {
		t.Fatal("accepted a scrip without an HSM topic prefix")
	}
	if _, err := encodeScripBlob([]string{""}); err == nil {
		t.Fatal("accepted empty scrip")
	}
}

func receiveQuote(t *testing.T, quotes <-chan *Quote) *Quote {
	t.Helper()
	select {
	case quote := <-quotes:
		return quote
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for HSM quote")
		return nil
	}
}

func testDataFrame(snapshot bool, ltp int32) []byte {
	const topicID = uint32(9)
	var packet []byte
	if snapshot {
		name := "sf|nse_fo|12345"
		packet = append(packet, 'S')
		id := make([]byte, 4)
		binary.BigEndian.PutUint32(id, topicID)
		packet = append(packet, id...)
		packet = append(packet, byte(len(name)))
		packet = append(packet, name...)
		packet = append(packet, 25)
		values := make([]int32, 25)
		for i := range values {
			values[i] = trashValue
		}
		values[3] = 1_761_566_400
		values[4] = 100
		values[5] = ltp
		values[9] = 12_340
		values[10] = 12_350
		values[21] = 12_000
		values[22] = 500
		values[23] = 1
		values[24] = 2
		for _, value := range values {
			raw := make([]byte, 4)
			binary.BigEndian.PutUint32(raw, uint32(value))
			packet = append(packet, raw...)
		}
		packet = append(packet, 1, 52, 5)
		packet = append(packet, "12345"...)
	} else {
		packet = append(packet, 'U')
		id := make([]byte, 4)
		binary.BigEndian.PutUint32(id, topicID)
		packet = append(packet, id...)
		packet = append(packet, 25)
		for i := 0; i < 25; i++ {
			value := trashValue
			if i == 5 {
				value = ltp
			}
			raw := make([]byte, 4)
			binary.BigEndian.PutUint32(raw, uint32(value))
			packet = append(packet, raw...)
		}
	}
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, 1)
	packetLength := make([]byte, 2)
	binary.BigEndian.PutUint16(packetLength, uint16(len(packet)))
	payload = append(payload, packetLength...)
	payload = append(payload, packet...)
	frame := make([]byte, 3, 3+len(payload))
	binary.BigEndian.PutUint16(frame[:2], uint16(1+len(payload)))
	frame[2] = byte(DataType)
	return append(frame, payload...)
}

type testDiscard struct{}

func (testDiscard) Write(p []byte) (int, error) { return len(p), nil }
