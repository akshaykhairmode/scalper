package web

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

// Book names used by the cache and handlers.
const (
	bookPositions = "positions"
	bookOrders    = "orders"
	bookTrades    = "trades"
	bookLimits    = "limits"
)

// Refresh cadence for the server-side account book. Every dashboard tab reads
// this cache, so Kotak sees a fixed number of calls regardless of how many
// tabs are open or how many order-feed events arrive.
const (
	positionsInterval = 5 * time.Second
	ordersInterval    = 5 * time.Second
	tradesInterval    = 15 * time.Second
	limitsInterval    = 120 * time.Second
	forceMinGap       = 2 * time.Second
	bookRetryBackoff  = 1 * time.Second
)

type bookState struct {
	raw     json.RawMessage
	err     error
	fetched time.Time
	nextTry time.Time
}

// booksCache stores the last successful Kotak report payloads. Stale data is
// served on failure: an expired book is better than a blank dashboard.
type booksCache struct {
	mu      sync.RWMutex
	key     string
	books   map[string]*bookState
	fetchMu map[string]*sync.Mutex
	force   chan struct{}
}

func newBooksCache() *booksCache {
	return &booksCache{
		books: map[string]*bookState{
			bookPositions: {},
			bookOrders:    {},
			bookTrades:    {},
			bookLimits:    {},
		},
		fetchMu: map[string]*sync.Mutex{
			bookPositions: {},
			bookOrders:    {},
			bookTrades:    {},
			bookLimits:    {},
		},
		force: make(chan struct{}, 1),
	}
}

// invalidate asks the refresher to re-read the mutable books (positions and
// orders) as soon as its minimum gap allows.
func (c *booksCache) invalidate() {
	if c == nil {
		return
	}
	select {
	case c.force <- struct{}{}:
	default:
	}
}

// expire drops the cached payload for the given books so the next read fetches
// fresh data. Concurrent readers still share one upstream call per book.
func (c *booksCache) expire(kinds ...string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, kind := range kinds {
		if state, ok := c.books[kind]; ok {
			*state = bookState{}
		}
	}
	c.invalidate()
}

func (c *booksCache) reset(sessionKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key == sessionKey {
		return
	}
	c.key = sessionKey
	for _, state := range c.books {
		*state = bookState{}
	}
}

func (c *booksCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.key = ""
	for _, state := range c.books {
		*state = bookState{}
	}
}

func (c *booksCache) lookup(kind string) (bookState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	state, ok := c.books[kind]
	if !ok {
		return bookState{}, false
	}
	return *state, true
}

func (c *booksCache) store(kind string, raw json.RawMessage, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.books[kind]
	if !ok {
		return
	}
	if err != nil {
		state.err = err
		state.nextTry = time.Now().Add(bookRetryBackoff)
		return
	}
	state.raw = raw
	state.err = nil
	state.fetched = time.Now()
	state.nextTry = time.Time{}
}

func (c *booksCache) lock(kind string) *sync.Mutex {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fetchMu[kind]
}

func sessionKey(s kotakapi.Session) string {
	return s.SID + "|" + s.Token + "|" + s.BaseURL
}

// brokerSessionValue mirrors brokerSession without writing an HTTP response.
func (s *Server) brokerSessionValue() (kotakapi.Session, bool) {
	if s.kotak == nil {
		return kotakapi.Session{}, false
	}
	a := s.a.CurrentSession()
	if a == nil || a.BrokerToken == "" || a.BrokerSID == "" {
		return kotakapi.Session{}, false
	}
	return kotakapi.Session{
		BaseURL:     a.BaseURL,
		SID:         a.BrokerSID,
		Token:       a.BrokerToken,
		ConsumerKey: s.consumerKey,
	}, true
}

func (s *Server) feedClientCount() int {
	s.feedMu.RLock()
	defer s.feedMu.RUnlock()
	return len(s.feedClients)
}

