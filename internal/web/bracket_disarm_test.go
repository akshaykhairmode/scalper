package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/akshaykhairmode/scalper/internal/auth"
	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/scrip"
	"github.com/akshaykhairmode/scalper/internal/store"
)

const liveDisarmKey = "nse_fo|NIFTY|2026-11-26|25000|CE"

func newLiveDisarmTestServer(t *testing.T, transport http.RoundTripper) *Server {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{
		Path:   filepath.Join(t.TempDir(), "live.db"),
		Logger: testLogger(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := testConfig()
	cfg.Trading.ConfirmLiveOrders = false
	cfg.Trading.Margin.CheckBeforeOrder = false
	cfg.Trading.Risk.MaxDailyLossINR = 0
	cfg.Trading.Risk.MaxLotsPerOrder = 0
	cfg.Trading.Risk.MaxOpenLotsPerIndex = 0
	cfg.Trading.Risk.MaxOrderValueINR = 0
	cfg.Trading.Risk.BlockOnDataStaleMS = 0

	instrument := scrip.Option{
		InstrumentKey:   liveDisarmKey,
		ExchangeSegment: "nse_fo",
		PSymbol:         "NIFTY26NOV25000CE",
		PTrdSymbol:      "NIFTY26NOV25000CE",
		Underlying:      "NIFTY",
		Expiry:          "2026-11-26",
		StrikePaise:     2500000,
		OptionType:      scrip.Call,
		LotSize:         75,
	}
	client := kotakapi.NewClient(nil, transport)
	s := newTestServer(t, serverOpts{
		db:           st,
		cfg:          &cfg,
		kotak:        client,
		consumerKey:  "ck-live",
		bracketStore: st.BracketsFor("live"),
		marketAPI: fakeMarketAPI{
			quote:      market.Quote{LTP: 100, Bid: 99.9, Ask: 100.1},
			instrument: instrument,
		},
	})
	if _, err := s.a.SessionManager().NewBrokerSession(auth.TradeSession{
		Token:   "trade-token",
		SID:     "trade-sid",
		BaseURL: "https://cis.kotaksecurities.com",
	}, auth.LoginMethodTOTP); err != nil {
		t.Fatalf("broker session: %v", err)
	}
	return s
}

func TestLiveManualExitDisarmsBracket(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		switch {
		case strings.Contains(req.URL.Path, "portfolio/v2/positions"):
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write([]byte(`{"stat":"Ok","data":[]}`))
		case strings.Contains(req.URL.Path, "quick/order/rule/ms/place"):
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write([]byte(`{"stat":"Ok","nOrdNo":"ord-exit-1"}`))
		case strings.Contains(req.URL.Path, "quick/user/orders/ord-exit-1"):
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write([]byte(`{"stat":"Ok","data":[{"order_id":"ord-exit-1","status":"COMPLETE","avg_price":105}]}`))
		default:
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write([]byte(`{"stat":"Ok","data":[]}`))
		}
		return rec.Result(), nil
	})
	s := newLiveDisarmTestServer(t, transport)
	defer s.Close()

	ctx := context.Background()
	b, err := s.bracketEngine.Create(ctx, bracket.Bracket{
		ID:              "brk-live-exit",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   liveDisarmKey,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   90,
		Status:          bracket.StatusInPosition,
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}

	body := []byte(`{"instrument_key":"` + liveDisarmKey + `","exchange_segment":"nse_fo","trading_symbol":"NIFTY26NOV25000CE","transaction_type":"S","quantity":75,"order_type":"MARKET","product":"MIS","validity":"DAY","intent":"EXIT","mode":"live","confirm":true}`)
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/broker/orders", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("place exit status = %d: %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, ok := s.bracketEngine.Get(b.ID)
		if ok && got.Status == bracket.StatusExitedManual {
			if got.ExitPrice != 105 {
				t.Fatalf("exit price = %v, want 105", got.ExitPrice)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, _ := s.bracketEngine.Get(b.ID)
	t.Fatalf("bracket was not disarmed after the exit fill: %+v", got)
}

func TestLiveModifyUpdatesBracketEntryPrice(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		switch {
		case strings.Contains(req.URL.Path, "quick/order/vr/modify"):
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write([]byte(`{"stat":"Ok"}`))
		default:
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write([]byte(`{"stat":"Ok","data":[]}`))
		}
		return rec.Result(), nil
	})
	s := newLiveDisarmTestServer(t, transport)
	defer s.Close()

	ctx := context.Background()
	b, err := s.bracketEngine.Create(ctx, bracket.Bracket{
		ID:              "brk-live-modify",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   liveDisarmKey,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   90,
		EntryOrderID:    "ord-entry-1",
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}

	body := []byte(`{"exchange_segment":"nse_fo","trading_symbol":"NIFTY26NOV25000CE","price":95,"quantity":75,"order_type":"L","validity":"DAY","mode":"live","confirm":true}`)
	rec := do(t, s, localRequestWithBody(http.MethodPatch, "/api/broker/orders/ord-entry-1", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("modify status = %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := s.bracketEngine.Get(b.ID)
	if got.EntryPrice != 95 {
		t.Fatalf("entry price = %v, want 95", got.EntryPrice)
	}
}

func TestLiveSquareOffRearmsBracketWhenCloseFails(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		switch {
		case strings.Contains(req.URL.Path, "portfolio/v2/positions"):
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write([]byte(`{"stat":"Ok","data":[{"exchange_segment":"nse_fo","trading_symbol":"NIFTY26NOV25000CE","quantity":75,"product":"MIS"}]}`))
		case strings.Contains(req.URL.Path, "quick/order/rule/ms/place"):
			rec.WriteHeader(http.StatusBadRequest)
			_, _ = rec.Write([]byte(`{"stat":"NotOk","errMsg":"rejected"}`))
		default:
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write([]byte(`{"stat":"Ok","data":[]}`))
		}
		return rec.Result(), nil
	})
	s := newLiveDisarmTestServer(t, transport)
	defer s.Close()

	ctx := context.Background()
	b, err := s.bracketEngine.Create(ctx, bracket.Bracket{
		ID:              "brk-squareoff",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   liveDisarmKey,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   90,
		Status:          bracket.StatusInPosition,
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}

	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/broker/square-off-all?mode=live",
		[]byte(`{"confirm":true,"mode":"live"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("square-off status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp brokerSquareOffAllResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Fatal("square-off should report failure when the close order is rejected")
	}

	got, ok := s.bracketEngine.Get(b.ID)
	if !ok {
		t.Fatal("bracket missing after square-off")
	}
	if got.Status != bracket.StatusInPosition {
		t.Fatalf("bracket status = %s, want IN_POSITION (re-armed)", got.Status)
	}
}

func newDisarmEngine(t *testing.T) *bracket.Engine {
	t.Helper()
	return bracket.NewEngine(nil, nil, testLogger())
}

func createLiveBracket(t *testing.T, engine *bracket.Engine, id, product string, created time.Time) *bracket.Bracket {
	t.Helper()
	b, err := engine.Create(context.Background(), bracket.Bracket{
		ID: id, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		InstrumentKey: liveDisarmKey, TransactionType: "BUY", Quantity: 50, Product: product,
		EntryPrice: 100, TargetPrice: 110, StopLossPrice: 90, Status: bracket.StatusInPosition,
		CreatedAt: created,
	})
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	return b
}

func TestPartialExitDoesNotDisarmRemainingBracket(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()
	engine := newDisarmEngine(t)
	s.bracketEngine = engine
	ctx := context.Background()
	now := time.Now()
	createLiveBracket(t, engine, "brk-first", "MIS", now.Add(-time.Minute))
	createLiveBracket(t, engine, "brk-second", "MIS", now)

	s.disarmLiveBracketsForExit(ctx, "nse_fo", "NIFTY26NOV25000CE", "MIS", "SELL", 50, 100)

	var exited, armed int
	for _, id := range []string{"brk-first", "brk-second"} {
		b, _ := engine.Get(id)
		switch b.Status {
		case bracket.StatusExitedManual:
			exited++
		case bracket.StatusInPosition:
			armed++
		}
	}
	if exited != 1 || armed != 1 {
		t.Fatalf("exited=%d armed=%d, want 1/1", exited, armed)
	}
}

func TestSquareOffDisarmRespectsProduct(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()
	engine := newDisarmEngine(t)
	s.bracketEngine = engine
	ctx := context.Background()
	now := time.Now()
	mis := createLiveBracket(t, engine, "brk-mis", "MIS", now.Add(-time.Minute))
	nrml := createLiveBracket(t, engine, "brk-nrml", "NRML", now)

	if errs := s.disarmLiveBracketsForContract(ctx, "nse_fo", "NIFTY26NOV25000CE", "MIS"); len(errs) != 0 {
		t.Fatalf("disarm errors: %v", errs)
	}
	gotMIS, _ := engine.Get(mis.ID)
	gotNRML, _ := engine.Get(nrml.ID)
	if gotMIS.Status != bracket.StatusExitedManual {
		t.Fatalf("MIS status = %s, want EXITED_MANUAL", gotMIS.Status)
	}
	if gotNRML.Status != bracket.StatusInPosition {
		t.Fatalf("NRML status = %s, want IN_POSITION", gotNRML.Status)
	}
	if errs := s.rearmLiveBrackets(ctx, []bracket.Bracket{*gotMIS}, "nse_fo", "NIFTY26NOV25000CE", "MIS"); len(errs) != 0 {
		t.Fatalf("rearm errors: %v", errs)
	}
	gotMIS, _ = engine.Get(mis.ID)
	if gotMIS.Status != bracket.StatusInPosition {
		t.Fatalf("MIS status after rearm = %s, want IN_POSITION", gotMIS.Status)
	}
}

func TestWebSocketSuppressesLiveBracketsWithoutBrokerSession(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	s.hsm = market.NewHSMFeed(testConfig(), nil, testLogger())

	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()

	endpoint := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	authHdr := http.Header{}
	authHdr.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:adminpass")))
	dial := func() *websocket.Conn {
		conn, response, err := websocket.DefaultDialer.Dial(endpoint, authHdr)
		if response != nil && response.Body != nil {
			defer func() { _ = response.Body.Close() }()
		}
		if err != nil {
			t.Fatalf("dial WebSocket: %v", err)
		}
		for i := 0; i < 2; i++ {
			var msg struct {
				Type string `json:"type"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				_ = conn.Close()
				t.Fatalf("read initial message: %v", err)
			}
		}
		return conn
	}

	ctx := context.Background()
	liveBracket := func(id, entryOrderID string) bracket.Bracket {
		return bracket.Bracket{
			ID: id, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
			InstrumentKey: liveDisarmKey, TransactionType: "BUY", Quantity: 75, Product: "MIS",
			EntryPrice: 100, EntryOrderID: entryOrderID, TargetPrice: 110, StopLossPrice: 90,
		}
	}

	// No broker session: a live bracket update must not be broadcast.
	conn := dial()
	if _, err := s.bracketEngine.Create(ctx, liveBracket("brk-ws-live-1", "ord-existing-1")); err != nil {
		t.Fatalf("create live bracket: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&msg); err == nil {
		t.Fatalf("received live data without a broker session: %+v", msg)
	}
	_ = conn.Close()

	// With a broker session the update is delivered.
	if _, err := s.a.SessionManager().NewBrokerSession(auth.TradeSession{
		Token: "tok", SID: "sid", BaseURL: "https://cis.kotaksecurities.com",
	}, auth.LoginMethodTOTP); err != nil {
		t.Fatalf("broker session: %v", err)
	}
	if s.a.CurrentSession() == nil {
		t.Fatal("broker session not visible after login")
	}
	conn = dial()
	defer conn.Close()
	if _, err := s.bracketEngine.Create(ctx, liveBracket("brk-ws-live-2", "ord-existing-2")); err != nil {
		t.Fatalf("create live bracket: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("expected bracket_update after broker login: %v", err)
		}
		if msg.Type == "bracket_update" {
			return
		}
	}
}
