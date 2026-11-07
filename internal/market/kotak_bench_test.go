package market

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

func makeTestQuotesJSON(count int) []byte {
	var sb strings.Builder
	sb.WriteString(`{"data":[`)
	for i := 0; i < count; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{
			"exchange_token":"%d",
			"display_symbol":"NIFTY24NOV%dCE",
			"exchange":"nse_fo",
			"lstup_time":"1761566400",
			"ltp":"%d.50",
			"change":"12.25",
			"per_change":"1.05",
			"last_volume":"15000",
			"oi":"45000",
			"ohlc":{"close":"%d.25"},
			"depth":{
				"buy":[
					{"price":"%d.45","quantity":"50","orders":"2"},
					{"price":"%d.40","quantity":"100","orders":"5"},
					{"price":"%d.35","quantity":"150","orders":"8"},
					{"price":"%d.30","quantity":"200","orders":"10"},
					{"price":"%d.25","quantity":"250","orders":"12"}
				],
				"sell":[
					{"price":"%d.55","quantity":"50","orders":"1"},
					{"price":"%d.60","quantity":"100","orders":"4"},
					{"price":"%d.65","quantity":"150","orders":"7"},
					{"price":"%d.70","quantity":"200","orders":"9"},
					{"price":"%d.75","quantity":"250","orders":"11"}
				]
			}
		}`, 10000+i, 24000+i*50, 150+i, 140+i, 150+i, 150+i, 150+i, 150+i, 150+i, 150+i, 150+i, 150+i, 150+i, 150+i)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String())
}

func BenchmarkDecodeQuotes(b *testing.B) {
	payload := makeTestQuotesJSON(40) // 40 strikes, typical option chain batch
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		quotes, err := decodeQuotes(payload)
		if err != nil || len(quotes) != 40 {
			b.Fatalf("failed decoding quotes: %v", err)
		}
	}
}

// BenchmarkDecodeQuotes_LegacyMapReflect measures the legacy reflective trial-unmarshal method.
func BenchmarkDecodeQuotes_LegacyMapReflect(b *testing.B) {
	payload := makeTestQuotesJSON(40)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var wrapper struct {
			Data []map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(payload, &wrapper); err != nil {
			b.Fatal(err)
		}
		quotes := make([]Quote, 0, len(wrapper.Data))
		for _, row := range wrapper.Data {
			var q Quote
			q.ExchangeToken = stringField(row, "exchange_token", "p_symbol")
			q.DisplaySymbol = stringField(row, "display_symbol", "symbol")
			q.LTP, _ = floatField(row, "ltp", "last_price")
			q.Change, _ = floatField(row, "change")
			q.ChangePct, _ = floatField(row, "per_change")
			q.Volume = intField(row, "last_volume")
			q.OI = intField(row, "oi")

			var depth map[string][]map[string]json.RawMessage
			if raw := row["depth"]; len(raw) > 0 {
				_ = json.Unmarshal(raw, &depth)
				for _, buy := range depth["buy"] {
					p, _ := floatField(buy, "price")
					qty := intField(buy, "quantity")
					ord := intField(buy, "orders")
					q.Bids = append(q.Bids, DepthLevel{Price: p, Quantity: qty, Orders: ord})
				}
				for _, sell := range depth["sell"] {
					p, _ := floatField(sell, "price")
					qty := intField(sell, "quantity")
					ord := intField(sell, "orders")
					q.Asks = append(q.Asks, DepthLevel{Price: p, Quantity: qty, Orders: ord})
				}
			}
			quotes = append(quotes, q)
		}
		if len(quotes) != 40 {
			b.Fatalf("unexpected count %d", len(quotes))
		}
	}
}

func stringField(row map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw := row[key]
		if len(raw) == 0 {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return strings.TrimSpace(value)
		}
		var number json.Number
		if json.Unmarshal(raw, &number) == nil {
			return number.String()
		}
	}
	return ""
}

func floatField(row map[string]json.RawMessage, keys ...string) (float64, bool) {
	for _, key := range keys {
		raw := row[key]
		if len(raw) == 0 {
			continue
		}
		var value json.Number
		if err := json.Unmarshal(raw, &value); err == nil {
			n, err := value.Float64()
			return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			n, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
			return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
		}
	}
	return 0, false
}

func intField(row map[string]json.RawMessage, keys ...string) int64 {
	n, ok := floatField(row, keys...)
	if !ok || n > math.MaxInt64 || n < math.MinInt64 {
		return 0
	}
	return int64(n)
}
