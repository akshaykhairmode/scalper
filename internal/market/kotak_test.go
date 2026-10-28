package market

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuoteEndpointEscapesSymbolsAndRejectsUntrustedHosts(t *testing.T) {
	endpoint, err := quoteEndpoint(DefaultBaseURL, []QuoteInstrument{
		{ExchangeSegment: "nse_cm", Symbol: "Nifty 50"},
		{ExchangeSegment: "nse_fo", Symbol: "12345"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(endpoint, "nse_cm%7CNifty%2050,nse_fo%7C12345/all") {
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
	client := NewQuotesClient("access-token-secret", slog.New(slog.NewTextHandler(&logOutput, nil)))
	client.http = server.Client()
	quotes, err := client.Fetch(context.Background(), server.URL, []QuoteInstrument{
		{ExchangeSegment: "nse_cm", Symbol: "Nifty 50"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestAuthorization != "access-token-secret" {
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
	if strings.Contains(logOutput.String(), "access-token-secret") {
		t.Fatal("logs leaked the Kotak access token")
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
