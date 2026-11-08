package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/akshaykhairmode/scalper/internal/ai/features"
	"github.com/akshaykhairmode/scalper/internal/market"
)

// GetSpotPrice retrieves the latest spot price of an underlying index (e.g. NIFTY).
func (s *Server) GetSpotPrice(ctx context.Context, underlying string) (float64, error) {
	if s.market == nil {
		return 0, errors.New("market API not configured")
	}

	indices, err := s.market.Indices(ctx, "nse_cm")
	if err == nil {
		for _, idx := range indices {
			if strings.EqualFold(idx.Key, underlying) || strings.EqualFold(idx.DisplayName, underlying) {
				if idx.LTP > 0 {
					return idx.LTP, nil
				}
			}
		}
	}

	// Try BSE if not found on NSE
	bseIndices, err := s.market.Indices(ctx, "bse_cm")
	if err == nil {
		for _, idx := range bseIndices {
			if strings.EqualFold(idx.Key, underlying) || strings.EqualFold(idx.DisplayName, underlying) {
				if idx.LTP > 0 {
					return idx.LTP, nil
				}
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

	segment := "nse_fo"
	if strings.EqualFold(underlying, "SENSEX") || strings.EqualFold(underlying, "BANKEX") {
		segment = "bse_fo"
	}

	nearestExpiry := expiries[0].Date
	chainRows, err := s.market.Chain(ctx, segment, underlying, nearestExpiry)
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

	segment := "nse_fo"
	if strings.Contains(instrumentKey, "bse_fo") {
		segment = "bse_fo"
	}

	q, err := s.market.Quote(ctx, segment, instrumentKey)
	if err != nil {
		return features.OrderBookMetrics{}, err
	}

	return features.CalculateOrderBookMetrics(q.LTP, q.Bids, q.Asks), nil
}
