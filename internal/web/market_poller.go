package web

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/hsm"
	"github.com/akshaykhairmode/scalper/internal/market"
)

type backgroundContractSet struct {
	keys   []string
	expiry string
	at     time.Time
}

// initWatchlist restores the last selected instruments from SQLite so the
// background poller and the SFeed start with the same watchlist after a restart.
func (s *Server) initWatchlist() {
	if s == nil || s.watchlistStore == nil {
		return
	}
	if !s.configSnapshot().Market.Background.PersistLastSelection {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keys, err := s.watchlistStore.LoadWatchlist(ctx)
	if err != nil {
		s.log.Warn("failed to load background watchlist", "event", "watchlist_load_failed", "error", err)
		return
	}
	s.setWatchlist(keys)
	if s.hsm != nil && len(keys) > 0 {
		if err := s.hsm.Subscribe(keys); err != nil {
			s.log.Debug("failed to restore HSM watchlist subscriptions", "event", "watchlist_hsm_subscribe_failed", "error", err)
		}
	}
}

func (s *Server) setWatchlist(keys []string) {
	s.watchlistMu.Lock()
	s.watchlist = append([]string(nil), keys...)
	s.watchlistMu.Unlock()
}

func (s *Server) watchlistSnapshot() []string {
	s.watchlistMu.RLock()
	defer s.watchlistMu.RUnlock()
	return append([]string(nil), s.watchlist...)
}

// scheduleWatchlistSave debounces persistence of the current HSM subscription
// set. Browser subscribe/unsubscribe and AI OnTrack both feed this.
func (s *Server) scheduleWatchlistSave() {
	if s == nil || s.watchlistStore == nil {
		return
	}
	if !s.configSnapshot().Market.Background.PersistLastSelection {
		return
	}
	s.watchlistSaveMu.Lock()
	if s.watchlistSaveTimer != nil {
		s.watchlistSaveTimer.Stop()
	}
	s.watchlistSaveTimer = time.AfterFunc(2*time.Second, func() {
		s.watchlistSaveMu.Lock()
		s.watchlistSaveTimer = nil
		s.watchlistSaveMu.Unlock()
		s.saveWatchlistNow()
	})
	s.watchlistSaveMu.Unlock()
}

func (s *Server) saveWatchlistNow() {
	if s == nil || s.watchlistStore == nil || s.hsm == nil {
		return
	}
	keys := s.hsm.SubscribedKeys()
	s.setWatchlist(keys)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.watchlistStore.SaveWatchlist(ctx, keys); err != nil {
		s.log.Warn("failed to save background watchlist", "event", "watchlist_save_failed", "error", err)
	}
}

// runBackgroundPoller keeps candle history and paper tracking alive with
// TOTP-free REST quotes while the SFeed is unavailable.
func (s *Server) runBackgroundPoller(ctx context.Context) {
	// Give the Scrip Master and startup work time to settle.
	if err := sleepCtx(ctx, 5*time.Second); err != nil {
		return
	}
	for {
		cfg := s.configSnapshot()
		bg := cfg.Market.Background
		if !bg.Enabled {
			if err := sleepCtx(ctx, 5*time.Second); err != nil {
				return
			}
			continue
		}
		interval := time.Duration(bg.IntervalSeconds) * time.Second
		if interval < time.Second {
			interval = time.Second
		}
		if s.hsm != nil && s.hsm.IsConnected() {
			// The SFeed is live and more accurate; do not double-poll.
			s.bgLastStatus.Store(false)
			if err := sleepCtx(ctx, interval); err != nil {
				return
			}
			continue
		}
		s.pollBackgroundOnce(ctx, bg)
		if err := sleepCtx(ctx, interval); err != nil {
			return
		}
	}
}

func (s *Server) pollBackgroundOnce(ctx context.Context, bg config.BackgroundCollection) {
	if s.market == nil {
		return
	}
	keys := s.backgroundKeys(ctx, bg)
	opened := keys[:0]
	for _, key := range keys {
		if s.backgroundKeyMarketOpen(key) {
			opened = append(opened, key)
		}
	}
	keys = opened
	if len(keys) == 0 {
		return
	}
	optionKeys, spotKeys := s.splitBackgroundKeys(keys)
	now := time.Now()
	var payloads []*tickPayload

	if len(optionKeys) > 0 {
		quotes, err := s.market.Quotes(ctx, s.marketBaseURL(), optionKeys)
		if err != nil {
			s.log.Debug("background option quote poll failed", "event", "background_poll_quotes_failed", "error", err)
		} else {
			for key, quote := range quotes {
				if payload := s.applyPolledQuote(key, quote, now); payload != nil {
					payloads = append(payloads, payload)
				}
			}
		}
	}
	if len(spotKeys) > 0 {
		indices, err := s.market.Indices(ctx, s.marketBaseURL())
		if err != nil {
			s.log.Debug("background index quote poll failed", "event", "background_poll_indices_failed", "error", err)
		} else {
			for _, idx := range indices {
				if !spotKeys[idx.InstrumentKey] || idx.LTP <= 0 {
					continue
				}
				quote := market.Quote{
					LTP:           idx.LTP,
					PreviousClose: idx.PreviousClose,
					Change:        idx.Change,
					ChangePct:     idx.ChangePct,
					Volume:        idx.Volume,
					UpdatedAt:     idx.UpdatedAt,
				}
				if payload := s.applyPolledQuote(idx.InstrumentKey, quote, now); payload != nil {
					payloads = append(payloads, payload)
				}
			}
		}
	}
	s.broadcastPolledTicks(payloads)
}

func (s *Server) applyPolledQuote(key string, quote market.Quote, fallback time.Time) *tickPayload {
	if strings.TrimSpace(key) == "" || quote.LTP <= 0 {
		return nil
	}
	ts := fallback
	if quote.UpdatedAt > 0 {
		if quote.UpdatedAt > 1e12 {
			ts = time.UnixMilli(quote.UpdatedAt)
		} else {
			ts = time.Unix(quote.UpdatedAt, 0)
		}
	}
	s.setLiveQuoteFromMarket(key, quote, ts)
	s.onTick(s.feedCtx, key, quote.LTP, quote.Bid, quote.Ask)
	if s.candleAgg != nil && s.candleSessionAllows(key, ts) {
		s.candleAgg.OnTick(key, quote.LTP, quote.Volume, quote.OI, ts)
	}
	if s.evaluator != nil {
		s.evaluator.OnTick(s.feedCtx, key, quote.LTP)
	}

	payload := &tickPayload{
		Key:       key,
		LTP:       quote.LTP,
		PrevClose: quote.PreviousClose,
		Change:    quote.Change,
		ChangePct: quote.ChangePct,
		Volume:    quote.Volume,
		Bid:       quote.Bid,
		Ask:       quote.Ask,
		Timestamp: quote.UpdatedAt,
	}
	if payload.Timestamp <= 0 {
		payload.Timestamp = ts.Unix()
	}
	if quote.OI >= 0 && quote.OI <= 100_000_000 {
		oi := quote.OI
		payload.OI = &oi
	}
	if len(quote.Bids) > 0 || len(quote.Asks) > 0 {
		payload.Bids = toHSMDepth(quote.Bids)
		payload.Asks = toHSMDepth(quote.Asks)
	}
	return payload
}

// ingestChainQuotes turns option-chain snapshots into candle ticks. Kotak has
// no historical endpoint for MCX, so illiquid commodity options otherwise show
// an empty chart until they trade. Feeding quote snapshots keeps their bars
// building even when the last price is unchanged.
func (s *Server) ingestChainQuotes(rows []market.ChainRow) {
	if s.candleAgg == nil || len(rows) == 0 {
		return
	}
	now := time.Now()
	for _, row := range rows {
		for _, quote := range []*market.OptionQuote{row.Call, row.Put} {
			if quote == nil || quote.LTP <= 0 {
				continue
			}
			if !strings.HasPrefix(quote.InstrumentKey, "mcx_fo|") {
				continue
			}
			if !s.candleSessionAllows(quote.InstrumentKey, now) {
				continue
			}
			s.setLiveQuoteFromMarket(quote.InstrumentKey, market.Quote{
				LTP:           quote.LTP,
				PreviousClose: quote.PreviousClose,
				Change:        quote.Change,
				ChangePct:     quote.ChangePct,
				Volume:        quote.Volume,
				OI:            quote.OI,
				Bid:           quote.Bid,
				Ask:           quote.Ask,
				Bids:          quote.Bids,
				Asks:          quote.Asks,
			}, now)
			s.onTick(s.feedCtx, quote.InstrumentKey, quote.LTP, quote.Bid, quote.Ask)
			s.candleAgg.OnTick(quote.InstrumentKey, quote.LTP, quote.Volume, quote.OI, now)
		}
	}
}

func (s *Server) broadcastPolledTicks(payloads []*tickPayload) {
	if len(payloads) == 0 {
		return
	}
	if !s.bgLastStatus.Swap(true) {
		s.broadcast(map[string]any{"type": "feed_status", "data": map[string]string{"status": "polling"}})
	}
	if len(payloads) == 1 {
		s.broadcast(tickMessage{Type: "tick", Data: payloads[0]})
		return
	}
	s.broadcast(tickMessage{Type: "ticks", Data: payloads})
}

func (s *Server) setLiveQuoteFromMarket(key string, quote market.Quote, at time.Time) {
	if s == nil || key == "" || quote.LTP <= 0 {
		return
	}
	s.liveMu.Lock()
	if s.liveQuotes == nil {
		s.liveQuotes = make(map[string]liveQuote)
	}
	lq, ok := s.liveQuotes[key]
	if !ok && len(s.liveQuotes) >= 2048 {
		for existing := range s.liveQuotes {
			delete(s.liveQuotes, existing)
			break
		}
	}
	lq.LTP = quote.LTP
	lq.Bid = quote.Bid
	lq.Ask = quote.Ask
	lq.At = at
	if len(quote.Bids) > 0 || len(quote.Asks) > 0 {
		lq.Bids = toHSMDepth(quote.Bids)
		lq.Asks = toHSMDepth(quote.Asks)
		lq.DepthAt = at
	}
	s.liveQuotes[key] = lq
	s.liveMu.Unlock()
}

func toHSMDepth(levels []market.DepthLevel) []hsm.DepthLevel {
	if len(levels) == 0 {
		return nil
	}
	out := make([]hsm.DepthLevel, len(levels))
	for i, level := range levels {
		out[i] = hsm.DepthLevel{Price: level.Price, Quantity: level.Quantity, Orders: level.Orders}
	}
	return out
}

func (s *Server) backgroundKeys(ctx context.Context, bg config.BackgroundCollection) []string {
	set := make(map[string]bool)
	for _, key := range s.watchlistSnapshot() {
		set[key] = true
	}
	if s.hsm != nil {
		for _, key := range s.hsm.SubscribedKeys() {
			set[key] = true
		}
	}
	if bg.IncludeOpenPositions && s.evaluator != nil {
		for key := range s.evaluator.TrackedInstrumentKeys() {
			set[key] = true
		}
	}
	if s.paperExec != nil {
		for _, key := range s.paperExec.OpenInstrumentKeys() {
			set[key] = true
		}
	}
	for _, key := range s.resolvedBackgroundContracts(ctx, bg) {
		set[key] = true
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// splitBackgroundKeys separates option contracts from index/spot/futures keys.
func (s *Server) splitBackgroundKeys(keys []string) (options []string, spots map[string]bool) {
	spots = make(map[string]bool)
	for _, key := range keys {
		if s.market != nil {
			if _, ok := s.market.Option(key); ok {
				options = append(options, key)
				continue
			}
		}
		spots[key] = true
	}
	return options, spots
}

// resolvedBackgroundContracts resolves ATM +/- N contracts for the configured
// underlyings. Results are cached for five minutes to keep REST usage low.
func (s *Server) resolvedBackgroundContracts(ctx context.Context, bg config.BackgroundCollection) []string {
	if s.market == nil {
		return nil
	}
	underlyings := make([]string, 0, len(bg.Underlyings))
	for _, underlying := range bg.Underlyings {
		if trimmed := strings.ToUpper(strings.TrimSpace(underlying)); trimmed != "" {
			underlyings = append(underlyings, trimmed)
		}
	}
	if len(underlyings) == 0 {
		for _, underlying := range s.configSnapshot().AI.ScannedUnderlyings {
			if trimmed := strings.ToUpper(strings.TrimSpace(underlying)); trimmed != "" {
				underlyings = append(underlyings, trimmed)
			}
		}
	}
	if len(underlyings) == 0 {
		return nil
	}

	strikeWindow := bg.StrikeWindow
	if strikeWindow < 0 {
		strikeWindow = 0
	}
	if strikeWindow > 5 {
		strikeWindow = 5
	}
	count := 2*strikeWindow + 1

	s.bgContractsMu.Lock()
	if s.bgContracts == nil {
		s.bgContracts = make(map[string]backgroundContractSet)
	}
	s.bgContractsMu.Unlock()

	var out []string
	now := time.Now()
	for _, underlying := range underlyings {
		s.bgContractsMu.Lock()
		cached, ok := s.bgContracts[underlying]
		s.bgContractsMu.Unlock()
		if !s.IsUnderlyingMarketOpen(underlying) {
			out = append(out, cached.keys...)
			continue
		}
		if ok && now.Sub(cached.at) < 5*time.Minute && len(cached.keys) > 0 {
			out = append(out, cached.keys...)
			continue
		}

		expiries, err := s.market.Expiries(underlying)
		if err != nil || len(expiries) == 0 {
			out = append(out, cached.keys...)
			continue
		}
		expiry := firstLiveExpiry(expiries)
		spot, _ := s.GetSpotPrice(ctx, underlying)

		var rows []market.ChainRow
		if spot > 0 {
			if cw, ok := s.market.(chainWindowAPI); ok {
				if page, werr := cw.ChainWindow(ctx, s.marketBaseURL(), underlying, expiry, spot, count, "around"); werr == nil {
					rows = page.Rows
				}
			}
		}
		if len(rows) == 0 {
			rows, _ = s.market.Chain(ctx, s.marketBaseURL(), underlying, expiry)
			if len(rows) > count {
				if spot > 0 {
					sort.Slice(rows, func(i, j int) bool {
						return absFloat(rows[i].Strike-spot) < absFloat(rows[j].Strike-spot)
					})
				} else {
					mid := len(rows) / 2
					start := mid - count/2
					if start < 0 {
						start = 0
					}
					end := start + count
					if end > len(rows) {
						end = len(rows)
					}
					rows = rows[start:end]
					count = len(rows)
				}
				if len(rows) > count {
					rows = rows[:count]
				}
			}
		}
		keys := make([]string, 0, len(rows)*2)
		for _, row := range rows {
			if row.Call != nil && row.Call.InstrumentKey != "" {
				keys = append(keys, row.Call.InstrumentKey)
			}
			if row.Put != nil && row.Put.InstrumentKey != "" {
				keys = append(keys, row.Put.InstrumentKey)
			}
		}
		s.bgContractsMu.Lock()
		s.bgContracts[underlying] = backgroundContractSet{keys: keys, expiry: expiry, at: now}
		s.bgContractsMu.Unlock()
		out = append(out, keys...)
	}
	return out
}

// backgroundKeyMarketOpen decides whether an instrument's own exchange is
// currently open, so MCX polling never feeds stale NSE/BSE close prices.
func (s *Server) backgroundKeyMarketOpen(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if strings.HasPrefix(upper, "MCX_FO|") {
		parts := strings.Split(key, "|")
		underlying := "CRUDEOIL"
		if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
			underlying = parts[1]
		}
		return s.IsUnderlyingMarketOpen(underlying)
	}
	if strings.HasPrefix(upper, "NSE_") || strings.HasPrefix(upper, "BSE_") {
		return s.IsUnderlyingMarketOpen("NIFTY")
	}
	return true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
