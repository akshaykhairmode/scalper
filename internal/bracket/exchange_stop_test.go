package bracket

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/orderfeed"
)

type recordedOrder struct {
	order kotakapi.Order
	id    string
}

type exchangeStopHarness struct {
	mu       sync.Mutex
	orders   []recordedOrder
	events   []string
	nextID   int
	canceled []string
	lookup   func(orderID, tag string) (string, string, float64, error)
	cancelFn func(ctx context.Context, id string) error
}

func newExchangeStopHarness() *exchangeStopHarness {
	return &exchangeStopHarness{}
}

func (h *exchangeStopHarness) placeOrder(_ context.Context, o kotakapi.Order) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id := "ord-" + strconv.Itoa(h.nextID)
	h.orders = append(h.orders, recordedOrder{order: o, id: id})
	h.events = append(h.events, "place:"+o.Tag)
	return id, nil
}

func (h *exchangeStopHarness) cancelOrder(ctx context.Context, id string) error {
	h.mu.Lock()
	h.canceled = append(h.canceled, id)
	h.events = append(h.events, "cancel:"+id)
	fn := h.cancelFn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return nil
}

func (h *exchangeStopHarness) lookupOrder(_ context.Context, orderID, tag string) (string, string, float64, error) {
	h.mu.Lock()
	fn := h.lookup
	h.mu.Unlock()
	if fn != nil {
		return fn(orderID, tag)
	}
	return orderID, "OPEN", 0, nil
}

func (h *exchangeStopHarness) placedByTag(tag string) (kotakapi.Order, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rec := range h.orders {
		if rec.order.Tag == tag {
			return rec.order, true
		}
	}
	return kotakapi.Order{}, false
}

func (h *exchangeStopHarness) eventIndex(prefix string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, e := range h.events {
		if e == prefix {
			return i
		}
	}
	return -1
}

func newExchangeStopEngine(t *testing.T, cfg ExchangeStopConfig) (*Engine, *exchangeStopHarness, *mockStore) {
	t.Helper()
	h := newExchangeStopHarness()
	store := newMockStore()
	engine := NewEngine(store, &testExec{place: h.placeOrder, cancel: h.cancelOrder, lookup: h.lookupOrder}, testLogger())
	engine.SetExchangeStopConfig(cfg)
	return engine, h, store
}

func fillEntry(t *testing.T, engine *Engine) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"order_id": "entry-1",
		"status":   "TRAD",
		"avgPrc":   100,
	})
	engine.OnOrderEvent(context.Background(), orderfeed.Event{Data: data})
}

func TestExchangeStopSLMPlacedOnEntryFill(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	cfg.DistanceMode = "multiplier"
	cfg.DistanceValue = 2.5
	engine, h, _ := newExchangeStopEngine(t, cfg)

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType: "BUY",
		Quantity:        50,
		Product:         "MIS",
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   94,
		Status:          StatusPendingEntry,
		EntryOrderID:    "entry-1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fillEntry(t, engine)

	ord, ok := h.placedByTag(exchangeStopTag(b.ID))
	if !ok {
		t.Fatal("expected an exchange stop order to be placed")
	}
	if ord.OrderType != "SL-M" || ord.TransactionType != "SELL" || ord.Quantity != 50 {
		t.Fatalf("unexpected exchange stop order: %+v", ord)
	}
	// Server stop distance is 6 points; 2.5x = 15 points below entry.
	if ord.TriggerPrice != 85 || ord.Price != 0 {
		t.Fatalf("trigger/price = %v/%v, want 85/0", ord.TriggerPrice, ord.Price)
	}
	got, ok := engine.Get(b.ID)
	if !ok || got.ExchangeStopStatus != ExchangeStopWorking || got.ExchangeStopOrderID == "" {
		t.Fatalf("exchange stop state not recorded: %+v", got)
	}
}

