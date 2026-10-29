package kotakhttp

import "testing"

func TestEndpointClass(t *testing.T) {
	cases := map[string]string{
		"/script-details/1.0/quotes/neosymbol/nse_cm%7CNifty%2050": "quotes",
		"/quick/user/orders":         "orders",
		"/quick/user/orders/123":     "orders",
		"/quick/user/trades":         "trades",
		"/portfolio/v2/positions":    "positions",
		"/quick/user/limits":         "limits",
		"/portfolio/v1/holdings":     "holdings",
		"/quick/order/rule/ms/place": "order-write",
		"/quick/order/cancel":        "order-write",
		"/login/1.0/tradeApiLogin":   "login",
	}
	for path, want := range cases {
		if got := endpointClass(path); got != want {
			t.Errorf("endpointClass(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestReportResetsCounters(t *testing.T) {
	stats.Lock()
	stats.counts["orders"] = 3
	stats.limited = 1
	stats.errors = 2
	stats.Unlock()

	counts, limited, errors := Report()
	if counts["orders"] != 3 || limited != 1 || errors != 2 {
		t.Fatalf("Report() = %v, %d, %d", counts, limited, errors)
	}
	counts, limited, errors = Report()
	if len(counts) != 0 || limited != 0 || errors != 0 {
		t.Fatalf("second Report() = %v, %d, %d; want empty", counts, limited, errors)
	}
}
