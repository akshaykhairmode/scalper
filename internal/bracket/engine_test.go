package bracket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/orderfeed"
)

type mockStore struct {
	mu       sync.Mutex
	brackets map[string]Bracket
}

func newMockStore() *mockStore {
	return &mockStore{brackets: make(map[string]Bracket)}
}

func (m *mockStore) CreateBracket(_ context.Context, b Bracket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.brackets[b.ID] = b
	return nil
}

func (m *mockStore) UpdateBracket(_ context.Context, b Bracket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.brackets[b.ID] = b
	return nil
}

func (m *mockStore) GetBracket(_ context.Context, id string) (*Bracket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.brackets[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := b
	return &cp, nil
}

func (m *mockStore) ListBrackets(_ context.Context, limit int) ([]Bracket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []Bracket
	for _, b := range m.brackets {
		res = append(res, b)
	}
	if limit > 0 && len(res) > limit {
		res = res[:limit]
	}
	return res, nil
}

func (m *mockStore) GetActiveBrackets(_ context.Context) ([]Bracket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []Bracket
	for _, b := range m.brackets {
		if b.Status == StatusPendingEntry || b.Status == StatusInPosition {
			res = append(res, b)
		}
	}
	return res, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestEngineCreate(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()

	var placedOrder kotakapi.Order
	var placeCount atomic.Int32
	placeOrder := func(_ context.Context, o kotakapi.Order) (string, error) {
		placeCount.Add(1)
		placedOrder = o
		return "ord-12345", nil
	}
	cancelOrder := func(_ context.Context, _ string) error { return nil }

	engine := NewEngine(store, placeOrder, cancelOrder, testLogger())

	// 1. Validation errors
	if _, err := engine.Create(ctx, Bracket{}); err == nil {
		t.Fatal("expected error for empty bracket")
	}
	if _, err := engine.Create(ctx, Bracket{ExchangeSegment: "NSE", TradingSymbol: "RELIANCE", Quantity: 0}); err == nil {
		t.Fatal("expected error for quantity <= 0")
	}
	if _, err := engine.Create(ctx, Bracket{ExchangeSegment: "NSE", TradingSymbol: "RELIANCE", InstrumentKey: "inst-1", Quantity: 10, TransactionType: "INVALID"}); err == nil {
		t.Fatal("expected error for invalid transaction type")
	}

	// 2. Successful creation with automatic entry order placement
	var notified Bracket
	var notifyCount atomic.Int32
	engine.SetOnChange(func(b Bracket) {
		notifyCount.Add(1)
		notified = b
	})

	b1, err := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE-EQ",
		InstrumentKey:   "2885",
		TransactionType: "BUY",
		Quantity:        100,
		Product:         "MIS",
		EntryPrice:      2500,
		TargetPrice:     2550,
		StopLossPrice:   2450,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if b1.ID == "" {
		t.Fatal("expected generated bracket ID")
	}
	if b1.Status != StatusPendingEntry {
		t.Fatalf("expected StatusPendingEntry, got %s", b1.Status)
	}
	if b1.EntryOrderID != "ord-12345" {
		t.Fatalf("expected EntryOrderID 'ord-12345', got %q", b1.EntryOrderID)
	}
	if placeCount.Load() != 1 {
		t.Fatalf("expected 1 placeOrder call, got %d", placeCount.Load())
	}
	if placedOrder.TradingSymbol != "RELIANCE-EQ" || placedOrder.OrderType != "L" || placedOrder.Price != 2500 {
		t.Fatalf("unexpected placed order: %+v", placedOrder)
	}
	if notifyCount.Load() != 1 || notified.ID != b1.ID {
		t.Fatalf("expected notification, count=%d, notified=%+v", notifyCount.Load(), notified)
	}

	// Verify persistence in store
	stored, err := store.GetBracket(ctx, b1.ID)
	if err != nil || stored == nil || stored.EntryOrderID != "ord-12345" {
		t.Fatalf("stored bracket mismatch: %+v, err=%v", stored, err)
	}

	// 3. Creation with existing entry order ID should NOT place order again
	b2, err := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "TCS-EQ",
		InstrumentKey:   "11536",
		TransactionType: "SELL",
		Quantity:        50,
		EntryPrice:      3800,
		EntryOrderID:    "existing-ord-999",
		TargetPrice:     3700,
		StopLossPrice:   3850,
	})
	if err != nil {
		t.Fatalf("Create b2 failed: %v", err)
	}
	if placeCount.Load() != 1 {
		t.Fatalf("expected placeOrder not called for existing order ID, count=%d", placeCount.Load())
	}
	if b2.EntryOrderID != "existing-ord-999" {
		t.Fatalf("expected existing order ID preserved, got %q", b2.EntryOrderID)
	}
}

