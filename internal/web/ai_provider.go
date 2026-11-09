package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/akshaykhairmode/scalper/internal/ai/features"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

// GetSpotPrice retrieves the latest spot price of an underlying index (e.g. NIFTY).
func (s *Server) GetSpotPrice(ctx context.Context, underlying string) (float64, error) {
	// 1. Try cached indices first if fresh
	s.indicesMu.Lock()
	if s.indicesRaw != nil {
		var cached struct {
			Indices []market.IndexQuote `json:"indices"`
		}
		if err := json.Unmarshal(s.indicesRaw, &cached); err == nil {
			for _, idx := range cached.Indices {
				if strings.EqualFold(idx.Key, underlying) || strings.EqualFold(idx.DisplayName, underlying) {
					if idx.LTP > 0 {
						s.indicesMu.Unlock()
						return idx.LTP, nil
					}
				}
			}
		}
	}
	s.indicesMu.Unlock()

	if s.market == nil {
		return 0, errors.New("market API not configured")
	}

	// 2. Fetch live indices via market service using valid marketBaseURL
	indices, err := s.market.Indices(ctx, s.marketBaseURL())
	if err == nil {
		for _, idx := range indices {
			if strings.EqualFold(idx.Key, underlying) || strings.EqualFold(idx.DisplayName, underlying) {
				if idx.LTP > 0 {
					return idx.LTP, nil
				}
			}
		}
	}

	// 3. Fallback: if spot quote is unavailable (e.g. off-market or sandbox), derive from nearest option chain strike
	if expiries, eerr := s.market.Expiries(underlying); eerr == nil && len(expiries) > 0 {
		expiry := firstLiveExpiry(expiries)
		if chainRows, cerr := s.market.Chain(ctx, s.marketBaseURL(), underlying, expiry); cerr == nil && len(chainRows) > 0 {
			mid := chainRows[len(chainRows)/2].Strike
			if mid > 0 {
				return mid, nil
			}
		}
	}

	return 0, fmt.Errorf("spot price for underlying %q not available", underlying)
}

// GetATMStrikes locates the closest liquid Call and Put strikes relative to spot.
func (s *Server) GetATMStrikes(ctx context.Context, underlying string) (ceKey, peKey string, ceLTP, peLTP float64, err error) {
	if s.market == nil {
		return "", "", 0, 0, errors.New("market API not configured")
	}

	expiries, err := s.market.Expiries(underlying)
	if err != nil || len(expiries) == 0 {
		return "", "", 0, 0, fmt.Errorf("no expiries available for %q: %w", underlying, err)
	}

	nearestExpiry := firstLiveExpiry(expiries)
	chainRows, err := s.market.Chain(ctx, s.marketBaseURL(), underlying, nearestExpiry)
	if err != nil || len(chainRows) == 0 {
		return "", "", 0, 0, fmt.Errorf("option chain empty for %q %s: %w", underlying, nearestExpiry, err)
	}

	spot, _ := s.GetSpotPrice(ctx, underlying)
	if spot <= 0 {
		spot = chainRows[len(chainRows)/2].Strike
	}

	var bestRow market.ChainRow
	minDiff := math.MaxFloat64
	for _, r := range chainRows {
		diff := math.Abs(r.Strike - spot)
		if diff < minDiff {
			minDiff = diff
			bestRow = r
		}
	}

	if bestRow.Call != nil {
		ceKey = bestRow.Call.InstrumentKey
		ceLTP = bestRow.Call.LTP
	}
	if bestRow.Put != nil {
		peKey = bestRow.Put.InstrumentKey
		peLTP = bestRow.Put.LTP
	}

	if ceKey == "" && peKey == "" {
		return "", "", 0, 0, fmt.Errorf("no ATM strikes found near spot %f", spot)
	}

	return ceKey, peKey, ceLTP, peLTP, nil
}

// GetMarketDepth extracts L2 bid/ask microstructure metrics for an instrument.
func (s *Server) GetMarketDepth(ctx context.Context, instrumentKey string) (features.OrderBookMetrics, error) {
	if s.market == nil {
		return features.OrderBookMetrics{}, errors.New("market API not configured")
	}

	q, err := s.market.Quote(ctx, s.marketBaseURL(), instrumentKey)
	if err != nil {
		return features.OrderBookMetrics{}, err
	}

	return features.CalculateOrderBookMetrics(q.LTP, q.Bids, q.Asks), nil
}

// GetMarketSnapshot returns order-book microstructure together with contract
// metadata for an option instrument. Metadata comes from the scrip master, so
// it remains available even when the live quote request fails.
func (s *Server) GetMarketSnapshot(ctx context.Context, instrumentKey string) (features.OrderBookMetrics, features.OptionInfo, error) {
	if s.market == nil {
		return features.OrderBookMetrics{}, features.OptionInfo{}, errors.New("market API not configured")
	}

	var info features.OptionInfo
	info.InstrumentKey = instrumentKey
	if opt, found := s.market.Option(instrumentKey); found {
		info.Underlying = opt.Underlying
		info.Expiry = opt.Expiry
		info.Strike = float64(opt.StrikePaise) / 100
		info.OptionType = string(opt.OptionType)
		info.LotSize = opt.LotSize
	}

	q, err := s.market.Quote(ctx, s.marketBaseURL(), instrumentKey)
	if err != nil {
		// Metadata alone is still useful to the agent; surface the quote error
		// to the caller so it can decide whether to proceed.
		return features.OrderBookMetrics{}, info, err
	}

	info.LTP = q.LTP
	info.PreviousClose = q.PreviousClose
	info.Volume = q.Volume
	info.OI = q.OI
	info.Bid = q.Bid
	info.Ask = q.Ask

	return features.CalculateOrderBookMetrics(q.LTP, q.Bids, q.Asks), info, nil
}

// GetOptionLTP returns the latest traded price for an option instrument. It is
// used as a REST fallback when the live feed stops delivering ticks for a
// tracked paper position.
func (s *Server) GetOptionLTP(ctx context.Context, instrumentKey string) (float64, error) {
	if s.market == nil {
		return 0, errors.New("market API not configured")
	}

	q, err := s.market.Quote(ctx, s.marketBaseURL(), instrumentKey)
	if err != nil {
		return 0, err
	}
	if q.LTP <= 0 {
		return 0, fmt.Errorf("no LTP available for %q", instrumentKey)
	}
	return q.LTP, nil
}

// firstLiveExpiry returns the nearest non-expired expiry date. If every
// listed expiry is marked expired (stale scrip master), it falls back to the
// earliest available date so callers still get a usable value.
func firstLiveExpiry(expiries []scrip.ExpiryInfo) string {
	for _, e := range expiries {
		if !e.Expired {
			return e.Date
		}
	}
	if len(expiries) > 0 {
		return expiries[0].Date
	}
	return ""
}

// GetConfiguredUnderlyings returns the active underlyings to be evaluated by the AI scanner.
// Prioritizes cfg.AI.ScannedUnderlyings; if unconfigured or empty, defaults to ["NIFTY", "SENSEX"].
func (s *Server) GetConfiguredUnderlyings() []string {
	s.configMu.RLock()
	scanned := s.cfg.AI.ScannedUnderlyings
	s.configMu.RUnlock()

	var res []string
	for _, u := range scanned {
		trimmed := strings.ToUpper(strings.TrimSpace(u))
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	if len(res) > 0 {
		return res
	}
	return []string{"NIFTY", "SENSEX"}
}
