package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestPaperBracketCreateForcesPendingStatus(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	ctx := context.Background()
	s.onTick(ctx, key, 100, 99.9, 100.1)

	body, _ := json.Marshal(map[string]any{
		"id":                 "brk-forced-pending",
		"mode":               "paper",
		"exchange_segment":   "nse_fo",
		"trading_symbol":     "NIFTY26NOV25000CE",
		"instrument_key":     key,
		"transaction_type":   "BUY",
		"quantity":           75,
		"product":            "MIS",
		"entry_price":        99,
		"target_price":       110,
		"stoploss_price":     90,
		"status":             "IN_POSITION",
		"exchange_stop_mode": "SL-M",
		"confirm":            true,
	})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/brackets", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	var created bracket.Bracket
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != bracket.StatusPendingEntry {
		t.Fatalf("status = %s, want PENDING_ENTRY", created.Status)
	}
	got, ok := s.paperEngine.Get(created.ID)
	if !ok {
		t.Fatal("bracket missing")
	}
	if got.Status != bracket.StatusPendingEntry {
		t.Fatalf("stored status = %s, want PENDING_ENTRY", got.Status)
	}
	for _, o := range s.paperExec.Orders() {
		if o.Open() && strings.HasPrefix(o.Tag, "xstop_") {
			t.Fatalf("exchange backstop was armed for a fabricated in-position bracket: %+v", o)
		}
	}
}

func TestPaperPartialExitResizesBracket(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	ctx := context.Background()
	s.onTick(ctx, key, 100, 99.9, 100.1)
	if _, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "BUY", Product: "MIS", OrderType: "MKT", Quantity: 150, Validity: "DAY",
	}); err != nil {
		t.Fatalf("seed position: %v", err)
	}
	b, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode: "paper", ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		InstrumentKey: key, TransactionType: "BUY", Quantity: 150, Product: "MIS",
		EntryPrice: 100, TargetPrice: 110, StopLossPrice: 90, Status: bracket.StatusInPosition,
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"instrument_key": key, "exchange_segment": "nse_fo", "trading_symbol": "NIFTY26NOV25000CE",
		"transaction_type": "S", "quantity": 75, "order_type": "MARKET", "product": "MIS",
		"validity": "DAY", "intent": "ENTRY", "mode": "paper", "confirm": true,
	})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/broker/orders", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("partial exit status = %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := s.paperEngine.Get(b.ID)
	if got.Quantity != 75 {
		t.Fatalf("bracket quantity = %d, want 75", got.Quantity)
	}

	body, _ = json.Marshal(map[string]any{
		"instrument_key": key, "exchange_segment": "nse_fo", "trading_symbol": "NIFTY26NOV25000CE",
		"transaction_type": "S", "quantity": 75, "order_type": "MARKET", "product": "MIS",
		"validity": "DAY", "intent": "EXIT", "mode": "paper", "confirm": true,
	})
	rec = do(t, s, localRequestWithBody(http.MethodPost, "/api/broker/orders", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("full exit status = %d: %s", rec.Code, rec.Body.String())
	}
	got, _ = s.paperEngine.Get(b.ID)
	if got.Status != bracket.StatusExitedManual {
		t.Fatalf("bracket status = %s, want EXITED_MANUAL", got.Status)
	}
}

func TestPaperRestingExitResizesBracketOnFill(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	ctx := context.Background()
	s.onTick(ctx, key, 100, 99.9, 100.1)
	if _, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "BUY", Product: "MIS", OrderType: "MKT", Quantity: 150, Validity: "DAY",
	}); err != nil {
		t.Fatalf("seed position: %v", err)
	}
	b, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode: "paper", ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		InstrumentKey: key, TransactionType: "BUY", Quantity: 150, Product: "MIS",
		EntryPrice: 100, TargetPrice: 110, StopLossPrice: 90, Status: bracket.StatusInPosition,
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"instrument_key": key, "exchange_segment": "nse_fo", "trading_symbol": "NIFTY26NOV25000CE",
		"transaction_type": "S", "quantity": 75, "order_type": "L", "price": 101, "product": "MIS",
		"validity": "DAY", "intent": "EXIT", "mode": "paper", "confirm": true,
	})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/broker/orders", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("resting exit status = %d: %s", rec.Code, rec.Body.String())
	}

	s.onTick(ctx, key, 102, 101.9, 102.1)
	got, _ := s.paperEngine.Get(b.ID)
	if got.Quantity != 75 {
		t.Fatalf("bracket quantity = %d, want 75 after the resting exit filled", got.Quantity)
	}
}