func TestEngineCancel(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()

	var cancelledID string
	cancelOrder := func(_ context.Context, id string) error {
		cancelledID = id
		return nil
	}

	engine := NewEngine(store, nil, cancelOrder, testLogger())

	// Cancel non-existent bracket
	if err := engine.Cancel(ctx, "non-existent"); err == nil {
		t.Fatal("expected error cancelling non-existent bracket")
	}

	// Create a pending bracket
	b, err := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "INFY-EQ",
		InstrumentKey:   "1594",
		TransactionType: "BUY",
		Quantity:        10,
		EntryPrice:      1500,
		EntryOrderID:    "ord-entry-101",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var notifyCount atomic.Int32
	engine.SetOnChange(func(Bracket) {
		notifyCount.Add(1)
	})

	if err := engine.Cancel(ctx, b.ID); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	if cancelledID != "ord-entry-101" {
		t.Fatalf("expected cancelled order 'ord-entry-101', got %q", cancelledID)
	}

	got, ok := engine.Get(b.ID)
	if !ok || got.Status != StatusCancelled {
		t.Fatalf("expected status CANCELLED, got %+v", got)
	}

	// Repeated cancel should fail
	if err := engine.Cancel(ctx, b.ID); err == nil {
		t.Fatal("expected error on cancelling already cancelled bracket")
	}
}

func TestEngineExitManual(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()

	var cancelledOrders []string
	cancelOrder := func(_ context.Context, id string) error {
		cancelledOrders = append(cancelledOrders, id)
		return nil
	}

	var placedOrders []kotakapi.Order
	placeOrder := func(_ context.Context, o kotakapi.Order) (string, error) {
		placedOrders = append(placedOrders, o)
		return fmt.Sprintf("exit-%d", len(placedOrders)), nil
	}

	engine := NewEngine(store, placeOrder, cancelOrder, testLogger())

	// 1. Manual exit on PENDING_ENTRY cancels entry order and marks CANCELLED
	pending, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "HDFCBANK-EQ",
		InstrumentKey:   "1333",
		TransactionType: "BUY",
		Quantity:        25,
		EntryPrice:      1600,
		EntryOrderID:    "entry-pend-1",
	})

	if err := engine.Exit(ctx, pending.ID); err != nil {
		t.Fatalf("Exit pending failed: %v", err)
	}
	gotPending, _ := engine.Get(pending.ID)
	if gotPending.Status != StatusCancelled {
		t.Fatalf("expected status CANCELLED for pending exit, got %s", gotPending.Status)
	}

	// 2. Manual exit on IN_POSITION (BUY bracket) -> places SELL market order
	inPosLong, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "NIFTY26NOV24000CE",
		InstrumentKey:   "nifty-ce",
		TransactionType: "BUY",
		Quantity:        50,
		Product:         "NRML",
		EntryPrice:      120,
		Status:          StatusInPosition,
		ExitOrderID:     "resting-limit-target",
	})

	if err := engine.Exit(ctx, inPosLong.ID); err != nil {
		t.Fatalf("Exit inPosLong failed: %v", err)
	}

	// Verify resting exit order was cancelled
	foundResting := false
	for _, id := range cancelledOrders {
		if id == "resting-limit-target" {
			foundResting = true
			break
		}
	}
	if !foundResting {
		t.Fatalf("expected resting order 'resting-limit-target' cancelled, got %v", cancelledOrders)
	}

	// Verify market exit order placed (SELL, NRML, 50 shares)
	if len(placedOrders) != 1 {
		t.Fatalf("expected 1 exit order placed, got %d", len(placedOrders))
	}
	lastOrder := placedOrders[0]
	if lastOrder.TransactionType != "SELL" || lastOrder.OrderType != "MKT" || lastOrder.Quantity != 50 || lastOrder.Product != "NRML" {
		t.Fatalf("unexpected exit order: %+v", lastOrder)
	}

	gotLong, _ := engine.Get(inPosLong.ID)
	if gotLong.Status != StatusExitedManual || gotLong.ExitOrderID != "exit-1" {
		t.Fatalf("unexpected bracket state after manual exit: %+v", gotLong)
	}
}