func TestExchangeStopSLLUsesLimitOffset(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLL
	cfg.DistanceMode = "points"
	cfg.DistanceValue = 15
	cfg.SLLOffsetTicks = 2
	engine, h, _ := newExchangeStopEngine(t, cfg)

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType: "BUY",
		Quantity:        50,
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   94,
		Status:          StatusPendingEntry,
		EntryOrderID:    "entry-1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fillEntry(t, engine)

	ord, ok := h.placedByTag(exchangeStopTag(b.ID))
	if !ok {
		t.Fatal("expected an exchange stop-limit order")
	}
	if ord.OrderType != "SL" || ord.TriggerPrice != 85 {
		t.Fatalf("unexpected SL order: %+v", ord)
	}
	if diff := ord.TriggerPrice - ord.Price; diff < 0.099 || diff > 0.101 {
		t.Fatalf("limit offset = %v, want 0.10", diff)
	}
}

func TestExchangeStopOffPlacesNothing(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopOff
	engine, h, _ := newExchangeStopEngine(t, cfg)

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType: "BUY",
		Quantity:        50,
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   94,
		Status:          StatusPendingEntry,
		EntryOrderID:    "entry-1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fillEntry(t, engine)

	if _, ok := h.placedByTag(exchangeStopTag(b.ID)); ok {
		t.Fatal("exchange stop must not be placed when the resolved mode is off")
	}
}

func TestExchangeStopPerOrderOffOverridesGlobal(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	engine, h, _ := newExchangeStopEngine(t, cfg)

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:  "nse_fo",
		TradingSymbol:    "NIFTY26NOV25000CE",
		InstrumentKey:    "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType:  "BUY",
		Quantity:         50,
		EntryPrice:       100,
		TargetPrice:      110,
		StopLossPrice:    94,
		Status:           StatusPendingEntry,
		EntryOrderID:     "entry-1",
		ExchangeStopMode: ExchangeStopOff,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fillEntry(t, engine)

	if _, ok := h.placedByTag(exchangeStopTag(b.ID)); ok {
		t.Fatal("per-order off must override the global default")
	}
}

func TestExchangeStopFilledMarksSLHit(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	engine, _, store := newExchangeStopEngine(t, cfg)

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:     "nse_fo",
		TradingSymbol:       "NIFTY26NOV25000CE",
		InstrumentKey:       "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType:     "BUY",
		Quantity:            50,
		EntryPrice:          100,
		TargetPrice:         110,
		StopLossPrice:       90,
		Status:              StatusInPosition,
		ExchangeStopMode:    ExchangeStopSLM,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopStatus:  ExchangeStopWorking,
		ExchangeStopPrice:   85,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, _ := json.Marshal(map[string]any{
		"order_id": "xstop-1",
		"status":   "TRAD",
		"avgPrc":   84.5,
	})
	engine.OnOrderEvent(context.Background(), orderfeed.Event{Data: data})

	got, _ := engine.Get(b.ID)
	if got.Status != StatusSLHit || got.ExchangeStopStatus != ExchangeStopFilled {
		t.Fatalf("expected SL_HIT/FILLED, got %s/%s", got.Status, got.ExchangeStopStatus)
	}
	if got.ExitPrice != 84.5 || got.PnL != (84.5-100)*50 {
		t.Fatalf("unexpected exit/PnL: %+v", got)
	}
	persisted, _ := store.GetBracket(context.Background(), b.ID)
	if persisted.Status != StatusSLHit {
		t.Fatalf("fill was not persisted: %+v", persisted)
	}
}

func TestNormalTriggerCancelsExchangeStopFirst(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	cfg.CancelRetries = 1
	engine, h, _ := newExchangeStopEngine(t, cfg)
	h.lookup = func(orderID, _ string) (string, string, float64, error) {
		return orderID, "CANCELLED", 0, nil
	}

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:     "nse_fo",
		TradingSymbol:       "NIFTY26NOV25000CE",
		InstrumentKey:       "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType:     "BUY",
		Quantity:            50,
		EntryPrice:          100,
		TargetPrice:         110,
		StopLossPrice:       95,
		Status:              StatusInPosition,
		ExchangeStopMode:    ExchangeStopSLM,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopStatus:  ExchangeStopWorking,
		ExchangeStopPrice:   85,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	engine.OnTick(context.Background(), b.InstrumentKey, 94)

	cancelIdx := h.eventIndex("cancel:xstop-1")
	exitIdx := h.eventIndex("place:exit_" + b.ID)
	if cancelIdx == -1 {
		t.Fatal("exchange stop was not cancelled")
	}
	if exitIdx == -1 || cancelIdx > exitIdx {
		t.Fatalf("exit placed before exchange stop cancel: cancel=%d exit=%d", cancelIdx, exitIdx)
	}
	got, _ := engine.Get(b.ID)
	if got.Status != StatusSLHit {
		t.Fatalf("expected SL_HIT, got %s", got.Status)
	}
}