func TestPaperSquareOffRearmsBracketWhenCloseFails(t *testing.T) {
	s, st := newPaperTestServer(t)
	defer s.Close()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	ctx := context.Background()

	now := time.Now()
	custom, err := paper.New(ctx, paper.Options{
		Store:           st,
		StartingBalance: 100000,
		ShortMarginPct:  20,
		Now:             func() time.Time { return now },
		QuoteSource:     func(context.Context, string) (paper.Quote, bool) { return paper.Quote{}, false },
	})
	if err != nil {
		t.Fatalf("paper.New: %v", err)
	}
	engine := bracket.NewEngine(st.BracketsFor("paper"), custom, testLogger())
	engine.SetMode("paper")
	engine.SetAsync(false)
	custom.SetOnFill(func(f bracket.Fill) { engine.OnFill(ctx, f) })
	s.paperExec = custom
	s.paperEngine = engine

	custom.OnTick(ctx, key, 100, 99.9, 100.1)
	if _, err := custom.Place(ctx, kotakapi.Order{
		InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "BUY", Product: "MIS", OrderType: "MKT", Quantity: 75, Validity: "DAY",
	}); err != nil {
		t.Fatalf("seed position: %v", err)
	}
	b, err := engine.Create(ctx, bracket.Bracket{
		Mode: "paper", ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		InstrumentKey: key, TransactionType: "BUY", Quantity: 75, Product: "MIS",
		EntryPrice: 100, TargetPrice: 110, StopLossPrice: 90, Status: bracket.StatusInPosition,
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}

	// Make the cached quote stale; the close cannot price and must fail.
	now = now.Add(11 * time.Second)

	closed, errs := s.squareOffPaper(ctx)
	if closed != 0 {
		t.Fatalf("closed = %d, want 0", closed)
	}
	if len(errs) == 0 {
		t.Fatal("expected the close to fail")
	}
	got, ok := engine.Get(b.ID)
	if !ok {
		t.Fatal("bracket missing after square-off")
	}
	if got.Status != bracket.StatusInPosition {
		t.Fatalf("bracket status = %s, want IN_POSITION (re-armed)", got.Status)
	}
}

func TestPaperReversalDisarmsWrongSideBracket(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	ctx := context.Background()
	s.onTick(ctx, key, 100, 99.9, 100.1)
	if _, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "BUY", Product: "MIS", OrderType: "MKT", Quantity: 75, Validity: "DAY",
	}); err != nil {
		t.Fatalf("seed position: %v", err)
	}
	b, err := s.paperEngine.Create(ctx, bracket.Bracket{
		Mode: "paper", ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		InstrumentKey: key, TransactionType: "BUY", Quantity: 75, Product: "MIS",
		EntryPrice: 100, TargetPrice: 110, StopLossPrice: 90, Status: bracket.StatusInPosition,
	})
	if err != nil {
		t.Fatalf("create bracket: %v", err)
	}
	// Sell 150: the account flips to short, so the long bracket is wrong-side.
	if _, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "SELL", Product: "MIS", OrderType: "MKT", Quantity: 150, Validity: "DAY",
	}); err != nil {
		t.Fatalf("reverse position: %v", err)
	}
	s.syncPaperBracketsAfterExit(ctx, key, "MIS", "nse_fo", "NIFTY26NOV25000CE")
	got, _ := s.paperEngine.Get(b.ID)
	if got.Status != bracket.StatusExitedManual {
		t.Fatalf("status = %s, want EXITED_MANUAL", got.Status)
	}
}

func TestPaperPositionsSeparateProducts(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	ctx := context.Background()
	s.onTick(ctx, key, 100, 99.9, 100.1)

	seed := func(product string) {
		if _, err := s.paperExec.Place(ctx, kotakapi.Order{
			InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
			TransactionType: "BUY", Product: product, OrderType: "MKT", Quantity: 75, Validity: "DAY",
		}); err != nil {
			t.Fatalf("seed %s: %v", product, err)
		}
	}
	seed("MIS")
	seed("NRML")

	nonZero := func() []paper.PositionView {
		var out []paper.PositionView
		for _, p := range s.paperExec.Positions() {
			if p.NetQuantity != 0 {
				out = append(out, p)
			}
		}
		return out
	}
	if got := nonZero(); len(got) != 2 {
		t.Fatalf("positions = %+v, want one MIS and one NRML", got)
	}

	if _, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "SELL", Product: "MIS", OrderType: "MKT", Quantity: 75, Validity: "DAY",
	}); err != nil {
		t.Fatalf("close MIS: %v", err)
	}
	got := nonZero()
	if len(got) != 1 || !strings.EqualFold(got[0].Product, "NRML") || got[0].NetQuantity != 75 {
		t.Fatalf("after closing MIS positions = %+v, want only NRML 75", got)
	}
}

