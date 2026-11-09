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
		if chainRows, cerr := s.market.Chain(ctx, s.marketBaseURL(), underlying, expiries[0].Date); cerr == nil && len(chainRows) > 0 {
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

	nearestExpiry := expiries[0].Date
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

// GetConfiguredUnderlyings returns the list of active index keys configured in market.indices.
func (s *Server) GetConfiguredUnderlyings() []string {
	if len(s.cfg.Market.Indices) == 0 {
		return []string{"NIFTY", "BANKNIFTY", "SENSEX"}
	}
	var res []string
	for _, idx := range s.cfg.Market.Indices {
		key := strings.TrimSpace(idx.Key)
		if key != "" {
			res = append(res, key)
		}
	}
	if len(res) == 0 {
		return []string{"NIFTY", "BANKNIFTY", "SENSEX"}
	}
	return res
}
