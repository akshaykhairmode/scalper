package store

import (
	"context"
	"testing"
)

func TestSaveAndLoadWatchlist(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.SaveWatchlist(ctx, []string{"nse_fo|A", "nse_fo|B", " nse_fo|A "}); err != nil {
		t.Fatalf("SaveWatchlist: %v", err)
	}
	keys, err := s.LoadWatchlist(ctx)
	if err != nil {
		t.Fatalf("LoadWatchlist: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("watchlist = %v, want 2 deduplicated keys", keys)
	}

	if err := s.SaveWatchlist(ctx, nil); err != nil {
		t.Fatalf("SaveWatchlist(empty): %v", err)
	}
	keys, err = s.LoadWatchlist(ctx)
	if err != nil {
		t.Fatalf("LoadWatchlist(empty): %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("watchlist = %v, want empty", keys)
	}
}
