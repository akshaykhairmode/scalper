package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

func riskTestOrder() kotakapi.Order {
	return kotakapi.Order{
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		TransactionType: "B",
		Product:         "MIS",
		OrderType:       "LIMIT",
		Quantity:        75,
		Price:           100,
	}
}

func riskTestServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.Trading.Margin.CheckBeforeOrder = false
	cfg.Trading.Margin.FailClosed = false
	cfg.Trading.Risk = config.Risk{}
	if mutate != nil {
		mutate(&cfg)
	}
	instrument := scrip.Option{
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		ExchangeSegment: "nse_fo",
		PSymbol:         "NIFTY26NOV25000CE",
		PTrdSymbol:      "NIFTY26NOV25000CE",
		Underlying:      "NIFTY",
		LotSize:         75,
	}
	s := newTestServer(t, serverOpts{
		db:        fakeDB{},
		cfg:       &cfg,
		marketAPI: fakeMarketAPI{instrument: instrument, quote: market.Quote{LTP: 100}},
	})
	t.Cleanup(s.Close)
	return s
}

func TestOrderRiskMaxLotsPerOrder(t *testing.T) {
	s := riskTestServer(t, func(c *config.Config) { c.Trading.Risk.MaxLotsPerOrder = 1 })
	order := riskTestOrder()
	order.Quantity = 150
	err := s.checkOrderRisk(context.Background(), kotakapi.Session{SID: "sid"}, order)
	if err == nil || !strings.Contains(err.Error(), "max_lots_per_order") {
		t.Fatalf("expected max lots block, got %v", err)
	}
}

func TestOrderRiskMaxOrderValue(t *testing.T) {
	s := riskTestServer(t, func(c *config.Config) { c.Trading.Risk.MaxOrderValueINR = 10_000 })
	order := riskTestOrder()
	order.Quantity = 200
	err := s.checkOrderRisk(context.Background(), kotakapi.Session{SID: "sid"}, order)
	if err == nil || !strings.Contains(err.Error(), "max_order_value_inr") {
		t.Fatalf("expected max order value block, got %v", err)
	}
}

func TestOrderRiskMarginCheckUsesLimitsBook(t *testing.T) {
	s := riskTestServer(t, func(c *config.Config) {
		c.Trading.Margin.CheckBeforeOrder = true
		c.Trading.Margin.FailClosed = true
	})
	session := kotakapi.Session{SID: "sid"}
	s.books.reset(sessionKey(session))
	s.books.store(bookLimits, json.RawMessage(`{"data":{"available_margin":50}}`), nil)

	order := riskTestOrder()
	order.Quantity = 75 // ₹7,500 notional
	err := s.checkOrderRisk(context.Background(), session, order)
	if err == nil || !strings.Contains(err.Error(), "required margin") {
		t.Fatalf("expected margin block, got %v", err)
	}
}

func TestOrderRiskMarginCheckExemptsPositionReduction(t *testing.T) {
	s := riskTestServer(t, func(c *config.Config) {
		c.Trading.Margin.CheckBeforeOrder = true
		c.Trading.Margin.FailClosed = true
	})
	session := kotakapi.Session{SID: "sid"}
	s.books.reset(sessionKey(session))
	s.books.store(bookLimits, json.RawMessage(`{"data":{"available_margin":50}}`), nil)
	positionsRaw := json.RawMessage(`{"data":[
		{"instrument_key":"nse_fo|NIFTY|2026-11-26|25000|CE","exchange_segment":"nse_fo","trading_symbol":"NIFTY26NOV25000CE","underlying":"NIFTY","quantity":75,"lot_size":75}
	]}`)
	s.books.store(bookPositions, positionsRaw, nil)

	// An order that reduces an existing long position must NOT be blocked by margin checks
	exitOrder := riskTestOrder()
	exitOrder.TransactionType = "S"
	exitOrder.Quantity = 75 // ₹7,500 notional, exceeds ₹50 margin
	if err := s.checkOrderRisk(context.Background(), session, exitOrder); err != nil {
		t.Fatalf("expected exit order to pass margin check when reducing position, got %v", err)
	}

	// An order that increases the position must still be blocked
	buyOrder := riskTestOrder()
	buyOrder.TransactionType = "B"
	buyOrder.Quantity = 75
	if err := s.checkOrderRisk(context.Background(), session, buyOrder); err == nil || !strings.Contains(err.Error(), "required margin") {
		t.Fatalf("expected buy order to be blocked by margin check, got %v", err)
	}
}

func TestOrderRiskRateCaps(t *testing.T) {
	state := newRiskState()
	cfg := config.Risk{MaxOrdersPerMinute: 2, MaxExitOrdersPerSecond: 1}
	now := time.Now()
	if err := state.reserve("sid", "B", cfg, now); err != nil {
		t.Fatalf("first order: %v", err)
	}
	if err := state.reserve("sid", "B", cfg, now.Add(time.Second)); err != nil {
		t.Fatalf("second order: %v", err)
	}
	if err := state.reserve("sid", "B", cfg, now.Add(2*time.Second)); err == nil ||
		!strings.Contains(err.Error(), "max_orders_per_minute") {
		t.Fatalf("expected per-minute block, got %v", err)
	}

	// A fresh minute and a fresh session allow one exit, but not two.
	nextMinute := now.Add(2 * time.Minute)
	cfg.MaxOrdersPerMinute = 0
	if err := state.reserve("sid2", "S", cfg, nextMinute); err != nil {
		t.Fatalf("first exit: %v", err)
	}
	if err := state.reserve("sid2", "S", cfg, nextMinute.Add(100*time.Millisecond)); err == nil ||
		!strings.Contains(err.Error(), "max_exit_orders_per_second") {
		t.Fatalf("expected exit-rate block, got %v", err)
	}
}

func TestOrderRiskPositionExposureAndPnL(t *testing.T) {
	raw := json.RawMessage(`{"data":[
		{"instrument_key":"nse_fo|NIFTY|2026-11-26|25000|CE","exchange_segment":"nse_fo","trading_symbol":"NIFTY26NOV25000CE","underlying":"NIFTY","quantity":150,"lot_size":75,"pnl":-6000}
	]}`)
	positions, err := parseBrokerPositions(raw)
	if err != nil {
		t.Fatalf("parse positions: %v", err)
	}
	if len(positions) != 1 || positions[0].Quantity != 150 || positions[0].PnL != -6000 {
		t.Fatalf("positions = %+v", positions)
	}

	s := riskTestServer(t, func(c *config.Config) {
		c.Trading.Risk.MaxDailyLossINR = 5000
	})
	session := kotakapi.Session{SID: "sid"}
	s.books.reset(sessionKey(session))
	s.books.store(bookPositions, raw, nil)

	order := riskTestOrder()
	order.Quantity = 75
	err = s.checkOrderRisk(context.Background(), session, order)
	if err == nil || !strings.Contains(err.Error(), "daily loss") {
		t.Fatalf("expected daily-loss block, got %v", err)
	}
}

func TestParseAvailableMarginFindsNestedValue(t *testing.T) {
	value, ok := parseAvailableMargin(json.RawMessage(`{"data":{"Net":"125000.50"}}`))
	if !ok || value != 125000.5 {
		t.Fatalf("available margin = %v, %v", value, ok)
	}
}