func TestEngineOnOrderEventEntryFilled(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil, nil, testLogger())

	b, err := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "SBIN-EQ",
		InstrumentKey:   "3045",
		TransactionType: "BUY",
		Quantity:        200,
		EntryPrice:      800,
		EntryOrderID:    "kotak-ord-555",
		TargetPrice:     820,
		StopLossPrice:   790,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var notify Bracket
	engine.SetOnChange(func(br Bracket) {
		notify = br
	})

	// 1. Send unrelated order event -> no change
	engine.OnOrderEvent(ctx, orderfeed.Event{
		Type: "order",
		Data: json.RawMessage(`{"order_id":"other-ord","status":"TRAD"}`),
	})
	got, _ := engine.Get(b.ID)
	if got.Status != StatusPendingEntry {
		t.Fatalf("expected still PENDING_ENTRY, got %s", got.Status)
	}

	// 2. Send matching filled event with filled price
	engine.OnOrderEvent(ctx, orderfeed.Event{
		Type: "order",
		Data: json.RawMessage(`{"nOrdNo":"kotak-ord-555","status":"TRAD","avgPrc":"802.25"}`),
	})

	got, _ = engine.Get(b.ID)
	if got.Status != StatusInPosition {
		t.Fatalf("expected status IN_POSITION, got %s", got.Status)
	}
	if got.EntryPrice != 802.25 {
		t.Fatalf("expected entry price updated to 802.25, got %f", got.EntryPrice)
	}
	if notify.ID != b.ID || notify.Status != StatusInPosition {
		t.Fatalf("expected notification for IN_POSITION, got %+v", notify)
	}

	// 3. Test entry order cancelled event
	bCan, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "AXISBANK-EQ",
		InstrumentKey:   "5900",
		TransactionType: "BUY",
		Quantity:        10,
		EntryPrice:      1100,
		EntryOrderID:    "entry-to-cancel",
	})

	engine.OnOrderEvent(ctx, orderfeed.Event{
		Type: "order",
		Data: json.RawMessage(`{"order_id":"entry-to-cancel","status":"CANCELLED"}`),
	})

	gotCan, _ := engine.Get(bCan.ID)
	if gotCan.Status != StatusCancelled {
		t.Fatalf("expected CANCELLED, got %s", gotCan.Status)
	}
}

