package web

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestBooksCache(t *testing.T) {
	cache := newBooksCache()
	if cache == nil {
		t.Fatal("expected non-nil cache")
	}

	state, ok := cache.lookup(bookPositions)
	if !ok || state.raw != nil {
		t.Fatalf("expected empty state, got %v, %v", state, ok)
	}

	raw := json.RawMessage(`[{"symbol":"TEST"}]`)
	cache.store(bookPositions, raw, nil)

	state, ok = cache.lookup(bookPositions)
	if !ok || string(state.raw) != string(raw) || state.err != nil {
		t.Fatalf("expected stored raw, got %v", state)
	}

	// Storing error updates err and nextTry
	testErr := errors.New("kotak error")
	cache.store(bookOrders, nil, testErr)
	state, ok = cache.lookup(bookOrders)
	if !ok || !errors.Is(state.err, testErr) || state.nextTry.IsZero() {
		t.Fatalf("expected stored error, got %v", state)
	}

	// Expire clears raw and error
	cache.expire(bookPositions)
	state, _ = cache.lookup(bookPositions)
	if state.raw != nil || state.err != nil {
		t.Fatalf("expected expired state, got %v", state)
	}

	// Reset with new session key clears all books
	cache.store(bookTrades, json.RawMessage(`[]`), nil)
	cache.reset("session-1")
	state, _ = cache.lookup(bookTrades)
	if state.raw != nil {
		t.Fatal("expected cleared state after session key change")
	}

	// Reset with same key does nothing
	cache.store(bookTrades, json.RawMessage(`[]`), nil)
	cache.reset("session-1")
	state, _ = cache.lookup(bookTrades)
	if state.raw == nil {
		t.Fatal("expected state preserved for same key")
	}

	// Clear clears all
	cache.clear()
	if cache.key != "" {
		t.Fatal("expected empty key")
	}

	// Invalidate does not panic
	cache.invalidate()

	// staleAfter checks
	if staleAfter(bookPositions) != 3*positionsInterval {
		t.Errorf("unexpected staleAfter positions: %v", staleAfter(bookPositions))
	}
	if staleAfter(bookTrades) != 3*tradesInterval {
		t.Errorf("unexpected staleAfter trades: %v", staleAfter(bookTrades))
	}
	if staleAfter(bookLimits) != 3*limitsInterval {
		t.Errorf("unexpected staleAfter limits: %v", staleAfter(bookLimits))
	}

	// Unknown book lookup/store
	if _, ok := cache.lookup("unknown"); ok {
		t.Error("expected unknown book not ok")
	}
	cache.store("unknown", nil, nil)
	cache.expire("unknown")
}
