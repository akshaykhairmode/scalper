package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/market"
)

type flappyMarketAPI struct {
	fakeMarketAPI
	indicesErr error
	chainErr   error
	chainCalls int
}

func (f *flappyMarketAPI) Indices(ctx context.Context, baseURL string) ([]market.IndexQuote, error) {
	if f.indicesErr != nil {
		return nil, f.indicesErr
	}
	return f.fakeMarketAPI.Indices(ctx, baseURL)
}

func (f *flappyMarketAPI) ChainWindow(_ context.Context, _, _, _ string, _ float64, _ int, _ string) (market.ChainPage, error) {
	f.chainCalls++
	if f.chainErr != nil {
		return market.ChainPage{}, f.chainErr
	}
	return market.ChainPage{
		Rows: []market.ChainRow{
			{Strike: 25000},
		},
	}, nil
}

func TestIsMarketOpen(t *testing.T) {
	cfg := testConfig()
	cfg.App.Timezone = "Asia/Kolkata"
	cfg.Trading.Session.Open = "09:15"
	cfg.Trading.Session.Close = "15:30"
	cfg.Market.Holidays = []string{"2026-08-15"}
	s := &Server{cfg: cfg}

	loc := s.cfg.Location()

	// Sunday
	sunday := time.Date(2026, 11, 8, 10, 30, 0, 0, loc)
	if s.isMarketOpen(sunday) {
		t.Errorf("expected closed on Sunday, got open")
	}

	// Saturday
	saturday := time.Date(2026, 11, 7, 10, 30, 0, 0, loc)
	if s.isMarketOpen(saturday) {
		t.Errorf("expected closed on Saturday, got open")
	}

	// Holiday (Independence day)
	holiday := time.Date(2026, 8, 15, 11, 0, 0, 0, loc)
	if s.isMarketOpen(holiday) {
		t.Errorf("expected closed on Holiday, got open")
	}

	// Weekday before market open (Monday 08:30)
	mondayMorning := time.Date(2026, 11, 9, 8, 30, 0, 0, loc)
	if s.isMarketOpen(mondayMorning) {
		t.Errorf("expected closed before open, got open")
	}

	// Weekday during market hours (Monday 10:30)
	mondayActive := time.Date(2026, 11, 9, 10, 30, 0, 0, loc)
	if !s.isMarketOpen(mondayActive) {
		t.Errorf("expected open during market hours, got closed")
	}

	// Weekday after market close (Monday 16:00)
	mondayAfter := time.Date(2026, 11, 9, 16, 0, 0, 0, loc)
	if s.isMarketOpen(mondayAfter) {
		t.Errorf("expected closed after close, got open")
	}
}

func TestIsUnderlyingMarketOpen(t *testing.T) {
	cfg := testConfig()
	cfg.App.Timezone = "Asia/Kolkata"
	cfg.Trading.Session.Open = "09:15"
	cfg.Trading.Session.Close = "15:30"
	cfg.Market.Commodities = []config.Commodity{
		{Key: "CRUDEOIL", ScripName: "CRUDEOIL", ExchangeSegment: "mcx_fo"},
		{Key: "NATURALGAS", ScripName: "NATURALGAS", ExchangeSegment: "mcx_fo"},
	}
	s := &Server{cfg: cfg}
	loc := s.cfg.Location()

	mondayEvening := time.Date(2026, 11, 9, 16, 30, 0, 0, loc)
	if s.isUnderlyingMarketOpen("NIFTY", mondayEvening) {
		t.Errorf("NIFTY should be closed at 16:30 IST")
	}
	if !s.isUnderlyingMarketOpen("CRUDEOIL", mondayEvening) {
		t.Errorf("CRUDEOIL should be open at 16:30 IST")
	}
	if !s.isUnderlyingMarketOpen("NATURALGAS", mondayEvening) {
		t.Errorf("NATURALGAS should be open at 16:30 IST")
	}

	mondayNight := time.Date(2026, 11, 9, 23, 50, 0, 0, loc)
	if !s.isUnderlyingMarketOpen("CRUDEOIL", mondayNight) {
		t.Errorf("CRUDEOIL should be open at 23:50 IST")
	}

	mondayMidnight := time.Date(2026, 11, 9, 23, 56, 0, 0, loc)
	if s.isUnderlyingMarketOpen("CRUDEOIL", mondayMidnight) {
		t.Errorf("CRUDEOIL should be closed at 23:56 IST")
	}

	sundayEvening := time.Date(2026, 11, 8, 16, 30, 0, 0, loc)
	if s.isUnderlyingMarketOpen("CRUDEOIL", sundayEvening) {
		t.Errorf("CRUDEOIL should be closed on Sunday")
	}
}

func TestHandleIndicesStaleFallback(t *testing.T) {
	api := &flappyMarketAPI{}
	s := newTestServer(t, serverOpts{marketAPI: api})

	// First request succeeds and populates cache
	req := httptest.NewRequest(http.MethodGet, "/api/indices", nil)
	rec := httptest.NewRecorder()
	s.handleIndices(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request code = %d, want 200", rec.Code)
	}

	// Simulate Kotak upstream failure
	api.indicesErr = errors.New("kotak timeout")
	s.indicesAt = time.Time{} // simulate expired TTL

	rec = httptest.NewRecorder()
	s.handleIndices(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback request code = %d, want 200 (stale cache)", rec.Code)
	}

	var resp struct {
		Indices []market.IndexQuote `json:"indices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Indices) == 0 || resp.Indices[0].Key != "NIFTY" {
		t.Fatalf("unexpected indices response: %+v", resp)
	}
}

func TestHandleChainCachingAndFallback(t *testing.T) {
	api := &flappyMarketAPI{}
	s := newTestServer(t, serverOpts{marketAPI: api})

	// First request succeeds and populates cache
	req := httptest.NewRequest(http.MethodGet, "/api/chain?index=NIFTY&expiry=2026-11-12&count=10&center=25000", nil)
	rec := httptest.NewRecorder()
	s.handleChain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request code = %d, want 200", rec.Code)
	}
	if api.chainCalls != 1 {
		t.Fatalf("chainCalls = %d, want 1", api.chainCalls)
	}

	// Second request within TTL hits cache (chainCalls should not increase)
	rec = httptest.NewRecorder()
	s.handleChain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached request code = %d, want 200", rec.Code)
	}
	if api.chainCalls != 1 {
		t.Fatalf("chainCalls after cache hit = %d, want 1", api.chainCalls)
	}

	// Simulate expired TTL and upstream failure
	api.chainErr = errors.New("kotak 15s timeout")
	s.chainCacheMu.Lock()
	for k, v := range s.chainCache {
		v.fetchedAt = time.Now().Add(-2 * time.Minute)
		s.chainCache[k] = v
	}
	s.chainCacheMu.Unlock()

	rec = httptest.NewRecorder()
	s.handleChain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback request code = %d, want 200 (stale cache)", rec.Code)
	}

	var page market.ChainPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("failed to decode fallback page: %v", err)
	}
	if len(page.Rows) == 0 || page.Rows[0].Strike != 25000 {
		t.Fatalf("unexpected fallback page: %+v", page)
	}
}
