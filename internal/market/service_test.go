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
	requests := make(chan string, 3)
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
	if _, err := service.Candles(context.Background(), server.URL, option.InstrumentKey, "4h"); err != nil {
		t.Fatal(err)
	}
	got := []string{<-requests, <-requests, <-requests}
	if !strings.Contains(strings.Join(got, ","), "nse_cm|Nifty 50|5min") ||
		!strings.Contains(strings.Join(got, ","), "nse_fo|12345|60min") {
		t.Fatalf("historical instrument requests = %v", got)
	}
}

func TestAggregateFourHourCandlesAlignsToSessionOpen(t *testing.T) {
	loc := time.FixedZone("IST", 5*60*60+30*60)
	at := func(hour, minute int) int64 {
		return time.Date(2026, time.November, 10, hour, minute, 0, 0, loc).Unix()
	}
	got := aggregateFourHourCandles([]Candle{
		{Time: at(9, 15), Open: 100, High: 102, Low: 99, Close: 101, Volume: 10, OpenInterest: 1},
		{Time: at(10, 15), Open: 101, High: 105, Low: 100, Close: 104, Volume: 20, OpenInterest: 2},
		{Time: at(11, 15), Open: 104, High: 108, Low: 103, Close: 107, Volume: 30, OpenInterest: 3},
		{Time: at(12, 15), Open: 107, High: 109, Low: 105, Close: 106, Volume: 40, OpenInterest: 4},
		{Time: at(13, 15), Open: 106, High: 110, Low: 104, Close: 109, Volume: 50, OpenInterest: 5},
		{Time: at(14, 15), Open: 109, High: 112, Low: 108, Close: 111, Volume: 60, OpenInterest: 6},
		{Time: at(15, 15), Open: 111, High: 113, Low: 110, Close: 112, Volume: 70, OpenInterest: 7},
	}, "nse_fo")

	if len(got) != 2 {
		t.Fatalf("got %d 4h candles, want 2: %+v", len(got), got)
	}
	first, second := got[0], got[1]
	if first.Time != at(9, 15) || first.Open != 100 || first.High != 109 || first.Low != 99 || first.Close != 106 || first.Volume != 100 {
		t.Errorf("first 4h candle = %+v", first)
	}
	if second.Time != at(13, 15) || second.Open != 106 || second.High != 113 || second.Low != 104 || second.Close != 112 || second.Volume != 180 {
		t.Errorf("second 4h candle = %+v", second)
	}
}

func TestServiceQuotesAndScripHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{
			"exchange_token":"12345",
			"display_symbol":"NIFTY26OCT25000CE",
			"exchange":"nse_fo",
			"ltp":"150.25",
			"ohlc":{"close":"140.0"}
		}]`)
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

	ctx := context.Background()

	// Quotes & Quote
	quotes, err := service.Quotes(ctx, server.URL, []string{option.InstrumentKey})
	if err != nil {
		t.Fatalf("Quotes: %v", err)
	}
	if q, ok := quotes[option.InstrumentKey]; !ok || q.LTP != 150.25 {
		t.Fatalf("Quotes quote = %+v, want LTP 150.25", q)
	}

	singleQuote, err := service.Quote(ctx, server.URL, option.InstrumentKey)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if singleQuote.LTP != 150.25 {
		t.Fatalf("Quote LTP = %v, want 150.25", singleQuote.LTP)
	}

	// Option and FindOption
	if opt, ok := service.Option(option.InstrumentKey); !ok || opt.PSymbol != "12345" {
		t.Fatalf("service.Option = %+v, %v", opt, ok)
	}
	if opt, ok := service.FindOption("nse_fo", "12345"); !ok || opt.InstrumentKey != option.InstrumentKey {
		t.Fatalf("service.FindOption = %+v, %v", opt, ok)
	}

	// FindSpot
	if sp, ok := service.FindSpot("nse_cm", "NIFTY"); !ok || sp.PSymbol != "1333" {
		t.Fatalf("service.FindSpot = %+v, %v", sp, ok)
	}

	// ScripStatus
	st := service.ScripStatus()
	if !st.Loaded || st.Options != 1 || st.Spots != 1 {
		t.Fatalf("service.ScripStatus = %+v", st)
	}

	// IndexQuoteSymbol
	sym := IndexQuoteSymbol(config.Index{IndexSymbol: "Nifty Midcap Select"})
	if sym != "NIFTY MID SELECT" {
		t.Fatalf("IndexQuoteSymbol = %q, want NIFTY MID SELECT", sym)
	}
}