func TestExchangeStopCancelFailureBlocksExit(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	cfg.CancelRetries = 1
	cfg.OnCancelFailure = "alert"
	engine, h, _ := newExchangeStopEngine(t, cfg)
	h.lookup = func(orderID, _ string) (string, string, float64, error) {
		return orderID, "OPEN", 0, nil
	}
	h.cancelFn = func(_ context.Context, _ string) error {
		return errors.New("broker rejected cancel")
	}

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:     "nse_fo",
		TradingSymbol:       "NIFTY26NOV25000CE",
		InstrumentKey:       "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType:     "BUY",
		Quantity:            50,
		EntryPrice:          100,
		TargetPrice:         110,
		StopLossPrice:       95,
		Status:              StatusInPosition,
		ExchangeStopMode:    ExchangeStopSLM,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopStatus:  ExchangeStopWorking,
		ExchangeStopPrice:   85,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	engine.OnTick(context.Background(), b.InstrumentKey, 94)

	if idx := h.eventIndex("place:exit_" + b.ID); idx != -1 {
		t.Fatal("a second sell was placed while the exchange stop was still live")
	}
	got, _ := engine.Get(b.ID)
	if got.Status != StatusExitFailed {
		t.Fatalf("expected EXIT_FAILED, got %s", got.Status)
	}
}

func TestReconcileExchangeStopFilled(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	engine, h, _ := newExchangeStopEngine(t, cfg)
	h.lookup = func(orderID, _ string) (string, string, float64, error) {
		return orderID, "TRAD", 82, nil
	}

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:     "nse_fo",
		TradingSymbol:       "NIFTY26NOV25000CE",
		InstrumentKey:       "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType:     "BUY",
		Quantity:            50,
		EntryPrice:          100,
		TargetPrice:         110,
		StopLossPrice:       95,
		Status:              StatusInPosition,
		ExchangeStopMode:    ExchangeStopSLM,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopStatus:  ExchangeStopWorking,
		ExchangeStopPrice:   85,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Create may replace the pre-set exchange stop state; restore it to model
	// a bracket loaded from disk.
	engine.mu.Lock()
	engine.brackets[b.ID].ExchangeStopOrderID = "xstop-1"
	engine.brackets[b.ID].ExchangeStopStatus = ExchangeStopWorking
	engine.brackets[b.ID].Status = StatusInPosition
	engine.mu.Unlock()

	engine.Reconcile(context.Background())

	got, _ := engine.Get(b.ID)
	if got.Status != StatusSLHit || got.ExchangeStopStatus != ExchangeStopFilled {
		t.Fatalf("expected reconciled fill, got %s/%s", got.Status, got.ExchangeStopStatus)
	}
	if got.ExitPrice != 82 {
		t.Fatalf("exit price = %v, want 82", got.ExitPrice)
	}
}

func TestReconcileExchangeStopWorkingKeepsState(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	engine, h, _ := newExchangeStopEngine(t, cfg)
	h.lookup = func(orderID, _ string) (string, string, float64, error) {
		return orderID, "OPEN", 0, nil
	}
	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:     "nse_fo",
		TradingSymbol:       "NIFTY26NOV25000CE",
		InstrumentKey:       "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType:     "BUY",
		Quantity:            50,
		EntryPrice:          100,
		TargetPrice:         110,
		StopLossPrice:       95,
		Status:              StatusInPosition,
		ExchangeStopMode:    ExchangeStopSLM,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopStatus:  ExchangeStopWorking,
		ExchangeStopPrice:   85,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	engine.Reconcile(context.Background())

	got, _ := engine.Get(b.ID)
	if got.ExchangeStopStatus != ExchangeStopWorking || got.Status != StatusInPosition {
		t.Fatalf("working stop changed on reconcile: %+v", got)
	}
	if len(h.orders) != 0 {
		t.Fatalf("reconcile placed %d unexpected orders", len(h.orders))
	}
}

