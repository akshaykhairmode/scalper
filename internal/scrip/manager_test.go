package scrip

import (
	"context"
	"testing"
)

func TestManagerNewErrors(t *testing.T) {
	// A valid config needs both underlyings and segments.
	valid := DefaultManagerConfig()
	if _, err := NewManager(valid); err != nil {
		t.Fatalf("valid config: %v", err)
	}

	// A config with only underlyings (no segments) should now fail.
	if _, err := NewManager(ManagerConfig{Underlyings: testUnderlyings()}); err == nil {
		t.Fatal("underlyings-only should require segments")
	}

	cases := []ManagerConfig{
		{Underlyings: testUnderlyings()},
		{Segments: []string{"bad"}, Underlyings: testUnderlyings()},
		{Segments: []string{SegmentNSEFO}},
	}
	for i, cfg := range cases {
		if _, err := NewManager(cfg); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}

	// Duplicate scrip names must be rejected.
	dup := testUnderlyings()
	dup[1].ScripName = "NIFTY"
	if _, err := NewManager(ManagerConfig{Segments: []string{SegmentNSEFO}, Underlyings: dup}); err == nil {
		t.Fatal("duplicate scrip name should fail")
	}
}

func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		Underlyings: testUnderlyings(),
		Segments:    []string{SegmentNSEFO, SegmentBSEFO, SegmentNSECM, SegmentBSECM},
	}
}

type fakeRepo struct {
	options []Option
	spots   []Spot
}

func (f *fakeRepo) ReplaceScripSnapshot(_ context.Context, options []Option, spots []Spot, _ []SyncRecord) error {
	f.options = options
	f.spots = spots
	return nil
}

func (f *fakeRepo) LoadScripSnapshot(_ context.Context) ([]Option, []Spot, error) {
	return f.options, f.spots, nil
}

func TestManagerLoadAndQueries(t *testing.T) {
	opts := []Option{
		{
			InstrumentKey:   "nse_fo|NIFTY|2026-11-26|24000|CE",
			ExchangeSegment: "nse_fo",
			PSymbol:         "12345",
			PTrdSymbol:      "NIFTY26NOV24000CE",
			Underlying:      "NIFTY",
			Expiry:          "2026-11-26",
			StrikePaise:     2400000,
			OptionType:      Call,
			LotSize:         25,
		},
		{
			InstrumentKey:   "nse_fo|NIFTY|2026-11-26|24000|PE",
			ExchangeSegment: "nse_fo",
			PSymbol:         "12346",
			PTrdSymbol:      "NIFTY26NOV24000PE",
			Underlying:      "NIFTY",
			Expiry:          "2026-11-26",
			StrikePaise:     2400000,
			OptionType:      Put,
			LotSize:         25,
		},
	}
	spots := []Spot{
		{
			InstrumentKey:   "nse_cm|NIFTY",
			ExchangeSegment: "nse_cm",
			Symbol:          "Nifty 50",
			DisplayName:     "NIFTY 50",
			PSymbol:         "26000",
		},
	}

	repo := &fakeRepo{options: opts, spots: spots}
	cfg := DefaultManagerConfig()
	cfg.Repository = repo

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Before loading, queries should return empty/false.
	if st := mgr.Status(); st.Loaded {
		t.Fatalf("Status before load: want not loaded, got %+v", st)
	}
	if _, ok := mgr.Option(opts[0].InstrumentKey); ok {
		t.Fatal("Option should return false before load")
	}

	ctx := context.Background()
	if err := mgr.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	st := mgr.Status()
	if !st.Loaded || st.Source != "database" || st.Options != 2 || st.Spots != 1 {
		t.Fatalf("Status after load: %+v", st)
	}

	// Test Option
	if o, ok := mgr.Option(opts[0].InstrumentKey); !ok || o.PSymbol != "12345" {
		t.Fatalf("Option(%s) = %+v, %v", opts[0].InstrumentKey, o, ok)
	}

	// Test FindOption
	if o, ok := mgr.FindOption("nse_fo", "12345"); !ok || o.InstrumentKey != opts[0].InstrumentKey {
		t.Fatalf("FindOption by PSymbol = %+v, %v", o, ok)
	}
	if o, ok := mgr.FindOption("nse_fo", "NIFTY26NOV24000PE"); !ok || o.InstrumentKey != opts[1].InstrumentKey {
		t.Fatalf("FindOption by PTrdSymbol = %+v, %v", o, ok)
	}

	// Test Options and AllOptions
	byExpiry := mgr.Options("NIFTY", "2026-11-26")
	if len(byExpiry) != 2 {
		t.Fatalf("Options len = %d, want 2", len(byExpiry))
	}
	all := mgr.AllOptions()
	if len(all) != 2 {
		t.Fatalf("AllOptions len = %d, want 2", len(all))
	}

	// Test Expiries and NearestExpiry
	expiries := mgr.Expiries("NIFTY")
	if len(expiries) != 1 || expiries[0].Date != "2026-11-26" {
		t.Fatalf("Expiries = %+v, want 2026-11-26", expiries)
	}
	nearest, ok := mgr.NearestExpiry("NIFTY")
	if !ok || nearest.Date != "2026-11-26" {
		t.Fatalf("NearestExpiry = %+v, %v, want 2026-11-26", nearest, ok)
	}

	// Test Spot, FindSpot, Spots
	if sp, ok := mgr.Spot("nse_cm|NIFTY"); !ok || sp.PSymbol != "26000" {
		t.Fatalf("Spot = %+v, %v", sp, ok)
	}
	if sp, ok := mgr.FindSpot("nse_cm", "Nifty 50"); !ok || sp.InstrumentKey != "nse_cm|NIFTY" {
		t.Fatalf("FindSpot = %+v, %v", sp, ok)
	}
	if loadedSpots := mgr.Spots(); len(loadedSpots) != 1 {
		t.Fatalf("Spots len = %d, want 1", len(loadedSpots))
	}

	// Test Underlyings
	if u := mgr.Underlyings(); len(u) == 0 {
		t.Fatal("Underlyings returned empty")
	}

	// Test Search
	searchResults := mgr.Search(ScripQuery{Symbol: "NIFTY"})
	if len(searchResults) < 2 {
		t.Fatalf("expected at least 2 search results for NIFTY, got %d", len(searchResults))
	}
	ceResults := mgr.Search(ScripQuery{Symbol: "NIFTY", OptionType: "CE"})
	if len(ceResults) != 1 || ceResults[0].OptionType != "CE" {
		t.Fatalf("expected 1 CE result, got %d", len(ceResults))
	}
}
