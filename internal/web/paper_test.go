package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/paper"
	"github.com/akshaykhairmode/scalper/internal/scrip"
	"github.com/akshaykhairmode/scalper/internal/store"
)

func newPaperTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{
		Path:   filepath.Join(t.TempDir(), "paper.db"),
		Logger: testLogger(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := testConfig()
	cfg.Trading.Mode = "paper"
	cfg.Trading.Paper.Enabled = true
	cfg.Trading.Paper.StartingBalance = 100000
	cfg.Trading.Paper.ConfirmOrders = false

	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	instrument := scrip.Option{
		InstrumentKey:   key,
		ExchangeSegment: "nse_fo",
		PSymbol:         "NIFTY26NOV25000CE",
		PTrdSymbol:      "NIFTY26NOV25000CE",
		Underlying:      "NIFTY",
		Expiry:          "2026-11-26",
		StrikePaise:     2500000,
		OptionType:      scrip.Call,
		LotSize:         75,
	}
	marketAPI := fakeMarketAPI{
		quote:      market.Quote{LTP: 100, Bid: 99.9, Ask: 100.1},
		instrument: instrument,
	}
	s := newTestServer(t, serverOpts{
		db:                st,
		cfg:               &cfg,
		marketAPI:         marketAPI,
		bracketStore:      st.BracketsFor("live"),
		paperStore:        st,
		paperBracketStore: st.BracketsFor("paper"),
	})
	return s, st
}

func TestPaperOrderAndBooksWithoutBrokerSession(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	if !s.paperAvailable() {
		t.Fatal("paper account is not available")
	}

	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	body, _ := json.Marshal(map[string]any{
		"instrument_key":   key,
		"exchange_segment": "nse_fo",
		"trading_symbol":   "NIFTY26NOV25000CE",
		"transaction_type": "B",
		"quantity":         75,
		"order_type":       "MARKET",
		"product":          "MIS",
		"validity":         "DAY",
		"mode":             "paper",
		"confirm":          true,
	})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/broker/orders", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("paper order status = %d: %s", rec.Code, rec.Body.String())
	}
	var placed struct {
		OrderID string `json:"order_id"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &placed); err != nil {
		t.Fatal(err)
	}
	if placed.OrderID == "" || placed.Mode != "paper" {
		t.Fatalf("unexpected paper order response: %+v", placed)
	}

	rec = do(t, s, localRequest(http.MethodGet, "/api/broker/positions?mode=paper"))
	if rec.Code != http.StatusOK {
		t.Fatalf("paper positions status = %d: %s", rec.Code, rec.Body.String())
	}
	var positions struct {
		Data []struct {
			Quantity     int64   `json:"quantity"`
			AveragePrice float64 `json:"average_price"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &positions); err != nil {
		t.Fatal(err)
	}
	if len(positions.Data) != 1 || positions.Data[0].Quantity != 75 {
		t.Fatalf("paper positions = %+v", positions.Data)
	}

	rec = do(t, s, localRequest(http.MethodGet, "/api/broker/limits?mode=paper"))
	if rec.Code != http.StatusOK {
		t.Fatalf("paper limits status = %d: %s", rec.Code, rec.Body.String())
	}
	var limits map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &limits); err != nil {
		t.Fatal(err)
	}
	if limits["available_margin"] == nil {
		t.Fatalf("paper limits missing available_margin: %+v", limits)
	}

	rec = do(t, s, localRequest(http.MethodGet, "/api/broker/orders?mode=paper"))
	if rec.Code != http.StatusOK {
		t.Fatalf("paper orders status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPaperLimitOrderThenBracket(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	s.paperEngine.SetAsync(false)
	ctx := context.Background()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	s.onTick(ctx, key, 100, 99.9, 100.1)

	orderBody, _ := json.Marshal(map[string]any{
		"instrument_key":   key,
		"exchange_segment": "nse_fo",
		"trading_symbol":   "NIFTY26NOV25000CE",
		"transaction_type": "B",
		"quantity":         75,
		"order_type":       "L",
		"price":            99,
		"product":          "MIS",
		"validity":         "DAY",
		"mode":             "paper",
		"confirm":          true,
	})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/broker/orders", orderBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("paper limit order status = %d: %s", rec.Code, rec.Body.String())
	}
	var placed struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &placed); err != nil {
		t.Fatal(err)
	}

	// The UI places the order first and then registers the server bracket with
	// that entry order ID. The later paper fill must still arm the bracket.
	bracketBody, _ := json.Marshal(bracket.Bracket{
		Mode:            "paper",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      99,
		EntryOrderID:    placed.OrderID,
		TargetPrice:     110,
		StopLossPrice:   90,
	})
	rec = do(t, s, localRequestWithBody(http.MethodPost, "/api/brackets", bracketBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("paper bracket status = %d: %s", rec.Code, rec.Body.String())
	}
	var created bracket.Bracket
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != bracket.StatusPendingEntry {
		t.Fatalf("bracket should wait for the limit fill, got %s", created.Status)
	}

	s.onTick(ctx, key, 98.8, 98.7, 98.9)
	got, ok := s.paperEngine.Get(created.ID)
	if !ok || got.Status != bracket.StatusInPosition {
		t.Fatalf("limit fill did not arm the bracket: %+v", got)
	}
	s.onTick(ctx, key, 111, 110.9, 111.1)
	got, _ = s.paperEngine.Get(created.ID)
	if got.Status != bracket.StatusTargetHit {
		t.Fatalf("status = %s, want TARGET_HIT", got.Status)
	}
}