func TestExchangeStopIdempotent(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	engine, h, _ := newExchangeStopEngine(t, cfg)
	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:     "nse_fo",
		TradingSymbol:       "NIFTY26NOV25000CE",
		InstrumentKey:       "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType:     "BUY",
		Quantity:            50,
		EntryPrice:          100,
		TargetPrice:         110,
		StopLossPrice:       95,
		Status:              StatusInPosition,
		ExchangeStopMode:    ExchangeStopSLM,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopStatus:  ExchangeStopWorking,
		ExchangeStopPrice:   85,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	engine.EnsureExchangeStop(context.Background(), b.ID)
	engine.EnsureExchangeStop(context.Background(), b.ID)
	if len(h.orders) != 0 {
		t.Fatalf("idempotent ensure placed %d orders", len(h.orders))
	}
}

func TestExchangeStopDistanceFallbacks(t *testing.T) {
	e := &Engine{exchangeStopCfg: DefaultExchangeStopConfig()}
	e.exchangeStopCfg.DistanceMode = "ticks"
	e.exchangeStopCfg.DistanceValue = 10
	b := &Bracket{TransactionType: "BUY", EntryPrice: 100}
	if got := e.exchangeStopDistanceLocked(b); got < 0.499 || got > 0.501 {
		t.Fatalf("tick distance = %v, want 0.50", got)
	}
	e.exchangeStopCfg.DistanceMode = "multiplier"
	e.exchangeStopCfg.DistanceValue = 2
	b.StopLossPrice = 0
	if got := e.exchangeStopDistanceLocked(b); got != 2 {
		t.Fatalf("fallback distance = %v, want 2%% of entry", got)
	}
}

func TestExchangeStopSLMRejectionFallsBackToSLL(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	engine, h, store := newExchangeStopEngine(t, cfg)

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:     "nse_fo",
		TradingSymbol:       "NIFTY26NOV25000CE",
		InstrumentKey:       "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType:     "BUY",
		Quantity:            50,
		EntryPrice:          100,
		TargetPrice:         110,
		StopLossPrice:       94,
		Status:              StatusInPosition,
		ExchangeStopMode:    ExchangeStopSLM,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopStatus:  ExchangeStopWorking,
		ExchangeStopPrice:   85,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, _ := json.Marshal(map[string]any{
		"order_id": "xstop-1",
		"status":   "rejected",
		"rejRsn":   "RMS: STOP LOSS MARKET ORDER NOT ALLOWED",
	})
	engine.OnOrderEvent(context.Background(), orderfeed.Event{Data: data})

	got, _ := engine.Get(b.ID)
	if got.ExchangeStopMode != ExchangeStopSLL {
		t.Fatalf("expected the bracket to fall back to SL, got %q", got.ExchangeStopMode)
	}
	if got.ExchangeStopStatus != ExchangeStopWorking {
		t.Fatalf("expected a working replacement, got %q", got.ExchangeStopStatus)
	}
	if got.ExchangeStopReason != "" {
		t.Fatalf("a working replacement should clear the old rejection reason, got %q", got.ExchangeStopReason)
	}

	h.mu.Lock()
	var placed []kotakapi.Order
	for _, rec := range h.orders {
		if rec.order.Tag == exchangeStopTag(b.ID) {
			placed = append(placed, rec.order)
		}
	}
	h.mu.Unlock()
	if len(placed) != 1 || placed[0].OrderType != "SL" {
		t.Fatalf("expected exactly one SL-L replacement, got %+v", placed)
	}
	if placed[0].TriggerPrice != 85 || placed[0].Price >= placed[0].TriggerPrice {
		t.Fatalf("unexpected SL-L prices: %+v", placed[0])
	}

	persisted, _ := store.GetBracket(context.Background(), b.ID)
	if persisted.ExchangeStopMode != ExchangeStopSLL || persisted.ExchangeStopOrderID == "" {
		t.Fatalf("fallback was not persisted: %+v", persisted)
	}
}

