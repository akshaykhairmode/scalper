package chain

import (
	"testing"
	"time"
)

func TestOptionTypeAndExpiryResolver(t *testing.T) {
	for input, want := range map[string]OptionType{"CE": Call, "PE": Put} {
		got, err := OptionTypeFromString(input)
		if err != nil || got != want {
			t.Fatalf("OptionTypeFromString(%q) = %q, %v", input, got, err)
		}
	}
	if _, err := OptionTypeFromString("call"); err == nil {
		t.Fatal("invalid option type should fail")
	}

	resolver := NewExpiryResolver()
	first, err := resolver.ResolveExpiry("2026-10-29")
	if err != nil || first.Format("2006-01-02") != "2026-10-29" {
		t.Fatalf("ResolveExpiry date = %v, %v", first, err)
	}
	second, err := resolver.ResolveExpiry("2026-10-29")
	if err != nil || !second.Equal(first) {
		t.Fatalf("cached ResolveExpiry date = %v, %v", second, err)
	}
	if err := resolver.AddExpiry("2026-11-05"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveExpiry("not-a-date"); err == nil {
		t.Fatal("invalid expiry should fail")
	}
}

func TestExpiryAndStrikeHelpers(t *testing.T) {
	if !IsWeeklyExpiry(time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)) || IsWeeklyExpiry(time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("weekly expiry weekday check is incorrect")
	}
	if got := ExpiryMonthlies(2026, time.October); len(got) != 1 || got[0].Format("2006-01-02") != "2026-10-29" {
		t.Fatalf("October monthly expiries = %v", got)
	}
	if got := ExpiryMonthlies(2026, time.March); len(got) != 1 || got[0].Format("2006-01-02") != "2026-04-02" {
		t.Fatalf("March fallback monthly expiry = %v", got)
	}
	from, to := StrikeRange(100, 500, 3)
	if from != 9850 || to != 10150 {
		t.Fatalf("StrikeRange = (%d, %d)", from, to)
	}
	from, _ = StrikeRange(1, 500, 3)
	if from != 0 {
		t.Fatalf("negative lower strike was not clamped: %d", from)
	}
	if !ATMFlag(2_250_000, 22500, 0) || ATMFlag(2_251_000, 22500, 50) {
		t.Fatal("ATM flag tolerance is incorrect")
	}
}

func TestChainBuilderBuildsSortedLadderAndLooksUpData(t *testing.T) {
	expiry := Expiry(time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC))
	builder := NewChainBuilder()
	if ladder := builder.BuildLadder(UnderlyingNIFTY, expiry, &Spot{LTP: 22500}); ladder != nil {
		t.Fatal("missing chain should return nil ladder")
	}
	spot := &Spot{Symbol: "NIFTY", LTP: 22500}
	builder.SetSpot(UnderlyingNIFTY, spot)
	if got, ok := builder.GetSpot(UnderlyingNIFTY); !ok || got != spot {
		t.Fatalf("GetSpot = %v, %v", got, ok)
	}
	options := []ChainOption{
		{Underlying: UnderlyingNIFTY, Expiry: expiry, OptionType: Put, Strike: 2_250_000, LTP: 101, PreviousClose: 100, OI: 12},
		{Underlying: UnderlyingNIFTY, Expiry: expiry, OptionType: Call, Strike: 2_250_000, LTP: 110, PreviousClose: 100, OI: 20},
		{Underlying: UnderlyingNIFTY, Expiry: expiry, OptionType: Call, Strike: 2_249_000, LTP: 120},
	}
	for _, option := range options {
		if err := builder.AddOption(option); err != nil {
			t.Fatal(err)
		}
	}
	chain, ok := builder.GetChain(UnderlyingNIFTY, expiry)
	if !ok || len(chain.BuildIndices().CallIndex) != 2 || len(chain.BuildIndices().PutIndex) != 1 {
		t.Fatalf("GetChain/BuildIndices = %+v, %v", chain, ok)
	}
	chain.SortStrikes()
	if chain.Options[0].Strike != 2_249_000 {
		t.Fatalf("SortStrikes first strike = %d", chain.Options[0].Strike)
	}
	ladder := builder.BuildLadder(UnderlyingNIFTY, expiry, spot)
	if ladder == nil || len(ladder.Entries) != 2 || ladder.Entries[0].Strike != 2_249_000 || ladder.ATMStrike != 2_250_000 {
		t.Fatalf("ladder = %+v", ladder)
	}
	if !ladder.Entries[1].IsATM || ladder.Entries[1].LTP != 110 || ladder.Entries[1].Change != 10 || ladder.Entries[1].OI != 20 {
		t.Fatalf("ATM call ladder entry = %+v", ladder.Entries[1])
	}
	if len(builder.Underlyings()) != 1 || builder.Underlyings()[0].Key != UnderlyingNIFTY {
		t.Fatalf("Underlyings = %+v", builder.Underlyings())
	}
}

func TestBuildLadderPutFallbackAndNoSpot(t *testing.T) {
	expiry := Expiry(time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC))
	chain := NewChain(UnderlyingBANKNIFTY, expiry)
	chain.Spots = []Spot{{Symbol: "BANKNIFTY"}}
	chain.AddOption(ChainOption{OptionType: Put, Strike: 5_000_000, LTP: 50, PreviousClose: 0, OI: 8})
	ladder := buildLadder(chain, &Spot{LTP: 0, Close: 50000})
	if len(ladder.Entries) != 1 || ladder.Entries[0].OptionType != Put || ladder.Entries[0].Change != 0 || ladder.Entries[0].IsATM {
		t.Fatalf("put ladder entry = %+v", ladder.Entries)
	}
	if ladder.ATMStrike != 5_000_000 || len(ladder.Spots) != 1 {
		t.Fatalf("ladder metadata = %+v", ladder)
	}
}