// fetchBook performs exactly one Kotak call for the requested report.
func (s *Server) fetchBook(ctx context.Context, kind string, session kotakapi.Session) (json.RawMessage, error) {
	switch kind {
	case bookPositions:
		raw, err := s.kotak.Positions(ctx, session)
		if err != nil {
			return nil, err
		}
		return s.enrichBrokerPositions(ctx, raw), nil
	case bookOrders:
		return s.kotak.Orders(ctx, session)
	case bookTrades:
		return s.kotak.Trades(ctx, session)
	case bookLimits:
		return s.kotak.Limits(ctx, session)
	default:
		return nil, nil
	}
}

// staleAfter is how long a cached book may be served without a refresh
// attempt. It is deliberately longer than the background cadence so normal
// polling always hits fresh data, while a failed websocket still lets the
// getter refresh synchronously.
func staleAfter(kind string) time.Duration {
	switch kind {
	case bookPositions, bookOrders:
		return 3 * positionsInterval
	case bookTrades:
		return 3 * tradesInterval
	default:
		return 3 * limitsInterval
	}
}

// getBook returns the cached payload, fetching synchronously only when the
// cache is empty or stale so a cold tab still gets data immediately.
func (s *Server) getBook(ctx context.Context, kind string, session kotakapi.Session) (json.RawMessage, error) {
	cache := s.books
	if cache == nil {
		return s.fetchBook(ctx, kind, session)
	}
	cache.reset(sessionKey(session))
	if state, ok := cache.lookup(kind); ok {
		if state.raw != nil {
			if time.Since(state.fetched) < staleAfter(kind) || time.Now().Before(state.nextTry) {
				return state.raw, nil
			}
		} else if state.err != nil && time.Now().Before(state.nextTry) {
			return nil, state.err
		}
	}

	mu := cache.lock(kind)
	mu.Lock()
	defer mu.Unlock()
	cached, _ := cache.lookup(kind)
	if cached.raw != nil {
		if time.Since(cached.fetched) < staleAfter(kind) || time.Now().Before(cached.nextTry) {
			return cached.raw, nil
		}
	} else if cached.err != nil && time.Now().Before(cached.nextTry) {
		return nil, cached.err
	}
	raw, err := s.fetchBook(ctx, kind, session)
	if err != nil && cached.raw != nil {
		// Keep serving the last good book; the cache records the failure so
		// the next request backs off instead of hammering Kotak.
		cache.store(kind, nil, err)
		return cached.raw, nil
	}
	cache.store(kind, raw, err)
	return raw, err
}

// runAccountBooks owns the periodic refresh of positions, orders, trades and
// limits. Order-feed events only mark the mutable books dirty; they never
// trigger a burst of calls.
func (s *Server) runAccountBooks(ctx context.Context) {
	if s.books == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	last := map[string]time.Time{}
	var lastForced time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.books.force:
			if s.feedClientCount() > 0 && time.Since(lastForced) >= forceMinGap {
				last[bookPositions] = time.Time{}
				last[bookOrders] = time.Time{}
				last[bookLimits] = time.Time{}
				lastForced = time.Now()
			}
		}

		session, ok := s.brokerSessionValue()
		if !ok {
			s.books.clear()
			last = map[string]time.Time{}
			continue
		}
		s.books.reset(sessionKey(session))
		// With no dashboard connected there is nobody to serve, so stay idle.
		if s.feedClientCount() == 0 {
			continue
		}

		now := time.Now()
		for kind, every := range map[string]time.Duration{
			bookPositions: positionsInterval,
			bookOrders:    ordersInterval,
			bookTrades:    tradesInterval,
			bookLimits:    limitsInterval,
		} {
			if now.Sub(last[kind]) < every {
				continue
			}
			mu := s.books.lock(kind)
			mu.Lock()
			if state, ok := s.books.lookup(kind); ok && state.fetched.After(last[kind]) {
				// A handler refreshed this book while we waited.
				mu.Unlock()
				continue
			}
			last[kind] = now
			raw, err := s.fetchBook(ctx, kind, session)
			s.books.store(kind, raw, err)
			mu.Unlock()
			if err != nil && s.log != nil {
				s.log.Warn("Kotak book refresh failed", "event", "broker_book_refresh_failed",
					"book", kind, "error", err)
			}
		}
	}
}
