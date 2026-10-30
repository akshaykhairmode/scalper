package orderfeed

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestFeedURL(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		want    string
		wantErr bool
	}{
		{
			name: "valid cis base",
			base: "https://cis.kotaksecurities.com",
			want: "wss://cis.kotaksecurities.com/realtime",
		},
		{
			name: "valid with trailing slash",
			base: "https://cis.kotaksecurities.com/",
			want: "wss://cis.kotaksecurities.com/realtime",
		},
		{
			name: "valid trade api subdomain",
			base: "https://tradeapi.kotaksecurities.com",
			want: "wss://tradeapi.kotaksecurities.com/realtime",
		},
		{
			name: "exact apex domain",
			base: "https://kotaksecurities.com",
			want: "wss://kotaksecurities.com/realtime",
		},
		{
			name:    "invalid scheme http",
			base:    "http://cis.kotaksecurities.com",
			wantErr: true,
		},
		{
			name:    "invalid untrusted host",
			base:    "https://example.com",
			wantErr: true,
		},
		{
			name:    "untrusted domain suffix spoof",
			base:    "https://kotaksecurities.com.attacker.com",
			wantErr: true,
		},
		{
			name:    "prefix spoof without dot",
			base:    "https://fakekotaksecurities.com",
			wantErr: true,
		},
		{
			name:    "userinfo not allowed",
			base:    "https://admin:secret@cis.kotaksecurities.com",
			wantErr: true,
		},
		{
			name:    "empty string",
			base:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := feedURL(tt.base)
			if (err != nil) != tt.wantErr {
				t.Fatalf("feedURL(%q) err = %v, wantErr = %v", tt.base, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("feedURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func TestClientStartValidation(t *testing.T) {
	c := New(nil)

	if err := c.Start(context.Background(), Session{BaseURL: "https://cis.kotaksecurities.com", SID: "", Token: "tok"}); err == nil {
		t.Fatal("expected error for empty SID")
	}

	if err := c.Start(context.Background(), Session{BaseURL: "https://cis.kotaksecurities.com", SID: "sid", Token: ""}); err == nil {
		t.Fatal("expected error for empty Token")
	}

	if err := c.Start(context.Background(), Session{BaseURL: "https://attacker.com", SID: "sid", Token: "tok"}); err == nil {
		t.Fatal("expected error for untrusted base URL")
	}
}

func TestClientLifecycleAndDefaults(t *testing.T) {
	c := New(nil)
	if c.Connected() {
		t.Fatal("expected client to not be connected initially")
	}
	if c.Events() == nil {
		t.Fatal("expected non-nil events channel")
	}

	// Stop on unstarted client must not panic.
	c.Stop()
	c.Stop()
}

func TestClientStartAndStop(t *testing.T) {
	c := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Start(ctx, Session{
		BaseURL: "https://cis.kotaksecurities.com",
		SID:     "test-sid",
		Token:   "test-token",
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Stop must cleanly cancel the runner
	c.Stop()
	if c.Connected() {
		t.Fatal("client should not report connected after stop")
	}
}

func TestEmitNonBlockingOnFullBuffer(t *testing.T) {
	c := New(nil)
	// Fill events channel to capacity (256)
	for i := 0; i < 256; i++ {
		c.emit(Event{Type: "fill"})
	}

	// Emitting to full channel must not block or deadlock.
	done := make(chan struct{})
	go func() {
		c.emit(Event{Type: "overflow"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("c.emit blocked on full channel")
	}
}

type inMemoryListener struct {
	conns     chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

func newInMemoryListener() *inMemoryListener {
	return &inMemoryListener{
		conns: make(chan net.Conn, 8),
		done:  make(chan struct{}),
	}
}

func (l *inMemoryListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	case conn, ok := <-l.conns:
		if !ok {
			return nil, net.ErrClosed
		}
		return conn, nil
	}
}

func (l *inMemoryListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
	})
	return nil
}

type dummyAddr struct{}

func (dummyAddr) Network() string { return "pipe" }
func (dummyAddr) String() string  { return "pipe" }

func (l *inMemoryListener) Addr() net.Addr { return dummyAddr{} }

func setupMockOrderFeedServer(t *testing.T) (*websocket.Conn, *websocket.Conn, func()) {
	t.Helper()
	l := newInMemoryListener()
	serverConnChan := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/realtime", func(w http.ResponseWriter, r *http.Request) {
		ws, upgradeErr := upgrader.Upgrade(w, r, nil)
		if upgradeErr != nil {
			t.Errorf("upgrade error: %v", upgradeErr)
			return
		}
		serverConnChan <- ws
	})

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(l) }()

	dialer := websocket.Dialer{
		NetDialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			c1, c2 := net.Pipe()
			select {
			case <-l.done:
				_ = c1.Close()
				_ = c2.Close()
				return nil, net.ErrClosed
			case l.conns <- c2:
				return c1, nil
			}
		},
	}

	clientConn, resp, err := dialer.Dial("ws://pipe/realtime", nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		_ = l.Close()
		_ = server.Close()
		t.Fatalf("dial client: %v", err)
	}

	var serverConn *websocket.Conn
	select {
	case serverConn = <-serverConnChan:
	case <-time.After(2 * time.Second):
		_ = clientConn.Close()
		_ = l.Close()
		_ = server.Close()
		t.Fatal("timeout waiting for server websocket connection")
	}

	cleanup := func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		_ = server.Close()
		_ = l.Close()
	}

	return clientConn, serverConn, cleanup
}

func TestReadConnectionHandshakeAndEvents(t *testing.T) {
	clientConn, serverConn, cleanup := setupMockOrderFeedServer(t)
	defer cleanup()

	c := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readDone := make(chan error, 1)
	go func() {
		readDone <- c.readConnection(ctx, clientConn, Session{
			BaseURL: "https://cis.kotaksecurities.com",
			SID:     "test-sid",
			Token:   "test-token",
		})
	}()

	// 1. Verify login frame sent by client.
	msgType, raw, err := serverConn.ReadMessage()
	if err != nil {
		t.Fatalf("server read login frame: %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("expected text message, got %d", msgType)
	}
	expectedLogin := "{type:cn,Authorization:test-token,Sid:test-sid,src:WEB}"
	if string(raw) != expectedLogin {
		t.Fatalf("login frame = %q, want %q", string(raw), expectedLogin)
	}

	// 2. Client must emit status: live and mark connected.
	select {
	case event := <-c.Events():
		if event.Status != "live" {
			t.Fatalf("expected status live, got %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for live status event")
	}
	if !c.Connected() {
		t.Fatal("client should report connected=true")
	}

	// 3. Send order event from server.
	orderPayload := []byte(`{"type":"order","data":{"order_id":"ord-001","status":"TRAD"}}`)
	if err := serverConn.WriteMessage(websocket.TextMessage, orderPayload); err != nil {
		t.Fatalf("server write order: %v", err)
	}
	select {
	case event := <-c.Events():
		if event.Type != "order" {
			t.Fatalf("expected event type order, got %q", event.Type)
		}
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil || data["order_id"] != "ord-001" {
			t.Fatalf("unexpected order data: %s", string(event.Data))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for order event")
	}

	// 4. Send position event from server.
	posPayload := []byte(`{"type":"position","data":{"symbol":"NIFTY","qty":50}}`)
	if err := serverConn.WriteMessage(websocket.TextMessage, posPayload); err != nil {
		t.Fatalf("server write position: %v", err)
	}
	select {
	case event := <-c.Events():
		if event.Type != "position" {
			t.Fatalf("expected event type position, got %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for position event")
	}

	// 5. Send ignored events: "cn", "ak", unknown, or invalid json.
	if err := serverConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"cn"}`)); err != nil {
		t.Fatalf("write cn: %v", err)
	}
	if err := serverConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ak"}`)); err != nil {
		t.Fatalf("write ak: %v", err)
	}
	if err := serverConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"unknown"}`)); err != nil {
		t.Fatalf("write unknown: %v", err)
	}
	if err := serverConn.WriteMessage(websocket.TextMessage, []byte(`not json`)); err != nil {
		t.Fatalf("write non-json: %v", err)
	}

	// Send another order to confirm stream is still intact and ignored messages didn't pollute events.
	if err := serverConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"order","data":{"order_id":"ord-002"}}`)); err != nil {
		t.Fatalf("write ord-002: %v", err)
	}
	select {
	case event := <-c.Events():
		if event.Type != "order" || !strings.Contains(string(event.Data), "ord-002") {
			t.Fatalf("expected ord-002, got %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ord-002 event")
	}

	// 6. Close server connection and ensure readConnection returns.
	_ = serverConn.Close()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("expected error on server close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readConnection did not exit after server closed connection")
	}
}

func TestReadConnectionContextDone(t *testing.T) {
	clientConn, serverConn, cleanup := setupMockOrderFeedServer(t)
	defer cleanup()

	c := New(nil)
	ctx, cancel := context.WithCancel(context.Background())

	readDone := make(chan error, 1)
	go func() {
		readDone <- c.readConnection(ctx, clientConn, Session{
			BaseURL: "https://cis.kotaksecurities.com",
			SID:     "test-sid",
			Token:   "test-token",
		})
	}()

	// Read login frame
	if _, _, err := serverConn.ReadMessage(); err != nil {
		t.Fatalf("server read login: %v", err)
	}

	// Cancel context
	cancel()

	select {
	case err := <-readDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readConnection did not exit on context cancellation")
	}
}