func TestEngineOnTickTargetHitLong(t *testing.T) {
	ctx := context.Background()

	var placedExit kotakapi.Order
	var placeCallCount atomic.Int32
	placeOrder := func(_ context.Context, o kotakapi.Order) (string, error) {
		placeCallCount.Add(1)
		placedExit = o
		return "exit-target-order", nil
	}

	var cancelledResting string
	cancelOrder := func(_ context.Context, id string) error {
		cancelledResting = id
		return nil
	}

	engine := NewEngine(nil, placeOrder, cancelOrder, testLogger())

	b, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "NIFTY-FUT",
		InstrumentKey:   "nifty-fut-nov",
		TransactionType: "BUY",
		Quantity:        50,
		EntryPrice:      24000,
		TargetPrice:     24200,
		StopLossPrice:   23900,
		Status:          StatusInPosition,
		ExitOrderID:     "resting-exit-123",
	})

	var notified Bracket
	engine.SetOnChange(func(br Bracket) {
		notified = br
	})

	// Tick on different instrument -> ignored
	engine.OnTick(ctx, "other-key", 24300)
	got, _ := engine.Get(b.ID)
	if got.Status != StatusInPosition {
		t.Fatalf("status should not change for other instrument")
	}

	// Tick within range -> ignored
	engine.OnTick(ctx, "nifty-fut-nov", 24150)
	got, _ = engine.Get(b.ID)
	if got.Status != StatusInPosition {
		t.Fatalf("status should stay IN_POSITION, got %s", got.Status)
	}

	// Tick hits Target (>= 24200)
	engine.OnTick(ctx, "nifty-fut-nov", 24210)

	got, _ = engine.Get(b.ID)
	if got.Status != StatusTargetHit {
		t.Fatalf("expected status TARGET_HIT, got %s", got.Status)
	}
	if got.ExitPrice != 24210 {
		t.Fatalf("expected ExitPrice 24210, got %f", got.ExitPrice)
	}
	expectedPnL := (24210.0 - 24000.0) * 50.0 // 10500
	if got.PnL != expectedPnL {
		t.Fatalf("expected PnL %f, got %f", expectedPnL, got.PnL)
	}
	if cancelledResting != "resting-exit-123" {
		t.Fatalf("expected resting order cancelled, got %q", cancelledResting)
	}
	if placedExit.TransactionType != "SELL" || placedExit.OrderType != "MKT" || placedExit.Quantity != 50 {
		t.Fatalf("unexpected exit order placed: %+v", placedExit)
	}
	if got.ExitOrderID != "exit-target-order" {
		t.Fatalf("expected ExitOrderID updated, got %q", got.ExitOrderID)
	}
	if notified.Status != StatusTargetHit {
		t.Fatalf("expected callback notified of TARGET_HIT")
	}

	// Subsequent tick should not trigger exit again
	engine.OnTick(ctx, "nifty-fut-nov", 24250)
	if placeCallCount.Load() != 1 {
		t.Fatalf("expected placeOrder called once, got %d", placeCallCount.Load())
	}
}

func TestEngineOnTickSLHitLong(t *testing.T) {
	ctx := context.Background()

	var placedExit kotakapi.Order
	placeOrder := func(_ context.Context, o kotakapi.Order) (string, error) {
		placedExit = o
		return "exit-sl-order", nil
	}

	engine := NewEngine(nil, placeOrder, nil, testLogger())

	b, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "TATAMOTORS-EQ",
		InstrumentKey:   "3456",
		TransactionType: "BUY",
		Quantity:        100,
		EntryPrice:      1000,
		TargetPrice:     1050,
		StopLossPrice:   980,
		Status:          StatusInPosition,
	})

	// Tick hits StopLoss (<= 980)
	engine.OnTick(ctx, "3456", 975)

	got, _ := engine.Get(b.ID)
	if got.Status != StatusSLHit {
		t.Fatalf("expected status SL_HIT, got %s", got.Status)
	}
	if got.ExitPrice != 975 {
		t.Fatalf("expected exit price 975, got %f", got.ExitPrice)
	}
	expectedPnL := (975.0 - 1000.0) * 100.0 // -2500
	if got.PnL != expectedPnL {
		t.Fatalf("expected PnL %f, got %f", expectedPnL, got.PnL)
	}
	if placedExit.TransactionType != "SELL" || placedExit.OrderType != "MKT" || placedExit.Quantity != 100 {
		t.Fatalf("unexpected exit order: %+v", placedExit)
	}
}

