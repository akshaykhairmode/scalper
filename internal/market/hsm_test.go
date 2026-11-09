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

func TestHSMFeedBatchPreservesValidSubscriptionsWhenOneIsUnavailable(t *testing.T) {
	feed := NewHSMFeed(config.Default(), nil, nil)
	// Batch contains valid NIFTY spot and an invalid commodity/unloaded key
	if err := feed.Subscribe([]string{"nse_cm|NIFTY", "mcx_fo|UNKNOWN_COMMODITY"}); err != nil {
		t.Fatalf("expected batch subscribe with valid key to succeed, got: %v", err)
	}
	if _, ok := feed.subs["nse_cm|NIFTY"]; !ok {
		t.Fatal("valid subscription was dropped due to invalid sibling key")
	}
	if _, ok := feed.subs["mcx_fo|UNKNOWN_COMMODITY"]; ok {
		t.Fatal("invalid key should not be retained in subs")
	}

	// But subscribing ONLY unavailable keys should report an error
	if err := feed.Subscribe([]string{"completely_bogus_key"}); err == nil {
		t.Fatal("expected error when subscribing solely invalid keys")
	}
}

func TestHSMFeedSubscribesIndiaVIX(t *testing.T) {
	cfg := config.Default()
	cfg.Market.IndiaVIX.Enabled = true
	feed := NewHSMFeed(cfg, nil, nil)
	if err := feed.Subscribe([]string{"nse_cm|INDIAVIX"}); err != nil {
		t.Fatal(err)
	}
	sub, ok := feed.subs["nse_cm|INDIAVIX"]
	if !ok {
		t.Fatal("expected India VIX subscription to be retained")
	}
	if !sub.index || sub.token != "nse_cm|INDIA VIX" {
		t.Fatalf("unexpected India VIX subscription: %+v", sub)
	}
}
