package market

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

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
	if len(indices) != 2 {
		t.Fatalf("got %d index results, want 2", len(indices))
	}
	if !indices[0].QuoteAvailable || indices[0].LTP != 22708.85 || indices[0].PreviousClose != 22600 {
		t.Fatalf("NIFTY quote = %+v", indices[0])
	}
	if indices[1].QuoteAvailable || indices[1].LTP != 0 {
		t.Fatalf("missing MIDCPNIFTY quote = %+v", indices[1])
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