func TestEngineOnTickShortTargetAndSL(t *testing.T) {
	ctx := context.Background()

	var placedExit kotakapi.Order
	placeOrder := func(_ context.Context, o kotakapi.Order) (string, error) {
		placedExit = o
		return "short-exit", nil
	}

	// 1. Short Target Hit (price <= target)
	engine := NewEngine(nil, placeOrder, nil, testLogger())
	bTarget, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "BANKNIFTY-FUT",
		InstrumentKey:   "bnf-fut",
		TransactionType: "SELL",
		Quantity:        30,
		EntryPrice:      51000,
		TargetPrice:     50500,
		StopLossPrice:   51300,
		Status:          StatusInPosition,
	})

	engine.OnTick(ctx, "bnf-fut", 50480)
	gotTarget, _ := engine.Get(bTarget.ID)
	if gotTarget.Status != StatusTargetHit {
		t.Fatalf("expected TARGET_HIT for short, got %s", gotTarget.Status)
	}
	expectedPnL := (51000.0 - 50480.0) * 30.0 // +15600
	if gotTarget.PnL != expectedPnL {
		t.Fatalf("expected PnL %f, got %f", expectedPnL, gotTarget.PnL)
	}
	if placedExit.TransactionType != "BUY" {
		t.Fatalf("expected BUY exit for short, got %s", placedExit.TransactionType)
	}

	// 2. Short Stop Loss Hit (price >= SL)
	engine2 := NewEngine(nil, placeOrder, nil, testLogger())
	bSL, _ := engine2.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "BANKNIFTY-FUT",
		InstrumentKey:   "bnf-fut",
		TransactionType: "SELL",
		Quantity:        30,
		EntryPrice:      51000,
		TargetPrice:     50500,
		StopLossPrice:   51300,
		Status:          StatusInPosition,
	})

	engine2.OnTick(ctx, "bnf-fut", 51320)
	gotSL, _ := engine2.Get(bSL.ID)
	if gotSL.Status != StatusSLHit {
		t.Fatalf("expected SL_HIT for short, got %s", gotSL.Status)
	}
	expectedSLPnL := (51000.0 - 51320.0) * 30.0 // -9600
	if gotSL.PnL != expectedSLPnL {
		t.Fatalf("expected PnL %f, got %f", expectedSLPnL, gotSL.PnL)
	}
}

func TestEngineTrailingStop(t *testing.T) {
	ctx := context.Background()
	placeOrder := func(_ context.Context, _ kotakapi.Order) (string, error) {
		return "trail-exit", nil
	}

	engine := NewEngine(nil, placeOrder, nil, testLogger())

	// Long position with Entry=100, Initial SL=90, Target=150, TrailingStop=5
	b, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "WIPRO-EQ",
		InstrumentKey:   "wipro",
		TransactionType: "BUY",
		Quantity:        10,
		EntryPrice:      100,
		StopLossPrice:   90,
		TargetPrice:     150,
		TrailingStop:    5,
		Status:          StatusInPosition,
	})

	// Price rises to 108 -> SL trails to 108 - 5 = 103
	engine.OnTick(ctx, "wipro", 108)
	got, _ := engine.Get(b.ID)
	if got.Status != StatusInPosition {
		t.Fatalf("expected still in position, got %s", got.Status)
	}
	if got.StopLossPrice != 103 {
		t.Fatalf("expected trailing SL to be 103, got %f", got.StopLossPrice)
	}

	// Price drops to 104 -> SL remains 103
	engine.OnTick(ctx, "wipro", 104)
	got, _ = engine.Get(b.ID)
	if got.StopLossPrice != 103 {
		t.Fatalf("expected SL to remain 103, got %f", got.StopLossPrice)
	}

	// Price drops to 102.5 -> Hits updated Stop Loss (102.5 <= 103)
	engine.OnTick(ctx, "wipro", 102.5)
	got, _ = engine.Get(b.ID)
	if got.Status != StatusSLHit {
		t.Fatalf("expected SL_HIT from trailed stop loss, got %s", got.Status)
	}
	if got.ExitPrice != 102.5 {
		t.Fatalf("expected ExitPrice 102.5, got %f", got.ExitPrice)
	}
	// Profit locked in by trailing stop: (102.5 - 100) * 10 = 25
	if got.PnL != 25 {
		t.Fatalf("expected PnL +25, got %f", got.PnL)
	}
}

