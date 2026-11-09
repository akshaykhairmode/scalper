package web

import (
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/hsm"
)

func TestLiveQuoteCache(t *testing.T) {
	s := &Server{}
	at := time.Now()
	s.setLiveQuote("nse_fo|NIFTY26NOV25000CE", &hsm.Quote{
		LTP:  101.5,
		Bid:  101.4,
		Ask:  101.6,
		Bids: []hsm.DepthLevel{{Price: 101.4, Quantity: 10, Orders: 1}},
		Asks: []hsm.DepthLevel{{Price: 101.6, Quantity: 12, Orders: 2}},
	}, at)

	if ltp, ok := s.cachedLTP("nse_fo|NIFTY26NOV25000CE", time.Second); !ok || ltp != 101.5 {
		t.Fatalf("cachedLTP = %v, %v", ltp, ok)
	}
	bids, asks, ok := s.cachedDepth("nse_fo|NIFTY26NOV25000CE", time.Second)
	if !ok || len(bids) != 1 || len(asks) != 1 || bids[0].Price != 101.4 || asks[0].Price != 101.6 {
		t.Fatalf("cachedDepth = %+v %+v %v", bids, asks, ok)
	}
	if _, ok := s.cachedLTP("nse_fo|NIFTY26NOV25000CE", -time.Second); ok {
		t.Fatal("stale quote should not be returned")
	}
	if _, ok := s.cachedLTP("missing", time.Second); ok {
		t.Fatal("unknown key should not be cached")
	}
}