func TestExchangeStopCancelledSLMDoesNotFallBack(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	engine, h, _ := newExchangeStopEngine(t, cfg)

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:     "nse_fo",
		TradingSymbol:       "NIFTY26NOV25000CE",
		InstrumentKey:       "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType:     "BUY",
		Quantity:            50,
		EntryPrice:          100,
		TargetPrice:         110,
		StopLossPrice:       94,
		Status:              StatusInPosition,
		ExchangeStopMode:    ExchangeStopSLM,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopStatus:  ExchangeStopWorking,
		ExchangeStopPrice:   85,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, _ := json.Marshal(map[string]any{"order_id": "xstop-1", "status": "cancelled"})
	engine.OnOrderEvent(context.Background(), orderfeed.Event{Data: data})

	got, _ := engine.Get(b.ID)
	if got.ExchangeStopMode != ExchangeStopSLM {
		t.Fatalf("a cancel must not change the mode, got %q", got.ExchangeStopMode)
	}
	if got.ExchangeStopStatus != ExchangeStopCancelled {
		t.Fatalf("expected CANCELLED, got %q", got.ExchangeStopStatus)
	}
	if len(h.orders) != 0 {
		t.Fatalf("a cancel must not place a replacement, got %d orders", len(h.orders))
	}
}

func TestExchangeStopBSEFoCoercesSLMToSLL(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLM
	engine, h, _ := newExchangeStopEngine(t, cfg)

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:  "bse_fo",
		TradingSymbol:    "SENSEX26NOV83200CE",
		InstrumentKey:    "bse_fo|SENSEX|2026-11-26|83200|CE",
		TransactionType:  "BUY",
		Quantity:         20,
		EntryPrice:       100,
		TargetPrice:      110,
		StopLossPrice:    94,
		Status:           StatusInPosition,
		ExchangeStopMode: ExchangeStopSLM,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	engine.EnsureExchangeStop(context.Background(), b.ID)

	ord, ok := h.placedByTag(exchangeStopTag(b.ID))
	if !ok {
		t.Fatal("expected an exchange stop order")
	}
	if ord.OrderType != "SL" {
		t.Fatalf("BSE F&O must use SL-L, got %q", ord.OrderType)
	}
	got, _ := engine.Get(b.ID)
	if got.ExchangeStopMode != ExchangeStopSLL {
		t.Fatalf("bracket mode = %q, want SL", got.ExchangeStopMode)
	}
}

func TestExchangeStopRejectionReasonFromAuditTrail(t *testing.T) {
	cfg := DefaultExchangeStopConfig()
	cfg.Mode = ExchangeStopSLL
	h := newExchangeStopHarness()
	store := newMockStore()
	var askedFor string
	engine := NewEngine(store, &testExec{
		place:  h.placeOrder,
		cancel: h.cancelOrder,
		lookup: h.lookupOrder,
		rejection: func(_ context.Context, orderID string) (string, error) {
			askedFor = orderID
			return "SL-L Order Not Allowed — Stop-loss limit orders are not allowed for this segment.", nil
		},
	}, testLogger())
	engine.SetExchangeStopConfig(cfg)

	b, err := engine.Create(context.Background(), Bracket{
		ExchangeSegment:     "bse_fo",
		TradingSymbol:       "SENSEX26NOV83200CE",
		InstrumentKey:       "bse_fo|SENSEX|2026-11-26|83200|CE",
		TransactionType:     "BUY",
		Quantity:            20,
		EntryPrice:          100,
		TargetPrice:         110,
		StopLossPrice:       94,
		Status:              StatusInPosition,
		ExchangeStopMode:    ExchangeStopSLL,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopStatus:  ExchangeStopWorking,
		ExchangeStopPrice:   85,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, _ := json.Marshal(map[string]any{"order_id": "xstop-1", "status": "rejected"})
	engine.OnOrderEvent(context.Background(), orderfeed.Event{Data: data})

	if askedFor != "xstop-1" {
		t.Fatalf("audit trail was asked for %q", askedFor)
	}
	got, _ := engine.Get(b.ID)
	if got.ExchangeStopStatus != ExchangeStopRejected {
		t.Fatalf("status = %q, want REJECTED", got.ExchangeStopStatus)
	}
	if !strings.Contains(got.ExchangeStopReason, "not allowed") {
		t.Fatalf("rejection reason was not recorded: %q", got.ExchangeStopReason)
	}
	if len(h.orders) != 0 {
		t.Fatalf("a rejected SL-L must not be retried, got %d orders", len(h.orders))
	}
}