func TestEngineListAndGet(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil, nil, testLogger())

	now := time.Now()
	b1, _ := engine.Create(ctx, Bracket{
		ID:              "b-1",
		ExchangeSegment: "NSE",
		TradingSymbol:   "SYM1",
		InstrumentKey:   "k1",
		TransactionType: "BUY",
		Quantity:        10,
		CreatedAt:       now.Add(-2 * time.Minute),
	})
	b2, _ := engine.Create(ctx, Bracket{
		ID:              "b-2",
		ExchangeSegment: "NSE",
		TradingSymbol:   "SYM2",
		InstrumentKey:   "k2",
		TransactionType: "BUY",
		Quantity:        20,
		CreatedAt:       now.Add(-1 * time.Minute),
	})
	b3, _ := engine.Create(ctx, Bracket{
		ID:              "b-3",
		ExchangeSegment: "NSE",
		TradingSymbol:   "SYM3",
		InstrumentKey:   "k3",
		TransactionType: "BUY",
		Quantity:        30,
		CreatedAt:       now,
	})

	list, err := engine.List(10)
	if err != nil || len(list) != 3 {
		t.Fatalf("expected 3 brackets, got %d, err=%v", len(list), err)
	}
	// Verify descending order
	if list[0].ID != b3.ID || list[1].ID != b2.ID || list[2].ID != b1.ID {
		t.Fatalf("unexpected order: %v, %v, %v", list[0].ID, list[1].ID, list[2].ID)
	}

	// Verify limit
	limited, err := engine.List(2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("expected 2 brackets with limit, got %d", len(limited))
	}

	// Verify Get returns copy
	got, ok := engine.Get(b1.ID)
	if !ok || got == nil {
		t.Fatal("expected Get to succeed")
	}
	got.Quantity = 9999
	original, _ := engine.Get(b1.ID)
	if original.Quantity == 9999 {
		t.Fatal("mutation of Get result affected internal bracket")
	}
}

func TestEngineConcurrentAccessRaceFree(_ *testing.T) {
	ctx := context.Background()
	placeOrder := func(_ context.Context, _ kotakapi.Order) (string, error) {
		return "ord-conc", nil
	}
	cancelOrder := func(_ context.Context, _ string) error {
		return nil
	}

	engine := NewEngine(nil, placeOrder, cancelOrder, testLogger())

	// Create 10 brackets
	for i := 0; i < 10; i++ {
		_, _ = engine.Create(ctx, Bracket{
			ExchangeSegment: "NSE",
			TradingSymbol:   fmt.Sprintf("SYM-%d", i),
			InstrumentKey:   fmt.Sprintf("inst-%d", i),
			TransactionType: "BUY",
			Quantity:        10,
			EntryPrice:      100,
			TargetPrice:     110,
			StopLossPrice:   90,
			Status:          StatusInPosition,
		})
	}

	var wg sync.WaitGroup
	// Concurrently send ticks
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				engine.OnTick(ctx, fmt.Sprintf("inst-%d", j%10), 95.0+float64(idx))
			}
		}(i)
	}

	// Concurrently read List and Get
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = engine.List(5)
				_, _ = engine.Get("inst-0")
			}
		}()
	}

	// Concurrently send order events
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				engine.OnOrderEvent(ctx, orderfeed.Event{
					Type: "order",
					Data: json.RawMessage(fmt.Sprintf(`{"order_id":"ord-%d","status":"TRAD"}`, idx)),
				})
			}
		}(i)
	}

	wg.Wait()
}

func TestCancelOnInPositionFails(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil, nil, testLogger())

	b, err := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE",
		InstrumentKey:   "nse_cm|2885",
		TransactionType: "BUY",
		Quantity:        10,
		Status:          StatusInPosition,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Cancelling an IN_POSITION bracket must fail
	if err := engine.Cancel(ctx, b.ID); err == nil {
		t.Fatal("expected error cancelling active IN_POSITION bracket, got nil")
	}
	got, _ := engine.Get(b.ID)
	if got.Status != StatusInPosition {
		t.Fatalf("expected status to stay IN_POSITION, got %s", got.Status)
	}
}

func TestDirectionalPriceValidation(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil, nil, testLogger())

	// BUY: Target <= Entry must fail
	_, err := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE",
		InstrumentKey:   "nse_cm|2885",
		TransactionType: "BUY",
		Quantity:        10,
		EntryPrice:      100,
		TargetPrice:     95, // inverted
		StopLossPrice:   90,
	})
	if err == nil || !strings.Contains(err.Error(), "target price must be greater than entry price for BUY") {
		t.Fatalf("expected target validation error for BUY, got %v", err)
	}

	// BUY: StopLoss >= Entry must fail
	_, err = engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE",
		InstrumentKey:   "nse_cm|2885",
		TransactionType: "BUY",
		Quantity:        10,
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   105, // inverted
	})
	if err == nil || !strings.Contains(err.Error(), "stoploss price must be less than entry price for BUY") {
		t.Fatalf("expected stoploss validation error for BUY, got %v", err)
	}

	// SELL: Target >= Entry must fail
	_, err = engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE",
		InstrumentKey:   "nse_cm|2885",
		TransactionType: "SELL",
		Quantity:        10,
		EntryPrice:      100,
		TargetPrice:     105, // inverted
		StopLossPrice:   110,
	})
	if err == nil || !strings.Contains(err.Error(), "target price must be less than entry price for SELL") {
		t.Fatalf("expected target validation error for SELL, got %v", err)
	}
}

