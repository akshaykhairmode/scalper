package market

import (
	"testing"

	"github.com/akshaykhairmode/scalper/internal/config"
)

func TestHSMFeedClassifiesConfiguredIndexSubscriptions(t *testing.T) {
	feed := NewHSMFeed(config.Default(), nil, nil)
	if err := feed.Subscribe([]string{" nse_cm|NIFTY "}); err != nil {
		t.Fatal(err)
	}
	subscription, ok := feed.subs["nse_cm|NIFTY"]
	if !ok {
		t.Fatal("configured index subscription was not retained")
	}
	if !subscription.index || subscription.token != "nse_cm|Nifty 50" {
		t.Fatalf("index subscription = %+v", subscription)
	}
	if err := feed.Unsubscribe([]string{" nse_cm|NIFTY "}); err != nil {
		t.Fatal(err)
	}
	if _, ok := feed.subs["nse_cm|NIFTY"]; ok {
		t.Fatal("trimmed index subscription was not removed")
	}
}
