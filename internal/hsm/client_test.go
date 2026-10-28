package hsm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSFeedClientAuthenticatesSubscribesAndDecodesTicks(t *testing.T) {
	quotes := make(chan *Quote, 4)
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

		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read auth frame: %v", err)
			return
		}
		if messageType != websocket.TextMessage {
			t.Errorf("auth message type = %d, want text", messageType)
			return
		}
		var auth map[string]any
		if err := json.Unmarshal(raw, &auth); err != nil {
			t.Errorf("decode auth frame: %v", err)
			return
		}
		for key, want := range map[string]any{
			"user": "test-ucc", "auth": "trade-sid", "format": "native_batch",
			"source": "NEOTRADEAPI", "platform": "Web", "version": "1.2.3",
		} {
			if auth[key] != want {
				t.Errorf("auth[%q] = %v, want %v", key, auth[key], want)
				return
			}
		}
		if auth["sessionValidation"] != false || auth["sdk_version"] != float64(2) {
			t.Errorf("SDK auth fields = %#v", auth)
			return
		}
		if _, ok := auth["conn_req_time"].(float64); !ok {
			t.Errorf("conn_req_time has type %T", auth["conn_req_time"])
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(
			`{"message_code":1119,"format":"native_batch","exchanges":{"nse_cm":{"value":1,"divider":100},"nse_fo":{"value":2,"divider":100}}}`,
		)); err != nil {
			t.Errorf("write auth response: %v", err)
			return
		}

		for _, want := range []struct {
			event string
			input string
		}{
			{"subscribeIndices", "nse_cm|Nifty 50"},
			{"subscribeScrips", "nse_fo|12345,nse_fo|12346"},
		} {
			messageType, raw, err = conn.ReadMessage()
			if err != nil {
				t.Errorf("read %s: %v", want.event, err)
				return
			}
			if messageType != websocket.TextMessage {
				t.Errorf("%s message type = %d, want text", want.event, messageType)
				return
			}
			var request map[string]any
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Errorf("decode %s: %v", want.event, err)
				return
			}
			if request["event"] != want.event || request["inputtoken"] != want.input ||
				request["ack_symbol"] != true {
				t.Errorf("%s request = %#v", want.event, request)
				return
			}
			var data []byte
			if want.event == "subscribeIndices" {
				data = testIndexPacket()
			} else {
				data = append(testMarketPicturePacket(), testMiniTouchPacket()...)
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				t.Errorf("write %s feed data: %v", want.event, err)
				return
			}
			ack := `{"message_code":1109,"trading_symbols":{"nse_cm|Nifty 50":"Nifty 50"}}`
			if want.event == "subscribeScrips" {
				ack = `{"message_code":1109,"trading_symbols":{"nse_fo|12345":"NIFTY-TEST-CE","nse_fo|12346":"NIFTY-TEST-PE"}}`
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(ack)); err != nil {
				t.Errorf("write %s acknowledgement: %v", want.event, err)
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewHSMClientWithLogger(endpoint, quotes,
		slog.New(slog.NewTextHandler(testDiscard{}, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Connect(ctx, "test-ucc", "trade-sid"); err != nil {
		t.Fatal(err)
	}
	if err := client.SubscribeIndices([]string{"nse_cm|Nifty 50"}); err != nil {
		t.Fatal(err)
	}
	if err := client.SubscribeScrips([]string{"nse_fo|12345", "nse_fo|12346"}); err != nil {
		t.Fatal(err)
	}

	quotesByKey := make(map[string]*Quote, 3)
	for range 3 {
		quote := receiveQuote(t, quotes)
		quotesByKey[quote.InstrumentKey] = quote
	}
	index := quotesByKey["if|nse_cm|Nifty 50"]
	option := quotesByKey["sf|nse_fo|12345"]
	mini := quotesByKey["sf|nse_fo|12346"]
	if index == nil || option == nil || mini == nil {
		t.Fatalf("received unexpected SFeed quotes: %+v", quotesByKey)
	}
	if index.InstrumentKey != "if|nse_cm|Nifty 50" || index.Identifier != "Nifty 50" ||
		index.LTP != 25250 || index.PreviousClose != 25100 || index.Change != 150 ||
		index.ChangePct != 0.5 || index.Timestamp != 1_761_566_400 {
		t.Fatalf("index quote = %+v", index)
	}
	if option.InstrumentKey != "sf|nse_fo|12345" || option.Identifier != "NIFTY-TEST-CE" ||
		option.LTP != 12345.5 || option.PreviousClose != 12000 ||
		option.Change != 123.45 || option.ChangePct != 1.25 ||
		option.Volume != 1000 || option.OI != 5000 ||
		option.Bid != 12345 || option.Ask != 12346 ||
		option.Timestamp != 1_761_566_400 {
		t.Fatalf("option quote = %+v", option)
	}
	if mini.InstrumentKey != "sf|nse_fo|12346" || mini.Identifier != "NIFTY-TEST-PE" ||
		mini.LTP != 12350 || mini.PreviousClose != 12000 || mini.Change != 350 ||
		mini.ChangePct != 1.5 || mini.Timestamp != 1_761_566_400 {
		t.Fatalf("mini-touch quote = %+v", mini)
	}
	client.Stop()
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("SFeed test server did not stop")
	}
}

func TestSplitSFeedBatchRejectsTruncatedPackets(t *testing.T) {
	for _, frame := range [][]byte{
		{0x01},
		{0x08, 0x00, 0, 0, 0, 0, 0, 0, 0, 0},
		{0x0b, 0x00, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		if _, err := splitSFeedBatch(frame); err == nil {
			t.Fatalf("accepted malformed SFeed batch %x", frame)
		}
	}
}

func TestSFeedAuthenticationRejectsFallbackAndUnexpectedResponses(t *testing.T) {
	client := NewHSMClientWithLogger(DefaultURL, nil, slog.New(slog.NewTextHandler(testDiscard{}, nil)))
	if err := client.handleTextMessage([]byte(`{"message_code":1119,"format":"native_fallback"}`)); err == nil {
		t.Fatal("accepted native_fallback auth")
	}
	if err := client.handleTextMessage([]byte(`{"message_code":500}`)); err == nil {
		t.Fatal("accepted unexpected auth response")
	}
	if client.IsConnected() {
		t.Fatal("rejected auth response marked client connected")
	}
}

func TestSFeedClientDoesNotLeakServerCloseReason(t *testing.T) {
	err := sanitizedConnectionError(&websocket.CloseError{
		Code: websocket.ClosePolicyViolation,
		Text: "trade token must remain private",
	})
	if !strings.Contains(err.Error(), "close code 1008") ||
		strings.Contains(err.Error(), "trade token") {
		t.Fatalf("sanitized error = %v", err)
	}
}

func TestSFeedSubscriptionLimitAndTokenValidation(t *testing.T) {
	client := NewHSMClientWithLogger(DefaultURL, nil, slog.New(slog.NewTextHandler(testDiscard{}, nil)))
	client.connected = true
	tokens := make([]string, maxSFeedTokens+1)
	for i := range tokens {
		tokens[i] = "nse_fo|" + strconv.Itoa(i)
	}
	if err := client.writeSubscription("subscribeScrips", "scrips", tokens, true); err == nil {
		t.Fatal("accepted more than the maximum subscriptions")
	}
	if len(client.subs) != 0 {
		t.Fatalf("failed subscription left state behind: %d", len(client.subs))
	}
	for _, token := range []string{"nse_fo|", "|123", "nse_fo|1,2", "nse_fo|1\n2"} {
		if err := validateSFeedToken(token); err == nil {
			t.Errorf("accepted invalid token %q", token)
		}
	}
}

func TestSFeedURLValidation(t *testing.T) {
	for _, endpoint := range []string{
		"http://sfeed.kotaksecurities.com/apifeed",
		"wss://kotaksecurities.com.attacker.example/apifeed",
		"wss://sfeed.kotaksecurities.com/apifeed?token=secret",
	} {
		if err := validateSFeedURL(endpoint); err == nil {
			t.Errorf("accepted unsafe endpoint %q", endpoint)
		}
	}
	if err := validateSFeedURL(DefaultURL); err != nil {
		t.Fatalf("rejected default SFeed URL: %v", err)
	}
}

func testIndexPacket() []byte {
	body := make([]byte, 78)
	binary.LittleEndian.PutUint32(body[0:4], 26000)
	binary.LittleEndian.PutUint32(body[4:8], 2_500_000)
	binary.LittleEndian.PutUint32(body[8:12], 2_510_000)
	binary.LittleEndian.PutUint32(body[12:16], 2_530_000)
	binary.LittleEndian.PutUint32(body[16:20], 2_490_000)
	binary.LittleEndian.PutUint32(body[20:24], 2_525_000)
	binary.LittleEndian.PutUint64(body[24:32], 1_761_566_400)
	binary.LittleEndian.PutUint32(body[40:44], 50)
	copy(body[57:78], "Nifty 50")
	return testSFeedPacket(sfeedIndexCode, 1, 0, body)
}

func testMarketPicturePacket() []byte {
	body := make([]byte, 135)
	binary.LittleEndian.PutUint32(body[0:4], 12345)
	binary.LittleEndian.PutUint64(body[20:28], 1000)
	binary.LittleEndian.PutUint64(body[28:36], 1_761_566_400)
	binary.LittleEndian.PutUint64(body[36:44], 1_761_566_400)
	binary.LittleEndian.PutUint32(body[44:48], 1_220_000)
	binary.LittleEndian.PutUint32(body[48:52], 1_200_000)
	binary.LittleEndian.PutUint32(body[52:56], 1_240_000)
	binary.LittleEndian.PutUint32(body[56:60], 1_190_000)
	binary.LittleEndian.PutUint32(body[60:64], 1_234_550)
	binary.LittleEndian.PutUint32(body[80:84], 1)
	binary.LittleEndian.PutUint32(body[84:88], 1)
	binary.LittleEndian.PutUint32(body[90:94], 125)
	binary.LittleEndian.PutUint64(body[94:102], 5000)
	binary.LittleEndian.PutUint32(body[106:110], 12345)

	packet := testSFeedPacket(sfeedMarketPictureCode, 2, sfeedTouchLevel, body)
	buy := make([]byte, 16)
	binary.LittleEndian.PutUint64(buy[0:8], 10)
	binary.LittleEndian.PutUint32(buy[8:12], 1_234_500)
	binary.LittleEndian.PutUint32(buy[12:16], 3)
	sell := make([]byte, 16)
	binary.LittleEndian.PutUint64(sell[0:8], 11)
	binary.LittleEndian.PutUint32(sell[8:12], 1_234_600)
	binary.LittleEndian.PutUint32(sell[12:16], 4)
	packet = append(packet, buy...)
	packet = append(packet, sell...)
	binary.LittleEndian.PutUint16(packet[:2], uint16(len(packet)))
	return packet
}

func testMiniTouchPacket() []byte {
	body := make([]byte, 45)
	binary.LittleEndian.PutUint32(body[0:4], 12346)
	binary.LittleEndian.PutUint64(body[4:12], 1_761_566_400)
	binary.LittleEndian.PutUint32(body[12:16], 1_235_000)
	binary.LittleEndian.PutUint64(body[16:24], 25)
	binary.LittleEndian.PutUint32(body[24:28], 1_200_000)
	binary.LittleEndian.PutUint32(body[28:32], 150)
	binary.LittleEndian.PutUint32(body[32:36], 35_000)
	return testSFeedPacket(sfeedMarketPictureCode, 2, sfeedMiniTouchLevel, body)
}

func testSFeedPacket(messageCode uint16, exchangeID, level uint8, body []byte) []byte {
	packet := make([]byte, sfeedHeaderSize+len(body))
	binary.LittleEndian.PutUint16(packet[:2], uint16(len(packet)))
	binary.LittleEndian.PutUint16(packet[2:4], messageCode)
	packet[4] = exchangeID
	packet[5] = level
	copy(packet[sfeedHeaderSize:], body)
	return packet
}

func receiveQuote(t *testing.T, quotes <-chan *Quote) *Quote {
	t.Helper()
	select {
	case quote := <-quotes:
		return quote
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SFeed quote")
		return nil
	}
}

type testDiscard struct{}

func (testDiscard) Write(p []byte) (int, error) { return len(p), nil }
