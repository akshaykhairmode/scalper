package market

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

type testScripRepository struct {
	options []scrip.Option
	spots   []scrip.Spot
}

func (r testScripRepository) ReplaceScripSnapshot(context.Context, []scrip.Option, []scrip.Spot, []scrip.SyncRecord) error {
	return nil
}

func (r testScripRepository) LoadScripSnapshot(context.Context) ([]scrip.Option, []scrip.Spot, error) {
	return r.options, r.spots, nil
}

func TestIndicesPreservesAvailableQuotesWhenOneSymbolIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{
			"exchange_token":"Nifty 50",
			"display_symbol":"Nifty 50-IN",
			"exchange":"nse_cm",
			"ltp":"22708.85",
			"ohlc":{"close":"22600"}
		}]`)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Market.Indices = []config.Index{
		{
			Key: "NIFTY", DisplayName: "NIFTY", IndexSegment: "nse_cm",
			IndexSymbol: "Nifty 50", ScripName: "NIFTY",
		},
		{
			Key: "MIDCPNIFTY", DisplayName: "MIDCPNIFTY", IndexSegment: "nse_cm",
			IndexSymbol: "Nifty Midcap Select", ScripName: "MIDCPNIFTY",
		},
	}
	scripts, err := scrip.NewManager(scrip.ManagerConfig{
		Segments: []string{"nse_cm"},
		Underlyings: []scrip.Underlying{
			{Key: "NIFTY", ScripName: "NIFTY"},
			{Key: "MIDCPNIFTY", ScripName: "MIDCPNIFTY"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := NewQuotesClient("test-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.http = server.Client()
	service, err := NewService(cfg, scripts, client)
	if err != nil {
		t.Fatal(err)
	}

	indices, err := service.Indices(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	wantCount := len(cfg.Market.Indices) + len(cfg.Market.Commodities)
	if len(indices) != wantCount {
		t.Fatalf("got %d underlying results, want %d", len(indices), wantCount)
	}
	if !indices[0].QuoteAvailable || indices[0].LTP != 22708.85 || indices[0].PreviousClose != 22600 {
		t.Fatalf("NIFTY quote = %+v", indices[0])
	}
	if indices[1].QuoteAvailable || indices[1].LTP != 0 {
		t.Fatalf("missing MIDCPNIFTY quote = %+v", indices[1])
	}
}

func TestCommodityUnderlyingsExposeExpiriesAndChain(t *testing.T) {
	cfg := config.Default()
	cfg.Market.Commodities = []config.Commodity{{
		Key: "CRUDEOIL", DisplayName: "CRUDE OIL", ExchangeSegment: scrip.SegmentMCXFO,
		ScripName: "CRUDEOIL",
	}}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	option := scrip.Option{
		InstrumentKey:   scrip.InstrumentKey(scrip.SegmentMCXFO, "CRUDEOIL", "2026-11-26", 600000, scrip.Call),
		ExchangeSegment: scrip.SegmentMCXFO, PSymbol: "mcx-token", PTrdSymbol: "CRUDEOIL26NOV6000CE",
		Underlying: "CRUDEOIL", Expiry: "2026-11-26", StrikePaise: 600000, OptionType: scrip.Call, LotSize: 100,
	}
	repo := &testScripRepository{options: []scrip.Option{option}}
	scripts, err := scrip.NewManager(scrip.ManagerConfig{
		Segments: []string{scrip.SegmentMCXFO}, Underlyings: []scrip.Underlying{{
			Key: "CRUDEOIL", DisplayName: "CRUDE OIL", ScripName: "CRUDEOIL", ExchangeSegment: scrip.SegmentMCXFO,
		}}, Repository: repo, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scripts.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(cfg, scripts, NewQuotesClient("", nil))
	if err != nil {
		t.Fatal(err)
	}
	if expiries, err := service.Expiries("CRUDEOIL"); err != nil || len(expiries) != 1 {
		t.Fatalf("Expiries = %+v, %v", expiries, err)
	}
	if !service.configuredUnderlying("CRUDEOIL") {
		t.Fatal("commodity underlying is not configured")
	}
}

func TestIndicesErrorsWhenNoConfiguredIndexQuoteIsReturned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Market.Indices = cfg.Market.Indices[:1]
	scripts, err := scrip.NewManager(scrip.ManagerConfig{
		Segments:    []string{"nse_cm"},
		Underlyings: []scrip.Underlying{{Key: "NIFTY", ScripName: "NIFTY"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := NewQuotesClient("test-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.http = server.Client()
	service, err := NewService(cfg, scripts, client)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Indices(context.Background(), server.URL); err == nil {
		t.Fatal("expected an error when Kotak returns no configured index quotes")
	}
}

func TestCandlesResolvesCanonicalInstrumentsToKotakSymbols(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Query().Get("neosymbol") + "|" + r.URL.Query().Get("interval")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","data":{"candles":[]}}`)
	}))
	defer server.Close()

	option := scrip.Option{
		InstrumentKey:   scrip.InstrumentKey("nse_fo", "NIFTY", "2026-10-29", 2_500_000, scrip.Call),
		ExchangeSegment: "nse_fo",
		PSymbol:         "12345",
		PTrdSymbol:      "NIFTY26OCT25000CE",
		Underlying:      "NIFTY",
		Expiry:          "2026-10-29",
		StrikePaise:     2_500_000,
		OptionType:      scrip.Call,
		LotSize:         25,
	}
	spot := scrip.Spot{
		InstrumentKey:   scrip.SpotKey("nse_cm", "NIFTY"),
		ExchangeSegment: "nse_cm",
		Symbol:          "Nifty 50",
		PSymbol:         "1333",
	}
	scripts, err := scrip.NewManager(scrip.ManagerConfig{
		Segments: []string{"nse_cm", "nse_fo"},
		Underlyings: []scrip.Underlying{{
			Key: "NIFTY", ScripName: "NIFTY", ExchangeSegment: "nse_fo", SpotSegment: "nse_cm",
		}},
		Repository: testScripRepository{options: []scrip.Option{option}, spots: []scrip.Spot{spot}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scripts.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := NewQuotesClient("test-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.http = server.Client()
	cfg := config.Default()
	service, err := NewService(cfg, scripts, client)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Candles(context.Background(), server.URL, "nse_cm|NIFTY", "5m"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Candles(context.Background(), server.URL, option.InstrumentKey, "1h"); err != nil {
		t.Fatal(err)
	}
	got := []string{<-requests, <-requests}
	if !strings.Contains(strings.Join(got, ","), "nse_cm|Nifty 50|5min") ||
		!strings.Contains(strings.Join(got, ","), "nse_fo|12345|60min") {
		t.Fatalf("historical instrument requests = %v", got)
	}
}
