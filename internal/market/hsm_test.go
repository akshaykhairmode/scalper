package market

import (
	"context"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/scrip"
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

// mutableScripRepository lets a test swap the loaded snapshot in place so a
// scrip refresh (contract roll) can be simulated on the same manager.
type mutableScripRepository struct {
	spots []scrip.Spot
}

func (r *mutableScripRepository) ReplaceScripSnapshot(context.Context, []scrip.Option, []scrip.Spot, []scrip.SyncRecord) error {
	return nil
}

func (r *mutableScripRepository) LoadScripSnapshot(context.Context) ([]scrip.Option, []scrip.Spot, error) {
	return nil, r.spots, nil
}

func TestHSMFeedResyncTokensAfterScripRoll(t *testing.T) {
	future := scrip.Spot{
		InstrumentKey:   scrip.SpotKey("nse_fo", "NIFTY"),
		ExchangeSegment: "nse_fo",
		Symbol:          "NIFTY26NOVFUT",
		DisplayName:     "NIFTY FUT",
		PSymbol:         "48999",
	}
	repo := &mutableScripRepository{spots: []scrip.Spot{future}}
	scripts, err := scrip.NewManager(scrip.ManagerConfig{
		Segments: []string{"nse_fo"},
		Underlyings: []scrip.Underlying{{
			Key: "NIFTY", ScripName: "NIFTY", ExchangeSegment: "nse_fo", SpotSegment: "nse_cm",
		}},
		Repository: repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scripts.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	feed := NewHSMFeed(config.Default(), scripts, nil)
	if err := feed.Subscribe([]string{"nse_fo|NIFTY"}); err != nil {
		t.Fatal(err)
	}
	if sub, ok := feed.subs["nse_fo|NIFTY"]; !ok || sub.token != "nse_fo|48999" {
		t.Fatalf("subscription = %+v, ok=%v", sub, ok)
	}

	// The Scrip Master sync lands with the rolled contract under the same
	// stable key: only the broker token changes.
	rolled := future
	rolled.PSymbol = "53110"
	rolled.Symbol = "NIFTY26DECFUT"
	repo.spots = []scrip.Spot{rolled}
	if err := scripts.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	// No client is connected, so the resync only rewrites the stored token —
	// exactly the part under test.
	feed.ResyncTokens()
	sub, ok := feed.subs["nse_fo|NIFTY"]
	if !ok || sub.token != "nse_fo|53110" {
		t.Fatalf("after resync subscription = %+v, ok=%v", sub, ok)
	}
}

func TestHSMFeedSubscribesIndexFutureFromScrips(t *testing.T) {
	future := scrip.Spot{
		InstrumentKey:   scrip.SpotKey("nse_fo", "NIFTY"),
		ExchangeSegment: "nse_fo",
		Symbol:          "NIFTY26NOVFUT",
		DisplayName:     "NIFTY FUT",
		PSymbol:         "48999",
	}
	scripts, err := scrip.NewManager(scrip.ManagerConfig{
		Segments: []string{"nse_fo"},
		Underlyings: []scrip.Underlying{{
			Key: "NIFTY", ScripName: "NIFTY", ExchangeSegment: "nse_fo", SpotSegment: "nse_cm",
		}},
		Repository: testScripRepository{spots: []scrip.Spot{future}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scripts.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	feed := NewHSMFeed(config.Default(), scripts, nil)
	if err := feed.Subscribe([]string{"nse_fo|NIFTY"}); err != nil {
		t.Fatal(err)
	}
	sub, ok := feed.subs["nse_fo|NIFTY"]
	if !ok {
		t.Fatal("index future subscription was not retained")
	}
	if sub.index || sub.token != "nse_fo|48999" {
		t.Fatalf("future subscription = %+v", sub)
	}

	// A cash-segment spot row must keep resolving through the configured index
	// topic, never through the scrips feed.
	cash := scrip.Spot{
		InstrumentKey:   scrip.SpotKey("nse_cm", "NIFTY"),
		ExchangeSegment: "nse_cm",
		Symbol:          "Nifty 50",
		DisplayName:     "NIFTY",
		PSymbol:         "1333",
	}
	cashScripts, err := scrip.NewManager(scrip.ManagerConfig{
		Segments:    []string{"nse_cm"},
		Underlyings: []scrip.Underlying{{Key: "NIFTY", ScripName: "NIFTY", ExchangeSegment: "nse_fo", SpotSegment: "nse_cm"}},
		Repository:  testScripRepository{spots: []scrip.Spot{cash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cashScripts.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	cashFeed := NewHSMFeed(config.Default(), cashScripts, nil)
	if err := cashFeed.Subscribe([]string{"nse_cm|NIFTY"}); err != nil {
		t.Fatal(err)
	}
	cashSub, ok := cashFeed.subs["nse_cm|NIFTY"]
	if !ok {
		t.Fatal("cash index subscription was not retained")
	}
	if !cashSub.index || cashSub.token != "nse_cm|Nifty 50" {
		t.Fatalf("cash index subscription = %+v", cashSub)
	}
}