func TestPaperLongMarginIsConsistent(t *testing.T) {
	ctx := context.Background()
	optionKey := "nse_fo|NIFTY|2026-11-26|25000|CE"
	futureKey := "nse_fo|NIFTY|2026-11-26|FUT"

	sFuture, _ := newPaperTestServer(t)
	defer sFuture.Close()
	sFuture.onTick(ctx, futureKey, 100, 99.9, 100.1)
	if _, err := sFuture.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey: futureKey, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOVFUT",
		TransactionType: "BUY", Product: "MIS", OrderType: "MKT", Quantity: 75, Validity: "DAY",
	}); err != nil {
		t.Fatalf("seed future: %v", err)
	}
	usedFuture := sFuture.paperExec.Limits().Used
	if usedFuture > 3000 {
		t.Fatalf("future long margin = %v, want the intraday percentage (~1500)", usedFuture)
	}

	sOption, _ := newPaperTestServer(t)
	defer sOption.Close()
	sOption.onTick(ctx, optionKey, 100, 99.9, 100.1)
	if _, err := sOption.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey: optionKey, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "BUY", Product: "MIS", OrderType: "MKT", Quantity: 75, Validity: "DAY",
	}); err != nil {
		t.Fatalf("seed option: %v", err)
	}
	usedOption := sOption.paperExec.Limits().Used
	if usedOption < 7000 {
		t.Fatalf("option long margin = %v, want the full premium", usedOption)
	}
}

func TestPaperMaxLotsCountsRestingEntryOrders(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	ctx := context.Background()
	s.onTick(ctx, key, 100, 99.9, 100.1)

	if _, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "BUY", Product: "MIS", OrderType: "L", Quantity: 75, Price: 99, Validity: "DAY",
	}); err != nil {
		t.Fatalf("seed resting order: %v", err)
	}

	s.configMu.Lock()
	s.cfg.Trading.Risk.MaxOpenLotsPerIndex = 1
	s.configMu.Unlock()

	order := kotakapi.Order{
		InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "BUY", Product: "MIS", OrderType: "L", Quantity: 75, Price: 99, Validity: "DAY",
	}
	if err := s.checkPaperOrderRisk(ctx, order); err == nil {
		t.Fatal("expected the resting entry order to count toward max_open_lots_per_index")
	}
}

func TestPaperReconcileCancelsOrphanPendingBracket(t *testing.T) {
	s, st := newPaperTestServer(t)
	defer s.Close()
	ctx := context.Background()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"

	orphan := bracket.Bracket{
		ID: "brk-orphan", Mode: "paper", ExchangeSegment: "nse_fo",
		TradingSymbol: "NIFTY26NOV25000CE", InstrumentKey: key,
		TransactionType: "BUY", Quantity: 75, Product: "MIS",
		EntryPrice: 100, TargetPrice: 110, StopLossPrice: 90,
		Status: bracket.StatusPendingEntry,
	}
	if err := st.BracketsFor("paper").CreateBracket(ctx, orphan); err != nil {
		t.Fatalf("create orphan: %v", err)
	}

	newEngine := bracket.NewEngine(st.BracketsFor("paper"), s.paperExec, testLogger())
	newEngine.SetMode("paper")
	s.paperEngine = newEngine
	s.reconcilePaperBrackets(ctx)

	got, ok := s.paperEngine.Get(orphan.ID)
	if !ok {
		t.Fatal("orphan bracket missing after reconcile")
	}
	if got.Status != bracket.StatusCancelled {
		t.Fatalf("orphan status = %s, want CANCELLED", got.Status)
	}
}

func TestPaperBracketRejectsMismatchedEntryOrder(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	ctx := context.Background()
	s.onTick(ctx, key, 100, 99.9, 100.1)
	id, err := s.paperExec.Place(ctx, kotakapi.Order{
		InstrumentKey: key, ExchangeSegment: "nse_fo", TradingSymbol: "NIFTY26NOV25000CE",
		TransactionType: "BUY", Product: "MIS", OrderType: "L", Quantity: 75, Price: 99, Validity: "DAY",
	})
	if err != nil {
		t.Fatalf("place entry: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"mode": "paper", "exchange_segment": "nse_fo", "trading_symbol": "NIFTY26NOV25000CE",
		"instrument_key": key, "transaction_type": "BUY", "quantity": 50, "product": "MIS",
		"entry_price": 99, "entry_order_id": id, "confirm": true,
	})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/brackets", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestPaperBracketRejectsUnknownEntryOrder(t *testing.T) {
	s, _ := newPaperTestServer(t)
	defer s.Close()
	key := "nse_fo|NIFTY|2026-11-26|25000|CE"
	body, _ := json.Marshal(map[string]any{
		"mode":             "paper",
		"exchange_segment": "nse_fo",
		"trading_symbol":   "NIFTY26NOV25000CE",
		"instrument_key":   key,
		"transaction_type": "BUY",
		"quantity":         75,
		"product":          "MIS",
		"entry_price":      100,
		"entry_order_id":   "paper_missing",
		"confirm":          true,
	})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/brackets", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
