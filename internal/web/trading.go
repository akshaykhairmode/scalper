package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/paper"
)

const (
	modeLive  = "live"
	modePaper = "paper"
)

// normalizeTradingMode canonicalises live/paper and rejects unknown values.
// An empty mode means the configured default.
func normalizeTradingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", modeLive:
		return modeLive
	case modePaper:
		return modePaper
	default:
		return ""
	}
}

// requestMode resolves the account for an HTTP request. Query parameters are
// used for read/update routes; POST handlers pass the body's mode instead.
func (s *Server) requestMode(r *http.Request) string {
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = s.configSnapshot().Trading.Mode
	}
	return normalizeTradingMode(mode)
}

// requireRequestMode resolves an explicit/configured mode and rejects invalid
// explicit values instead of silently falling back to live.
func (s *Server) requireRequestMode(w http.ResponseWriter, r *http.Request) (string, bool) {
	mode := s.requestMode(r)
	if mode == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "mode must be live or paper", nil)
		return "", false
	}
	return mode, true
}

// engineFor returns the bracket engine for a mode. A nil result means the
// requested account is unavailable.
func (s *Server) engineFor(mode string) *bracket.Engine {
	switch normalizeTradingMode(mode) {
	case modePaper:
		return s.paperEngine
	default:
		return s.bracketEngine
	}
}

// paperAvailable reports whether the paper account is running.
func (s *Server) paperAvailable() bool {
	return s.paperExec != nil && s.paperEngine != nil
}

// onTick fans one market tick out to the live engine, the paper fill simulator
// and the paper engine. The live engine runs first so paper persistence can
// never delay real SL/TP evaluation. Within paper, resting fills run before
// SL/TP so an entry can arm before the same tick is evaluated.
func (s *Server) onTick(ctx context.Context, key string, ltp, bid, ask float64) {
	if key == "" || ltp <= 0 {
		return
	}
	if s.bracketEngine != nil {
		s.bracketEngine.OnTick(ctx, key, ltp)
	}
	if s.paperExec != nil {
		s.paperExec.OnTick(ctx, key, ltp, bid, ask)
	}
	if s.paperEngine != nil {
		s.paperEngine.OnTick(ctx, key, ltp)
	}
}

// ensurePaperSubscribed asks the live feed (when signed in) to stream a paper
// instrument so its resting orders and open position keep receiving ticks.
func (s *Server) ensurePaperSubscribed(instrumentKey string) {
	instrumentKey = strings.TrimSpace(instrumentKey)
	if instrumentKey == "" {
		return
	}
	if s.hsm == nil {
		if !s.configSnapshot().Market.Background.Enabled {
			s.log.Warn("paper instrument has no tick source: HSM unavailable and background collection disabled",
				"event", "paper_tick_source_missing", "instrument", instrumentKey)
		}
		return
	}
	if err := s.hsm.Subscribe([]string{instrumentKey}); err != nil {
		s.log.Debug("failed to subscribe paper instrument", "instrument", instrumentKey, "error", err)
		return
	}
	s.scheduleWatchlistSave()
}

// paperShortNotional returns the per-share notional used for short paper
// margin. The option strike is a stable proxy for the underlying spot and does
// not require a live quote.
func (s *Server) paperShortNotional(instrumentKey string) (float64, bool) {
	if s.market == nil || strings.TrimSpace(instrumentKey) == "" {
		return 0, false
	}
	option, ok := s.market.Option(instrumentKey)
	if !ok {
		return 0, false
	}
	strike := float64(option.StrikePaise) / 100
	if strike <= 0 {
		return 0, false
	}
	return strike, true
}

// paperQuoteSource resolves a price for a paper order when no tick has been
// observed yet: first the in-memory tick cache, then one REST quote.
func (s *Server) paperQuoteSource(ctx context.Context, key string) (paper.Quote, bool) {
	if ltp, ok := s.cachedLTP(key, 10*time.Second); ok {
		return paper.Quote{LTP: ltp, At: s.now()}, true
	}
	if s.market == nil {
		return paper.Quote{}, false
	}
	quote, err := s.market.Quote(ctx, s.marketBaseURL(), key)
	if err != nil || quote.LTP <= 0 {
		return paper.Quote{}, false
	}
	return paper.Quote{LTP: quote.LTP, Bid: quote.Bid, Ask: quote.Ask, At: s.now()}, true
}

// applyPaperRuntimeConfig applies settings that can change without a restart.
func (s *Server) applyPaperRuntimeConfig(cfg config.Config) {
	if s.paperExec != nil {
		s.paperExec.SetSlippage(cfg.Trading.Paper.SlippageTicks)
		s.paperExec.SetShortMarginPct(cfg.Trading.Paper.ShortMarginPct)
	}
	s.applyPaperExchangeStopConfig(cfg)
}

// reconcilePaperBrackets replays restored paper order state into the bracket
// engine. It closes the crash window between a persisted fill and the bracket
// update that should follow it.
func (s *Server) reconcilePaperBrackets(ctx context.Context) {
	if s.paperEngine == nil || s.paperExec == nil {
		return
	}
	active, err := s.paperEngine.List(1000)
	if err != nil {
		s.log.Warn("failed to list paper brackets for reconciliation", "error", err)
		return
	}
	for _, b := range active {
		switch b.Status {
		case bracket.StatusPendingEntry:
			if b.EntryOrderID == "" {
				continue
			}
			o, ok := s.paperExec.Order(b.EntryOrderID)
			if !ok {
				continue
			}
			switch o.Status {
			case paper.StatusComplete:
				s.paperEngine.OnFill(ctx, bracket.Fill{
					OrderID: o.ID, Tag: b.ID, Status: paper.StatusComplete,
					Price: o.FilledPrice, Qty: o.FilledQuantity,
				})
			case paper.StatusCancelled, paper.StatusRejected:
				s.paperEngine.OnFill(ctx, bracket.Fill{OrderID: o.ID, Tag: b.ID, Status: o.Status})
			}
		case bracket.StatusInPosition, bracket.StatusExitFailed:
			if b.ExitOrderID == "" {
				continue
			}
			o, ok := s.paperExec.Order(b.ExitOrderID)
			if !ok {
				continue
			}
			if o.Status == paper.StatusComplete {
				s.paperEngine.OnFill(ctx, bracket.Fill{
					OrderID: o.ID, Tag: "exit_" + b.ID, Status: paper.StatusComplete,
					Price: o.FilledPrice, Qty: o.FilledQuantity,
				})
			}
		}
	}
}

// applyPaperExchangeStopConfig installs paper-specific backstop defaults. The
// server-side SL/TP remains the primary protection; paper backstops default off.
func (s *Server) applyPaperExchangeStopConfig(cfg config.Config) {
	if s.paperEngine == nil {
		return
	}
	mode := bracket.NormalizeExchangeStopMode(cfg.Trading.Paper.ExchangeStop)
	if mode == "" {
		mode = bracket.ExchangeStopOff
	}
	s.paperEngine.SetExchangeStopConfig(bracket.ExchangeStopConfig{
		Mode:               mode,
		DistanceMode:       "multiplier",
		DistanceValue:      2.5,
		SLLOffsetTicks:     2,
		PlaceOnPartialFill: true,
		CancelRetries:      1,
		CancelRetryDelay:   50 * time.Millisecond,
		OnCancelFailure:    "alert",
		ReconcileOnStartup: true,
	})
}
