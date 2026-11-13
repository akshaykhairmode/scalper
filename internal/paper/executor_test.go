package paper

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

type fakeStore struct {
	account   Account
	orders    map[string]Order
	trades    []Trade
	positions map[string]Position
	recordErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		orders:    make(map[string]Order),
		positions: make(map[string]Position),
	}
}

func (f *fakeStore) EnsurePaperAccount(_ context.Context, starting float64) (Account, error) {
	if f.account.StartingBalance == 0 {
		f.account.StartingBalance = starting
	}
	return f.account, nil
}

func (f *fakeStore) SavePaperAccount(_ context.Context, a Account) error {
	f.account = a
	return nil
}

func (f *fakeStore) CreatePaperOrder(_ context.Context, o Order) error {
	f.orders[o.ID] = o
	return nil
}

func (f *fakeStore) UpdatePaperOrder(_ context.Context, o Order) error {
	f.orders[o.ID] = o
	return nil
}

func (f *fakeStore) GetPaperOrder(_ context.Context, id string) (*Order, error) {
	o, ok := f.orders[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &o, nil
}

func (f *fakeStore) ListPaperOrders(_ context.Context, _ int) ([]Order, error) {
	out := make([]Order, 0, len(f.orders))
	for _, o := range f.orders {
		out = append(out, o)
	}
	return out, nil
}

func (f *fakeStore) ListOpenPaperOrders(_ context.Context) ([]Order, error) {
	var out []Order
	for _, o := range f.orders {
		if o.Open() {
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fakeStore) RecordPaperFill(_ context.Context, o Order, p Position, a Account, t Trade) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	f.orders[o.ID] = o
	f.positions[p.InstrumentKey] = p
	f.account = a
	f.trades = append(f.trades, t)
	return nil
}

func (f *fakeStore) CreatePaperFill(_ context.Context, o Order, p Position, a Account, t Trade) error {
	return f.RecordPaperFill(context.Background(), o, p, a, t)
}

func (f *fakeStore) AddPaperTrade(_ context.Context, t Trade) error {
	f.trades = append(f.trades, t)
	return nil
}

func (f *fakeStore) ListPaperTrades(_ context.Context, _ int) ([]Trade, error) {
	return f.trades, nil
}

func (f *fakeStore) UpsertPaperPosition(_ context.Context, p Position) error {
	f.positions[p.InstrumentKey] = p
	return nil
}

func (f *fakeStore) ListPaperPositions(_ context.Context) ([]Position, error) {
	out := make([]Position, 0, len(f.positions))
	for _, p := range f.positions {
		out = append(out, p)
	}
	return out, nil
}

var ErrNotFound = errors.New("not found")

func newTestExecutor(t *testing.T, store Store) *Executor {
	t.Helper()
	ex, err := New(context.Background(), Options{
		Store:           store,
		StartingBalance: 100000,
		Slippage:        0.05,
		ShortMarginPct:  20,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ex
}

func buyOrder(key string) kotakapi.Order {
	return kotakapi.Order{
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY",
		InstrumentKey:   key,
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "MKT",
		Quantity:        75,
		Validity:        "DAY",
	}
}

func TestMarketBuyFillsAtAskWithSlippage(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	ex := newTestExecutor(t, store)
	ex.OnTick(ctx, "nse_fo|NIFTY", 100, 99.5, 100.5)

	id, err := ex.Place(ctx, buyOrder("nse_fo|NIFTY"))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	o, err := store.GetPaperOrder(ctx, id)
	if err != nil {
		t.Fatalf("GetPaperOrder: %v", err)
	}
	if o.Status != StatusComplete {
		t.Fatalf("status = %s, want COMPLETE", o.Status)
	}
	want := 100.5 + 0.05
	if o.FilledPrice != want {
		t.Fatalf("filled price = %v, want %v", o.FilledPrice, want)
	}
	positions := ex.Positions()
	if len(positions) != 1 || positions[0].NetQuantity != 75 || positions[0].AveragePrice != want {
		t.Fatalf("unexpected position: %+v", positions)
	}
	if len(store.trades) != 1 || store.trades[0].Price != want {
		t.Fatalf("unexpected trades: %+v", store.trades)
	}
}

func TestMarketOrderRequiresQuote(t *testing.T) {
	ex := newTestExecutor(t, newFakeStore())
	if _, err := ex.Place(context.Background(), buyOrder("missing")); err == nil {
		t.Fatal("expected error without a live quote")
	}
}

func TestLimitBuyRestsAndFillsOnTick(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	ex := newTestExecutor(t, store)
	ex.OnTick(ctx, "key", 101, 100.9, 101.1)

	order := buyOrder("key")
	order.OrderType = "L"
	order.Price = 100
	id, err := ex.Place(ctx, order)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	got, _ := store.GetPaperOrder(ctx, id)
	if got.Status != StatusOpen {
		t.Fatalf("status = %s, want OPEN", got.Status)
	}

	ex.OnTick(ctx, "key", 100.2, 100.1, 100.3)
	got, _ = store.GetPaperOrder(ctx, id)
	if got.Status != StatusOpen {
		t.Fatalf("status = %s after non-crossing tick", got.Status)
	}

	ex.OnTick(ctx, "key", 99.9, 99.8, 100.0)
	got, _ = store.GetPaperOrder(ctx, id)
	if got.Status != StatusComplete || got.FilledPrice != 100 {
		t.Fatalf("unexpected fill: %+v", got)
	}
}

func TestStopMarketSellTriggers(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	ex := newTestExecutor(t, store)
	ex.OnTick(ctx, "key", 100, 99.9, 100.1)

	order := buyOrder("key")
	order.TransactionType = "SELL"
	order.OrderType = "SL-M"
	order.TriggerPrice = 95
	id, err := ex.Place(ctx, order)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	got, _ := store.GetPaperOrder(ctx, id)
	if got.Status != StatusTriggerPending {
		t.Fatalf("status = %s, want TRIGGER_PENDING", got.Status)
	}
	ex.OnTick(ctx, "key", 96, 95.9, 96.1)
	got, _ = store.GetPaperOrder(ctx, id)
	if got.Status != StatusTriggerPending {
		t.Fatalf("status = %s before trigger", got.Status)
	}
	ex.OnTick(ctx, "key", 94.9, 94.8, 95.0)
	got, _ = store.GetPaperOrder(ctx, id)
	if got.Status != StatusComplete {
		t.Fatalf("status = %s after trigger", got.Status)
	}
	if got.FilledPrice != 95-0.05 {
		t.Fatalf("filled price = %v", got.FilledPrice)
	}
}

func TestPositionFlipRealizesPnL(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	ex := newTestExecutor(t, store)
	ex.OnTick(ctx, "key", 100, 100, 100)

	// Buy 100 at 100 (ask = 100, slippage 0.05 -> 100.05).
	buy := buyOrder("key")
	buy.Quantity = 100
	if _, err := ex.Place(ctx, buy); err != nil {
		t.Fatalf("buy: %v", err)
	}
	ex.OnTick(ctx, "key", 110, 110, 110)
	sell := buyOrder("key")
	sell.TransactionType = "SELL"
	sell.Quantity = 150
	if _, err := ex.Place(ctx, sell); err != nil {
		t.Fatalf("sell: %v", err)
	}
	positions := ex.Positions()
	if len(positions) != 1 {
		t.Fatalf("positions = %+v", positions)
	}
	p := positions[0]
	if p.NetQuantity != -50 {
		t.Fatalf("net qty = %d, want -50", p.NetQuantity)
	}
	if p.AveragePrice != 109.95 {
		t.Fatalf("avg = %v, want 109.95", p.AveragePrice)
	}
	wantRealized := (109.95 - 100.05) * 100
	if diff := p.RealizedPnL - wantRealized; diff > 0.001 || diff < -0.001 {
		t.Fatalf("realized = %v, want %v", p.RealizedPnL, wantRealized)
	}
}

func TestCancelRestingOrder(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	ex := newTestExecutor(t, store)
	ex.OnTick(ctx, "key", 100, 100, 100)
	order := buyOrder("key")
	order.OrderType = "L"
	order.Price = 99
	id, err := ex.Place(ctx, order)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if err := ex.Cancel(ctx, id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	ex.OnTick(ctx, "key", 98, 98, 98)
	got, _ := store.GetPaperOrder(ctx, id)
	if got.Status != StatusCancelled {
		t.Fatalf("status = %s, want CANCELLED", got.Status)
	}
}

func TestFillCallbackDelivered(t *testing.T) {
	ctx := context.Background()
	ex := newTestExecutor(t, newFakeStore())
	ex.OnTick(ctx, "key", 100, 100, 100)
	var got bracket.Fill
	ex.SetOnFill(func(f bracket.Fill) { got = f })
	id, err := ex.Place(ctx, buyOrder("key"))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if got.OrderID != id || got.Status != StatusComplete {
		t.Fatalf("fill = %+v", got)
	}
}

func TestStaleQuoteRejected(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	ex, err := New(ctx, Options{
		Store:           newFakeStore(),
		StartingBalance: 100000,
		Slippage:        0.05,
		ShortMarginPct:  20,
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ex.OnTick(ctx, "key", 100, 100, 100)
	now = now.Add(maxQuoteAge + time.Second)
	if _, err := ex.Place(ctx, buyOrder("key")); err == nil {
		t.Fatal("expected stale quote to reject a market order")
	}
}

func TestCancelEntryDeliversCancelledFill(t *testing.T) {
	ctx := context.Background()
	ex := newTestExecutor(t, newFakeStore())
	ex.OnTick(ctx, "key", 100, 100, 100)
	order := buyOrder("key")
	order.OrderType = "L"
	order.Price = 99
	order.Tag = "brk_1"
	id, err := ex.Place(ctx, order)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	var got bracket.Fill
	ex.SetOnFill(func(f bracket.Fill) { got = f })
	if err := ex.Cancel(ctx, id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got.OrderID != id || got.Tag != "brk_1" || got.Status != StatusCancelled {
		t.Fatalf("cancel fill = %+v", got)
	}
}

func TestDayOrderExpiresAcrossSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	ex, err := New(ctx, Options{
		Store:           newFakeStore(),
		StartingBalance: 100000,
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ex.OnTick(ctx, "key", 100, 100, 100)
	order := buyOrder("key")
	order.OrderType = "L"
	order.Price = 100
	order.Tag = "brk_1"
	id, err := ex.Place(ctx, order)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	now = now.Add(24 * time.Hour)
	ex.OnTick(ctx, "key", 99, 99, 99)
	got, ok := ex.Order(id)
	if !ok || got.Status != StatusCancelled {
		t.Fatalf("order should expire across sessions: %+v", got)
	}
}

func TestFillPersistenceFailureDoesNotMutate(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.recordErr = errors.New("db down")
	ex := newTestExecutor(t, store)
	ex.OnTick(ctx, "key", 100, 100, 100)
	if _, err := ex.Place(ctx, buyOrder("key")); err == nil {
		t.Fatal("expected fill persistence error")
	}
	if positions := ex.Positions(); len(positions) != 0 {
		t.Fatalf("position mutated after failed fill: %+v", positions)
	}
	if trades, _ := store.ListPaperTrades(ctx, 10); len(trades) != 0 {
		t.Fatalf("trade persisted after failed fill: %+v", trades)
	}
	if orders, _ := store.ListPaperOrders(ctx, 10); len(orders) != 0 {
		t.Fatalf("order row survived failed market fill: %+v", orders)
	}
}

func TestRestoreOpenOrders(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.orders["paper_1"] = Order{
		ID:              "paper_1",
		InstrumentKey:   "key",
		TransactionType: "BUY",
		OrderType:       "L",
		Quantity:        75,
		Price:           90,
		Status:          StatusOpen,
		Validity:        "DAY",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	ex := newTestExecutor(t, store)
	ex.OnTick(ctx, "key", 89, 89, 89)
	got, _ := store.GetPaperOrder(ctx, "paper_1")
	if got.Status != StatusComplete {
		t.Fatalf("restored order did not fill: %+v", got)
	}
}
