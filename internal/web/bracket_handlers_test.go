package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/auth"
	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

//nolint:unparam
func localRequestWithBody(method, target string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:8080"
	req.SetBasicAuth("admin", "adminpass")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestBracketHandlers(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()

	engine := bracket.NewEngine(nil, func(_ context.Context, _ kotakapi.Order) (string, error) {
		return "ord-exit-ok", nil
	}, nil, testLogger())
	s.bracketEngine = engine

	// 1. List brackets initially (empty)
	rec := do(t, s, localRequest(http.MethodGet, "/api/brackets"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var listResp bracketListResponse
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("failed to decode brackets list: %v", err)
	}
	if len(listResp.Brackets) != 0 {
		t.Fatalf("expected 0 brackets, got %d", len(listResp.Brackets))
	}

	// 2. Create a bracket
	bReq := bracket.Bracket{
		ID:              "test-bracket-1",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26OCT26000CE",
		InstrumentKey:   "nse_fo|12345",
		TransactionType: "BUY",
		Quantity:        50,
		Product:         "MIS",
		EntryPrice:      100.5,
		TargetPrice:     115.0,
		StopLossPrice:   90.0,
		EntryOrderID:    "ord-1234",
	}
	body, _ := json.Marshal(bReq)
	rec = do(t, s, localRequestWithBody(http.MethodPost, "/api/brackets", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created bracket.Bracket
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("failed to decode created bracket: %v", err)
	}
	if created.ID != "test-bracket-1" || created.Status != bracket.StatusPendingEntry {
		t.Fatalf("unexpected created bracket: %+v", created)
	}
	if created.TargetPrice != 115.0 || created.StopLossPrice != 90.0 {
		t.Fatalf("expected target/stoploss to be preserved, got %+v", created)
	}

	// 3. List brackets again
	rec = do(t, s, localRequest(http.MethodGet, "/api/brackets"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("failed to decode brackets list: %v", err)
	}
	if len(listResp.Brackets) != 1 {
		t.Fatalf("expected 1 bracket, got %d", len(listResp.Brackets))
	}

	// 4. Move the target level from the chart.
	rec = do(t, s, localRequestWithBody(http.MethodPatch, "/api/brackets/test-bracket-1", []byte(`{"target_price":117.5,"confirm":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 updating target, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var moved bracket.Bracket
	if err := json.NewDecoder(rec.Body).Decode(&moved); err != nil {
		t.Fatalf("failed to decode moved bracket: %v", err)
	}
	if moved.TargetPrice != 117.5 {
		t.Fatalf("expected target 117.5, got %v", moved.TargetPrice)
	}

	// 5. Directional validation is enforced by the engine.
	rec = do(t, s, localRequestWithBody(http.MethodPatch, "/api/brackets/test-bracket-1", []byte(`{"target_price":95,"confirm":true}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid target, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 6. An empty level payload is rejected.
	rec = do(t, s, localRequestWithBody(http.MethodPatch, "/api/brackets/test-bracket-1", []byte(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty update, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 7. Cancel bracket
	cancelReq := localRequestWithBody(http.MethodPost, "/api/brackets/test-bracket-1/cancel", []byte(`{}`))
	rec = do(t, s, cancelReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Verify status is CANCELLED
	b, ok := s.bracketEngine.Get("test-bracket-1")
	if !ok || b.Status != bracket.StatusCancelled {
		t.Fatalf("expected status CANCELLED, got %+v", b)
	}

	// 5. Test Exit Bracket with another bracket
	bReq2 := bracket.Bracket{
		ID:              "test-bracket-2",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26OCT26000PE",
		InstrumentKey:   "nse_fo|54321",
		TransactionType: "BUY",
		Quantity:        50,
		Product:         "MIS",
		EntryPrice:      50.0,
		TargetPrice:     60.0,
		Status:          bracket.StatusInPosition,
	}
	_, err := s.bracketEngine.Create(context.Background(), bReq2)
	if err != nil {
		t.Fatalf("failed to create bracket 2: %v", err)
	}

	exitReq := localRequestWithBody(http.MethodPost, "/api/brackets/test-bracket-2/exit", []byte(`{}`))
	rec = do(t, s, exitReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	b2, ok := s.bracketEngine.Get("test-bracket-2")
	if !ok || b2.Status != bracket.StatusExitedManual {
		t.Fatalf("expected status EXITED_MANUAL, got %+v", b2)
	}
}

func TestBracketCreateEnforcesRiskLimits(t *testing.T) {
	s := riskTestServer(t, func(c *config.Config) {
		c.Trading.Risk.MaxLotsPerOrder = 1
	})
	defer s.Close()
	_, _ = s.a.SessionManager().NewBrokerSession(auth.TradeSession{
		Token:   "trade-token",
		SID:     "trade-sid",
		BaseURL: "https://cis.kotaksecurities.com",
	}, auth.LoginMethodTOTP)

	engine := bracket.NewEngine(nil, func(_ context.Context, _ kotakapi.Order) (string, error) {
		return "ord-1", nil
	}, nil, testLogger())
	s.bracketEngine = engine

	// Create a bracket violating max_lots_per_order (lot size is 75, quantity is 150 = 2 lots)
	bReq := bracket.Bracket{
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType: "BUY",
		Quantity:        150,
		EntryPrice:      100,
		TargetPrice:     110,
		StopLossPrice:   90,
	}
	body, _ := json.Marshal(bReq)
	req := localRequestWithBody(http.MethodPost, "/api/brackets", body)

	rec := do(t, s, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict risk block status, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestBracketHandlersExchangeStop(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()

	engine := bracket.NewEngine(nil, func(_ context.Context, _ kotakapi.Order) (string, error) {
		return "ord-exit-ok", nil
	}, nil, testLogger())
	engine.SetExchangeStopConfig(bracket.ExchangeStopConfig{
		Mode:               "sl_m",
		DistanceMode:       "multiplier",
		DistanceValue:      2.5,
		CancelRetries:      1,
		ReconcileOnStartup: true,
	})
	s.bracketEngine = engine

	base := bracket.Bracket{
		ID:                "test-xstop-1",
		ExchangeSegment:   "nse_fo",
		TradingSymbol:     "NIFTY26OCT26000CE",
		InstrumentKey:     "nse_fo|12345",
		TransactionType:   "BUY",
		Quantity:          50,
		Product:           "MIS",
		EntryPrice:        100.5,
		TargetPrice:       115.0,
		StopLossPrice:     90.0,
		EntryOrderID:      "ord-1234",
		ExchangeStopMode:  "SL-M",
		ExchangeStopPrice: 80.0,
	}
	body, _ := json.Marshal(base)
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/brackets", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created bracket.Bracket
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ExchangeStopMode != bracket.ExchangeStopSLM || created.ExchangeStopPrice != 80 {
		t.Fatalf("exchange stop not accepted: %+v", created)
	}

	// Trigger closer than the server stop is rejected.
	tooClose := base
	tooClose.ID = "test-xstop-2"
	tooClose.ExchangeStopPrice = 95
	body, _ = json.Marshal(tooClose)
	rec = do(t, s, localRequestWithBody(http.MethodPost, "/api/brackets", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a trigger inside the server SL, got %d", rec.Code)
	}

	// Invalid mode is rejected.
	badMode := base
	badMode.ID = "test-xstop-3"
	badMode.ExchangeStopMode = "banana"
	body, _ = json.Marshal(badMode)
	rec = do(t, s, localRequestWithBody(http.MethodPost, "/api/brackets", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid mode, got %d", rec.Code)
	}

	// v1 refuses to move the exchange stop from the chart.
	patchBody, _ := json.Marshal(map[string]any{"exchange_stop_price": 70})
	rec = do(t, s, localRequestWithBody(http.MethodPatch, "/api/brackets/test-xstop-1", patchBody))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for chart-editing the exchange stop, got %d", rec.Code)
	}
}

func TestBootstrapIncludesExchangeStopDefaults(t *testing.T) {
	cfg := config.Default()
	boot, err := newBootstrap(cfg, false)
	if err != nil {
		t.Fatalf("newBootstrap: %v", err)
	}
	var decoded bootstrapConfig
	if err := json.Unmarshal([]byte(boot), &decoded); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if decoded.ExchangeStopMode != "off" {
		t.Fatalf("exchangeStopMode = %q, want off", decoded.ExchangeStopMode)
	}
	if decoded.ExchangeStopDistanceMode != "multiplier" || decoded.ExchangeStopDistanceVal != 2.5 {
		t.Fatalf("unexpected distance defaults: %+v", decoded)
	}
}