func TestInvalidTradingModeRejected(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	for _, path := range []string{
		"/api/broker/positions?mode=bogus",
		"/api/broker/orders?mode=bogus",
		"/api/broker/limits?mode=bogus",
		"/api/brackets?mode=bogus",
	} {
		rec := do(t, s, localRequest(http.MethodGet, path))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, rec.Code)
		}
	}
	rec := do(t, s, localRequest(http.MethodPost, "/api/brackets/missing/exit?mode=bogus"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bracket exit invalid mode status = %d, want 400", rec.Code)
	}
}

func TestPaperOversizedOpposingOrderBlocked(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	ctx := context.Background()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	s.onTick(ctx, key, 100, 99.9, 100.1)
	s.configMu.Lock()
	s.cfg.Trading.Risk.MaxLotsPerOrder = 1
	s.configMu.Unlock()

	base := kotakapi.Order{
		InstrumentKey:   key,
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		Product:         "MIS",
		OrderType:       "MKT",
		Validity:        "DAY",
	}
	buy := base
	buy.TransactionType = "BUY"
	buy.Quantity = 75
	if _, err := s.paperExec.Place(ctx, buy); err != nil {
		t.Fatalf("seed long: %v", err)
	}

	sell := base
	sell.TransactionType = "SELL"
	sell.Quantity = 7500
	if err := s.checkPaperOrderRisk(ctx, sell); err == nil {
		t.Fatal("oversized opposing order should be blocked")
	}
	sell.Quantity = 75
	if err := s.checkPaperOrderRisk(ctx, sell); err != nil {
		t.Fatalf("reducing order should be allowed: %v", err)
	}
}