func TestExitFailedStatusOnBrokerError(t *testing.T) {
	ctx := context.Background()
	placeCallCount := 0
	placeOrder := func(_ context.Context, _ kotakapi.Order) (string, error) {
		placeCallCount++
		return "", errors.New("502 Bad Gateway: broker down")
	}

	engine := NewEngine(nil, placeOrder, nil, testLogger())

	b, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE",
		InstrumentKey:   "nse_cm|2885",
		TransactionType: "BUY",
		Quantity:        10,
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   90,
		Status:          StatusInPosition,
	})

	// Manual exit with failing broker must return error and set StatusExitFailed
	err := engine.Exit(ctx, b.ID)
	if err == nil {
		t.Fatal("expected Exit error, got nil")
	}

	got, _ := engine.Get(b.ID)
	if got.Status != StatusExitFailed {
		t.Fatalf("expected status %s, got %s", StatusExitFailed, got.Status)
	}
	if placeCallCount < 3 {
		t.Fatalf("expected at least 3 attempts (including retries), got %d", placeCallCount)
	}
}

func TestExitOrderCancellation(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil, nil, testLogger())

	b, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE",
		InstrumentKey:   "nse_cm|2885",
		TransactionType: "BUY",
		Quantity:        10,
		ExitOrderID:     "exit-ord-123",
		Status:          StatusSLHit,
	})

	// Broker sends rejection/cancellation report for exit order
	engine.OnOrderEvent(ctx, orderfeed.Event{
		Type: "order",
		Data: json.RawMessage(`{"order_id":"exit-ord-123","status":"REJ"}`),
	})

	got, _ := engine.Get(b.ID)
	if got.Status != StatusExitFailed {
		t.Fatalf("expected status %s on broker reject, got %s", StatusExitFailed, got.Status)
	}
}

func TestFastFillOrderEventPrecedesHTTP(t *testing.T) {
	ctx := context.Background()
	var engine *Engine
	placeOrder := func(_ context.Context, o kotakapi.Order) (string, error) {
		// Simulate fast WebSocket fill arriving BEFORE placeOrder returns
		engine.OnOrderEvent(ctx, orderfeed.Event{
			Type: "order",
			Data: json.RawMessage(fmt.Sprintf(`{"tag":"%s","order_id":"kotak-ord-999","status":"TRAD","avg_price":102.5}`, o.Tag)),
		})
		return "kotak-ord-999", nil
	}

	engine = NewEngine(nil, placeOrder, nil, testLogger())

	b, err := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE",
		InstrumentKey:   "nse_cm|2885",
		TransactionType: "BUY",
		Quantity:        10,
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   90,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := engine.Get(b.ID)
	if got.Status != StatusInPosition {
		t.Fatalf("expected status IN_POSITION from early fill, got %s", got.Status)
	}
	if got.EntryPrice != 102.5 {
		t.Fatalf("expected EntryPrice 102.5 from early fill, got %f", got.EntryPrice)
	}
}

