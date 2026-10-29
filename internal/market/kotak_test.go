package market

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestQuoteEndpointEscapesSymbolsAndRejectsUntrustedHosts(t *testing.T) {
	endpoint, err := quoteEndpoint(DefaultBaseURL, []QuoteInstrument{
		{ExchangeSegment: "nse_cm", Symbol: "Nifty 50"},
		{ExchangeSegment: "nse_fo", Symbol: "12345"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(endpoint, "nse_cm|Nifty%2050,nse_fo|12345/all") {
		t.Fatalf("quotes endpoint = %q", endpoint)
	}
	if _, err := quoteEndpoint("https://kotaksecurities.com.attacker.example", []QuoteInstrument{
		{ExchangeSegment: "nse_cm", Symbol: "Nifty 50"},
	}); err == nil {
		t.Fatal("accepted untrusted quotes host")
	}
}

func TestQuotesClientDecodesAndLogsRedactedExchange(t *testing.T) {
	var requestAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestAuthorization = r.Header.Get("Authorization")
		if !strings.Contains(r.URL.EscapedPath(), "nse_cm%7CNifty%2050") {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{
			  "exchange_token":"Nifty 50",
			  "display_symbol":"NIFTY-IN",
			  "exchange":"nse_cm",
			  "lstup_time":"1761566400",
			  "ltp":"25250.5",
			  "change":"25.5",
			  "per_change":"0.1",
			  "last_volume":"100",
			  "ohlc":{"close":"25225"},
			  "depth":{"buy":[{"price":"25250.4"}],"sell":[{"price":"25250.6"}]}
			}
		]`)
	}))
	defer server.Close()

	var logOutput strings.Builder
	client := NewQuotesClient("consumer-key-secret", slog.New(slog.NewTextHandler(&logOutput, nil)))
	client.http = server.Client()
	quotes, err := client.Fetch(context.Background(), server.URL, []QuoteInstrument{
		{ExchangeSegment: "nse_cm", Symbol: "Nifty 50"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestAuthorization != "consumer-key-secret" {
		t.Fatalf("Authorization = %q", requestAuthorization)
	}
	quote, ok := quotes[normalizeSymbol("Nifty 50")]
	if !ok {
		t.Fatalf("quotes = %+v", quotes)
	}
	if quote.LTP != 25250.5 || quote.PreviousClose != 25225 ||
		quote.Change != 25.5 || quote.ChangePct != 0.1 ||
		quote.Bid != 25250.4 || quote.Ask != 25250.6 ||
		quote.UpdatedAt != 1761566400 {
		t.Fatalf("decoded quote = %+v", quote)
	}
	if strings.Contains(logOutput.String(), "consumer-key-secret") {
		t.Fatal("logs leaked the Kotak consumer key")
	}
	if !strings.Contains(logOutput.String(), "[REDACTED]") {
		t.Fatalf("exchange log did not redact Authorization: %s", logOutput.String())
	}
}

func TestDecodeQuotesRejectsMalformedPrices(t *testing.T) {
	if _, err := decodeQuotes([]byte(`[{"ltp":"NaN"}]`)); err == nil {
		t.Fatal("accepted a non-finite market price")
	}
	if _, err := decodeQuotes([]byte(`{"error":"broker unavailable"}`)); err == nil {
		t.Fatal("accepted a non-array quotes payload")
	}
}

func TestHistoricalClientDecodesKotakCandleRows(t *testing.T) {
	var requestAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestAuthorization = r.Header.Get("Authorization")
		if r.URL.Path != historicalPath {
			t.Errorf("path = %q, want %q", r.URL.Path, historicalPath)
		}
		query := r.URL.Query()
		if query.Get("neosymbol") != "nse_cm|1333" ||
			query.Get("interval") != "5min" ||
			query.Get("fromdate") != "2026-10-01" ||
			query.Get("todate") != "2026-10-28" {
			t.Errorf("historical query = %v", query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"status":"success",
			"interval":"5min",
			"data":{"candles":[
				["2026-10-28T09:15:00+0530",12009.9,12019.35,12001.25,12001.5,163275,null],
				["2026-10-28T09:20:00+0530","12001","12003","11998.25","12001",105750,13667775]
			]}
		}`)
	}))
	defer server.Close()

	client := NewQuotesClient("consumer-key-secret", slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.http = server.Client()
	candles, err := client.Historical(context.Background(), server.URL, "nse_cm|1333", "5min", "2026-10-01", "2026-10-28")
	if err != nil {
		t.Fatal(err)
	}
	if requestAuthorization != "consumer-key-secret" {
		t.Fatalf("Authorization = %q", requestAuthorization)
	}
	wantTime := time.Date(2026, 10, 28, 9, 15, 0, 0, time.FixedZone("IST", 5*60*60+30*60)).Unix()
	if len(candles) != 2 || candles[0].Time != wantTime ||
		candles[0].Open != 12009.9 || candles[0].High != 12019.35 ||
		candles[0].Low != 12001.25 || candles[0].Close != 12001.5 ||
		candles[0].Volume != 163275 || candles[0].OpenInterest != 0 ||
		candles[1].OpenInterest != 13667775 {
		t.Fatalf("candles = %+v", candles)
	}
}

func TestHistoricalEndpointRejectsInvalidInput(t *testing.T) {
	if _, err := historicalEndpoint(DefaultBaseURL, "nse_cm|1333", "2min", "2026-10-01", "2026-10-28"); err == nil {
		t.Fatal("accepted unsupported historical interval")
	}
	if _, err := historicalEndpoint(DefaultBaseURL, "nse_cm|1333", "5min", "2026-10-29", "2026-10-28"); err == nil {
		t.Fatal("accepted reversed historical date range")
	}
	endpoint, err := historicalEndpoint(DefaultBaseURL, "nse_cm|1333", "5min", "2026-10-01", "2026-10-28")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("neosymbol"); got != "nse_cm|1333" {
		t.Fatalf("neosymbol query = %q", got)
	}
}

func TestDecodeHistoricalCandlesRejectsAPIErrorsAndMalformedRows(t *testing.T) {
	cases := []string{
		`{"status":"ERROR","fault":{"message":"Invalid interval value"}}`,
		`{"status":"success","data":{}}`,
		`{"status":"success","data":{"candles":[["bad timestamp",1,2,0,1,1,null]]}}`,
		`{"status":"success","data":{"candles":[["2026-10-28T09:15:00+0530","invalid_number",0,2,1,1,null]]}}`,
	}
	for _, response := range cases {
		if _, err := decodeHistoricalCandles([]byte(response)); err == nil {
			t.Errorf("accepted malformed historical response %s", response)
		}
	}
}
