package kotakhttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type mockCloseBody struct {
	io.Reader
}

func (m mockCloseBody) Close() error { return nil }

type mockRoundTripper struct{}

func (m *mockRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	time.Sleep(10 * time.Microsecond)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       mockCloseBody{Reader: strings.NewReader(`{"status":"ok"}`)},
		Header:     make(http.Header),
	}, nil
}

func BenchmarkHTTPTransport_Concurrency(b *testing.B) {
	client := NewClient(5 * time.Second)
	// Replace limited transport base with mock for pure concurrency benchmarking
	if lt, ok := client.Transport.(*limitedTransport); ok {
		lt.base = &mockRoundTripper{}
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://mis.kotaksecurities.com/quick/user/orders", nil)
		for pb.Next() {
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}

func BenchmarkEndpointClass(b *testing.B) {
	paths := []string{
		"/script-details/1.0/quotes/neosymbol/nse_cm%7CNifty%2050",
		"/quick/user/orders",
		"/portfolio/v2/positions",
		"/quick/order/rule/ms/place",
	}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = endpointClass(paths[i%len(paths)])
	}
}
