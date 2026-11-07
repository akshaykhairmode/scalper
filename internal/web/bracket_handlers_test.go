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
	req.Host = "127.0.0.1:8080"
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

	// 4. Cancel bracket
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