func TestEngineUpdateLevels(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()

	var modifiedID string
	var modifiedOrder kotakapi.Order
	var modifyCount atomic.Int32
	modifyOrder := func(_ context.Context, id string, o kotakapi.Order) error {
		modifyCount.Add(1)
		modifiedID = id
		modifiedOrder = o
		return nil
	}
	placeOrder := func(_ context.Context, _ kotakapi.Order) (string, error) { return "ord-entry-1", nil }

	engine := NewEngine(store, placeOrder, nil, testLogger())
	engine.SetModifyOrder(modifyOrder)

	b, err := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE-EQ",
		InstrumentKey:   "nse_cm|2885",
		TransactionType: "BUY",
		Quantity:        10,
		Product:         "MIS",
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   90,
		TrailingStop:    5,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	target := 112.0
	updated, err := engine.UpdateLevels(ctx, b.ID, LevelUpdates{TargetPrice: &target})
	if err != nil {
		t.Fatalf("UpdateLevels target: %v", err)
	}
	if updated.TargetPrice != target {
		t.Fatalf("expected target %v, got %v", target, updated.TargetPrice)
	}
	if modifyCount.Load() != 0 {
		t.Fatal("target-only update must not modify the broker order")
	}

	stop := 95.0
	updated, err = engine.UpdateLevels(ctx, b.ID, LevelUpdates{StopLossPrice: &stop})
	if err != nil {
		t.Fatalf("UpdateLevels stop: %v", err)
	}
	if updated.StopLossPrice != stop {
		t.Fatalf("expected stop %v, got %v", stop, updated.StopLossPrice)
	}
	if updated.TrailingStop != 0 {
		t.Fatalf("expected trailing stop cleared on manual SL update, got %v", updated.TrailingStop)
	}

	entry := 98.0
	updated, err = engine.UpdateLevels(ctx, b.ID, LevelUpdates{EntryPrice: &entry})
	if err != nil {
		t.Fatalf("UpdateLevels entry: %v", err)
	}
	if updated.EntryPrice != entry {
		t.Fatalf("expected entry %v, got %v", entry, updated.EntryPrice)
	}
	if modifyCount.Load() != 1 || modifiedID != "ord-entry-1" || modifiedOrder.Price != entry {
		t.Fatalf("unexpected modify call: count=%d id=%q order=%+v", modifyCount.Load(), modifiedID, modifiedOrder)
	}
	stored, err := store.GetBracket(ctx, b.ID)
	if err != nil || stored == nil || stored.EntryPrice != entry {
		t.Fatalf("store not updated: %+v err=%v", stored, err)
	}

	// A target below the entry must be rejected for a BUY bracket.
	badTarget := 90.0
	if _, err := engine.UpdateLevels(ctx, b.ID, LevelUpdates{TargetPrice: &badTarget}); err == nil {
		t.Fatal("expected directional validation error")
	}

	// Zero removes the target exit.
	zero := 0.0
	updated, err = engine.UpdateLevels(ctx, b.ID, LevelUpdates{TargetPrice: &zero})
	if err != nil {
		t.Fatalf("remove target: %v", err)
	}
	if updated.TargetPrice != 0 {
		t.Fatalf("expected target removed, got %v", updated.TargetPrice)
	}

	// Entry price cannot change once in position.
	inPos, err := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "TCS-EQ",
		InstrumentKey:   "nse_cm|11536",
		TransactionType: "BUY",
		Quantity:        10,
		EntryPrice:      3800,
		EntryOrderID:    "ord-filled-1",
		TargetPrice:     3900,
		StopLossPrice:   3700,
		Status:          StatusInPosition,
	})
	if err != nil {
		t.Fatalf("Create in-position: %v", err)
	}
	newEntry := 3790.0
	if _, err := engine.UpdateLevels(ctx, inPos.ID, LevelUpdates{EntryPrice: &newEntry}); err == nil {
		t.Fatal("expected error changing entry price of an active position")
	}
}

func TestDisarmManualSquareOff(t *testing.T) {
	ctx := context.Background()
	orderPlaced := false
	placeOrder := func(_ context.Context, _ kotakapi.Order) (string, error) {
		orderPlaced = true
		return "ord-err", nil
	}
	engine := NewEngine(nil, placeOrder, nil, testLogger())

	b, _ := engine.Create(ctx, Bracket{
		ExchangeSegment: "NSE",
		TradingSymbol:   "RELIANCE",
		InstrumentKey:   "nse_cm|2885",
		TransactionType: "BUY",
		Quantity:        10,
		Status:          StatusInPosition,
	})

	if err := engine.Disarm(ctx, b.ID); err != nil {
		t.Fatalf("Disarm failed: %v", err)
	}

	got, _ := engine.Get(b.ID)
	if got.Status != StatusExitedManual {
		t.Fatalf("expected EXITED_MANUAL, got %s", got.Status)
	}
	if orderPlaced {
		t.Fatal("Disarm should not place any order")
	}
}