func TestPaperOrderCancelAndModifyUpdateBracket(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	ctx := context.Background()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	s.onTick(ctx, key, 100, 99.9, 100.1)

	order := kotakapi.Order{
		InstrumentKey:   key,
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "L",
		Quantity:        75,
		Price:           99,
		Validity:        "DAY",
	}
	orderID, err := s.paperExec.Place(ctx, order)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	created, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode:            "paper",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      99,
		EntryOrderID:    orderID,
		TargetPrice:     110,
		StopLossPrice:   90,
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}

	body := []byte(`{"price":98,"quantity":75,"order_type":"L","validity":"DAY","confirm":true,"mode":"paper"}`)
	rec := do(t, s, localRequestWithBody(http.MethodPatch, "/api/broker/orders/"+orderID+"?mode=paper", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("modify status = %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := s.paperEngine.Get(created.ID)
	if got.EntryPrice != 98 {
		t.Fatalf("bracket entry price = %v, want 98", got.EntryPrice)
	}

	rec = do(t, s, localRequestWithBody(http.MethodDelete, "/api/broker/orders/"+orderID+"?mode=paper",
		[]byte(`{"confirm":true,"mode":"paper"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d: %s", rec.Code, rec.Body.String())
	}
	got, _ = s.paperEngine.Get(created.ID)
	if got.Status != bracket.StatusCancelled {
		t.Fatalf("bracket status = %s, want CANCELLED", got.Status)
	}
}

func TestPaperFillBeforeBracketRegistration(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	s.paperEngine.SetAsync(false)
	ctx := context.Background()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	s.onTick(ctx, key, 100, 99.9, 100.1)

	orderID, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey:   key,
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "L",
		Quantity:        75,
		Price:           99,
		Validity:        "DAY",
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	s.onTick(ctx, key, 98, 97.9, 98.1)

	created, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode:            "paper",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      99,
		EntryOrderID:    orderID,
		TargetPrice:     110,
		StopLossPrice:   90,
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}
	if created.Status != bracket.StatusInPosition {
		t.Fatalf("bracket should adopt the pre-registration fill, got %s", created.Status)
	}
}

func TestPaperManualExitLeavesPendingBracketAlone(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	s.paperEngine.SetAsync(false)
	ctx := context.Background()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	s.onTick(ctx, key, 100, 99.9, 100.1)

	if _, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey:   key,
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "MKT",
		Quantity:        75,
		Validity:        "DAY",
	}); err != nil {
		t.Fatalf("seed position: %v", err)
	}
	inPosition, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode:            "paper",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   90,
		Status:          bracket.StatusInPosition,
	})
	if err != nil {
		t.Fatalf("in-position bracket: %v", err)
	}
	restingID, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey:   key,
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "L",
		Quantity:        75,
		Price:           99,
		Validity:        "DAY",
	})
	if err != nil {
		t.Fatalf("resting entry: %v", err)
	}
	pending, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode:            "paper",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      99,
		EntryOrderID:    restingID,
		TargetPrice:     110,
		StopLossPrice:   90,
	})
	if err != nil {
		t.Fatalf("pending bracket: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"instrument_key":   key,
		"exchange_segment": "nse_fo",
		"trading_symbol":   "NIFTY26NOV25000CE",
		"transaction_type": "S",
		"quantity":         75,
		"order_type":       "MARKET",
		"product":          "MIS",
		"validity":         "DAY",
		"intent":           "EXIT",
		"mode":             "paper",
		"confirm":          true,
	})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/broker/orders", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("paper exit status = %d: %s", rec.Code, rec.Body.String())
	}

	gotInPosition, _ := s.paperEngine.Get(inPosition.ID)
	if gotInPosition.Status != bracket.StatusExitedManual {
		t.Fatalf("in-position bracket status = %s, want EXITED_MANUAL", gotInPosition.Status)
	}
	gotPending, _ := s.paperEngine.Get(pending.ID)
	if gotPending.Status != bracket.StatusPendingEntry {
		t.Fatalf("pending bracket status = %s, want PENDING_ENTRY", gotPending.Status)
	}
	order, ok := s.paperExec.Order(restingID)
	if !ok || !order.Open() {
		t.Fatalf("pending entry order should stay open: %+v", order)
	}
}

func TestPaperSquareOffCancelsRestingOrders(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	s.paperEngine.SetAsync(false)
	ctx := context.Background()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	s.onTick(ctx, key, 100, 99.9, 100.1)

	if _, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey:   key,
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "MKT",
		Quantity:        75,
		Validity:        "DAY",
	}); err != nil {
		t.Fatalf("seed position: %v", err)
	}
	restingID, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey:   key,
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "L",
		Quantity:        75,
		Price:           99,
		Validity:        "DAY",
	})
	if err != nil {
		t.Fatalf("resting order: %v", err)
	}
	pending, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode:            "paper",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      99,
		EntryOrderID:    restingID,
		TargetPrice:     110,
		StopLossPrice:   90,
	})
	if err != nil {
		t.Fatalf("pending bracket: %v", err)
	}
	inPosition, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode:            "paper",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   90,
		Status:          bracket.StatusInPosition,
	})
	if err != nil {
		t.Fatalf("in-position bracket: %v", err)
	}

	closed, errs := s.squareOffPaper(ctx)
	if len(errs) != 0 {
		t.Fatalf("square-off errors: %v", errs)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	for _, o := range s.paperExec.Orders() {
		if o.Open() {
			t.Fatalf("open order survived square-off: %+v", o)
		}
	}
	for _, p := range s.paperExec.Positions() {
		if p.NetQuantity != 0 {
			t.Fatalf("position survived square-off: %+v", p)
		}
	}
	gotPending, _ := s.paperEngine.Get(pending.ID)
	if gotPending.Status != bracket.StatusCancelled {
		t.Fatalf("pending bracket status = %s, want CANCELLED", gotPending.Status)
	}
	gotInPosition, _ := s.paperEngine.Get(inPosition.ID)
	if gotInPosition.Status != bracket.StatusExitedManual {
		t.Fatalf("in-position bracket status = %s, want EXITED_MANUAL", gotInPosition.Status)
	}
}

func TestPaperRestartReconcileArmsBracket(t *testing.T) {
	s, st := newPaperTestServer(t)
	defer s.Close()
	s.paperEngine.SetAsync(false)
	ctx := context.Background()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	s.onTick(ctx, key, 100, 99.9, 100.1)

	orderID, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey:   key,
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "L",
		Quantity:        75,
		Price:           99,
		Validity:        "DAY",
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	created, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode:            "paper",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      99,
		EntryOrderID:    orderID,
		TargetPrice:     110,
		StopLossPrice:   90,
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}

	order, err := st.GetPaperOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	order.Status = paper.StatusComplete
	order.FilledPrice = 99
	order.FilledQuantity = 75
	if err := st.UpdatePaperOrder(ctx, *order); err != nil {
		t.Fatalf("update order: %v", err)
	}

	restored, err := paper.New(ctx, paper.Options{Store: st, StartingBalance: 100000, ShortMarginPct: 20})
	if err != nil {
		t.Fatalf("restore executor: %v", err)
	}
	newEngine := bracket.NewEngine(st.BracketsFor("paper"), restored, testLogger())
	newEngine.SetMode("paper")
	newEngine.SetAsync(false)
	restored.SetOnFill(func(f bracket.Fill) { newEngine.OnFill(ctx, f) })
	s.paperExec = restored
	s.paperEngine = newEngine
	s.reconcilePaperBrackets(ctx)

	got, _ := s.paperEngine.Get(created.ID)
	if got.Status != bracket.StatusInPosition {
		t.Fatalf("reconciled status = %s, want IN_POSITION", got.Status)
	}
	if got.EntryPrice != 99 {
		t.Fatalf("reconciled entry = %v, want 99", got.EntryPrice)
	}
}

func TestPaperBracketTickLifecycle(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	s.paperEngine.SetAsync(false)
	ctx := context.Background()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"

	// Seed a live tick for the paper simulator and both engines.
	s.onTick(ctx, key, 100, 99.9, 100.1)

	body, _ := json.Marshal(bracket.Bracket{
		Mode:            "paper",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      0,
		TargetPrice:     110,
		StopLossPrice:   90,
	})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/brackets", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("paper bracket status = %d: %s", rec.Code, rec.Body.String())
	}
	var created bracket.Bracket
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Mode != "paper" {
		t.Fatalf("bracket mode = %q", created.Mode)
	}
	if created.Status != bracket.StatusInPosition {
		t.Fatalf("market entry should be IN_POSITION, got %s", created.Status)
	}
	if created.EntryPrice <= 0 {
		t.Fatalf("entry price not filled: %+v", created)
	}

	// Target hit -> paper exit is filled from the tick.
	s.onTick(ctx, key, 111, 110.9, 111.1)

	got, ok := s.paperEngine.Get(created.ID)
	if !ok {
		t.Fatal("paper bracket missing")
	}
	if got.Status != bracket.StatusTargetHit {
		t.Fatalf("status = %s, want TARGET_HIT", got.Status)
	}
	if got.ExitPrice <= 0 || got.PnL <= 0 {
		t.Fatalf("expected a profitable exit, got exit=%v pnl=%v", got.ExitPrice, got.PnL)
	}

	// Live view must never expose paper brackets.
	rec = do(t, s, localRequest(http.MethodGet, "/api/brackets?mode=live"))
	if rec.Code != http.StatusOK {
		t.Fatalf("live brackets status = %d", rec.Code)
	}
	var liveList bracketListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &liveList); err != nil {
		t.Fatal(err)
	}
	if len(liveList.Brackets) != 0 {
		t.Fatalf("live list leaked paper brackets: %+v", liveList.Brackets)
	}

	rec = do(t, s, localRequest(http.MethodGet, "/api/brackets?mode=paper"))
	if rec.Code != http.StatusOK {
		t.Fatalf("paper brackets status = %d", rec.Code)
	}
	var paperList bracketListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &paperList); err != nil {
		t.Fatal(err)
	}
	if len(paperList.Brackets) != 1 || paperList.Brackets[0].Status != bracket.StatusTargetHit {
		t.Fatalf("paper list = %+v", paperList.Brackets)
	}
}
