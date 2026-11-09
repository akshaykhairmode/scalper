package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/ai/features"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

// GetSpotPrice retrieves the latest spot price of an underlying index (e.g. NIFTY).
func (s *Server) GetSpotPrice(ctx context.Context, underlying string) (float64, error) {
	// 1. Try cached indices first if fresh
	s.indicesMu.RLock()
	if s.indicesRaw != nil {
		var cached struct {
			Indices []market.IndexQuote `json:"indices"`
		}
		if err := json.Unmarshal(s.indicesRaw, &cached); err == nil {
			for _, idx := range cached.Indices {
				if strings.EqualFold(idx.Key, underlying) || strings.EqualFold(idx.DisplayName, underlying) {
					if idx.LTP > 0 {
						s.indicesMu.RUnlock()
						return idx.LTP, nil
					}
				}
			}
		}
	}
	s.indicesMu.RUnlock()

	if s.market == nil {
		return 0, errors.New("market API not configured")
	}

	// 2. Fetch live indices via market service using valid marketBaseURL
	indices, err := s.market.Indices(ctx, s.marketBaseURL())
	if err == nil {
		if payload, merr := json.Marshal(map[string]any{"indices": indices}); merr == nil {
			s.storeIndicesPayload(payload)
		}
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
	if ltp, ok := s.cachedLTP(instrumentKey, 5*time.Second); ok {
		if bids, asks, dok := s.cachedDepth(instrumentKey, 5*time.Second); dok {
			return features.CalculateOrderBookMetrics(ltp, toMarketDepth(bids), toMarketDepth(asks)), nil
		}
	}
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
	info.Open = q.Open
	info.High = q.High
	info.Low = q.Low
	info.AvgPrice = q.AvgPrice
	info.ChangePct = q.ChangePct
	info.Volume = q.Volume
	info.OI = q.OI
	info.Bid = q.Bid
	info.Ask = q.Ask

	depth := features.CalculateOrderBookMetrics(q.LTP, q.Bids, q.Asks)
	info.OrderBook = depth
	return depth, info, nil
}

// GetOptionLTP returns the latest traded price for an option instrument. It is
// used as a REST fallback when the live feed stops delivering ticks for a
// tracked paper position.
func (s *Server) GetOptionLTP(ctx context.Context, instrumentKey string) (float64, error) {
	if ltp, ok := s.cachedLTP(instrumentKey, 5*time.Second); ok {
		return ltp, nil
	}
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

// GetUnderlyingContext returns index/commodity day context for the agent: spot,
// day OHLC, VWAP, India VIX and minutes left in the session.
func (s *Server) GetUnderlyingContext(ctx context.Context, underlying string) (features.UnderlyingContext, error) {
	out := features.UnderlyingContext{}

	var quotes []market.IndexQuote
	s.indicesMu.RLock()
	if s.indicesRaw != nil {
		var cached struct {
			Indices []market.IndexQuote `json:"indices"`
		}
		if err := json.Unmarshal(s.indicesRaw, &cached); err == nil {
			quotes = cached.Indices
		}
	}
	s.indicesMu.RUnlock()

	if len(quotes) == 0 && s.market != nil {
		if fetched, err := s.market.Indices(ctx, s.marketBaseURL()); err == nil {
			quotes = fetched
			if payload, merr := json.Marshal(map[string]any{"indices": fetched}); merr == nil {
				s.storeIndicesPayload(payload)
			}
		}
	}

	for _, q := range quotes {
		if strings.EqualFold(q.Key, underlying) || strings.EqualFold(q.DisplayName, underlying) {
			out.SpotPrice = q.LTP
			out.PreviousClose = q.PreviousClose
			out.ChangePct = q.ChangePct
			out.DayOpen = q.Open
			out.DayHigh = q.High
			out.DayLow = q.Low
			out.AvgPrice = q.AvgPrice
			break
		}
	}

	if out.SpotPrice <= 0 {
		if spot, err := s.GetSpotPrice(ctx, underlying); err == nil {
			out.SpotPrice = spot
		}
	}
	for _, q := range quotes {
		if strings.Contains(strings.ToUpper(q.Key), "VIX") || strings.Contains(strings.ToUpper(q.DisplayName), "VIX") {
			if q.LTP > 0 {
				out.IndiaVIX = q.LTP
			}
			break
		}
	}
	if out.IndiaVIX <= 0 {
		if vix, err := s.GetSpotPrice(ctx, "INDIA VIX"); err == nil {
			out.IndiaVIX = vix
		}
	}
	out.SessionMinutesLeft = s.sessionMinutesLeft(underlying)
	return out, nil
}

// GetChainContext returns a near-ATM option ladder plus chain-level stats for
// one underlying. It replaces the two-contract ATM-only view with a small
// strike ladder so the agent can choose the best contract.
func (s *Server) GetChainContext(ctx context.Context, underlying string, spot float64) (features.ChainContext, error) {
	if s.market == nil {
		return features.ChainContext{}, errors.New("market API not configured")
	}

	expiries, err := s.market.Expiries(underlying)
	if err != nil || len(expiries) == 0 {
		return features.ChainContext{}, fmt.Errorf("no expiries available for %q: %w", underlying, err)
	}
	expiry := firstLiveExpiry(expiries)

	if spot <= 0 {
		spot, _ = s.GetSpotPrice(ctx, underlying)
	}

	// Fetch only a bounded near-ATM window. 20 strikes = 40 instruments, which
	// fits in one quotes batch and keeps the scanner far below Kotak's rate
	// ceiling. A full-chain fetch is used only as a fallback.
	var rows []market.ChainRow
	if spot > 0 {
		if cw, ok := s.market.(chainWindowAPI); ok {
			if page, werr := cw.ChainWindow(ctx, s.marketBaseURL(), underlying, expiry, spot, 20, "around"); werr == nil && len(page.Rows) > 0 {
				rows = page.Rows
			}
		}
	}
	if len(rows) == 0 {
		rows, err = s.market.Chain(ctx, s.marketBaseURL(), underlying, expiry)
	}
	if err != nil || len(rows) == 0 {
		return features.ChainContext{}, fmt.Errorf("option chain empty for %q %s: %w", underlying, expiry, err)
	}
	if spot <= 0 {
		spot = rows[len(rows)/2].Strike
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Strike < rows[j].Strike })

	var out features.ChainContext
	var totalCallOI, totalPutOI int64
	var maxCallOI, maxPutOI int64
	for _, r := range rows {
		if r.Call != nil {
			totalCallOI += r.Call.OI
			if r.Call.OI > maxCallOI {
				maxCallOI = r.Call.OI
				out.CallOIWall = r.Strike
			}
		}
		if r.Put != nil {
			totalPutOI += r.Put.OI
			if r.Put.OI > maxPutOI {
				maxPutOI = r.Put.OI
				out.PutOIWall = r.Strike
			}
		}
	}
	if totalCallOI > 0 {
		out.PCR = float64(totalPutOI) / float64(totalCallOI)
	}
	out.MaxPain = computeMaxPain(rows)

	atmIdx := 0
	minDiff := math.MaxFloat64
	for i, r := range rows {
		if diff := math.Abs(r.Strike - spot); diff < minDiff {
			minDiff = diff
			atmIdx = i
		}
	}
	const radius = 2
	start := atmIdx - radius
	if start < 0 {
		start = 0
	}
	end := atmIdx + radius
	if end >= len(rows) {
		end = len(rows) - 1
	}
	for _, r := range rows[start : end+1] {
		if r.Call != nil && r.Call.LTP > 0 {
			out.Candidates = append(out.Candidates, optionInfoFromQuote(r.Call, spot))
		}
		if r.Put != nil && r.Put.LTP > 0 {
			out.Candidates = append(out.Candidates, optionInfoFromQuote(r.Put, spot))
		}
	}
	return out, nil
}

func optionInfoFromQuote(q *market.OptionQuote, spot float64) features.OptionInfo {
	moneyness := 0.0
	if spot > 0 && q.Strike > 0 {
		if strings.EqualFold(q.OptionType, "PE") {
			moneyness = ((q.Strike - spot) / spot) * 100.0
		} else {
			moneyness = ((spot - q.Strike) / spot) * 100.0
		}
	}
	label := "ATM"
	if math.Abs(moneyness) >= 0.5 {
		if moneyness > 0 {
			label = "ITM"
		} else {
			label = "OTM"
		}
	}
	days := 0.0
	if expiry, perr := time.Parse("2006-01-02", q.Expiry); perr == nil {
		days = math.Max(0, time.Until(expiry).Hours()/24.0)
		days = math.Round(days*10) / 10
	}
	return features.OptionInfo{
		InstrumentKey:  q.InstrumentKey,
		Underlying:     q.Underlying,
		Expiry:         q.Expiry,
		Strike:         q.Strike,
		OptionType:     q.OptionType,
		LotSize:        q.LotSize,
		LTP:            q.LTP,
		PreviousClose:  q.PreviousClose,
		Open:           q.Open,
		High:           q.High,
		Low:            q.Low,
		AvgPrice:       q.AvgPrice,
		ChangePct:      q.ChangePct,
		Volume:         q.Volume,
		OI:             q.OI,
		Bid:            q.Bid,
		Ask:            q.Ask,
		DaysToExpiry:   days,
		MoneynessLabel: label,
		OrderBook:      features.CalculateOrderBookMetrics(q.LTP, q.Bids, q.Asks),
	}
}

// computeMaxPain returns the strike with the lowest aggregate option-holder
// pain, using open interest as the weight.
func computeMaxPain(rows []market.ChainRow) float64 {
	var bestStrike float64
	bestPain := math.MaxFloat64
	for _, target := range rows {
		pain := 0.0
		for _, r := range rows {
			if r.Call != nil && target.Strike > r.Strike {
				pain += float64(r.Call.OI) * (target.Strike - r.Strike)
			}
			if r.Put != nil && r.Strike > target.Strike {
				pain += float64(r.Put.OI) * (r.Strike - target.Strike)
			}
		}
		if pain < bestPain {
			bestPain = pain
			bestStrike = target.Strike
		}
	}
	if bestPain == math.MaxFloat64 {
		return 0
	}
	return bestStrike
}

func (s *Server) sessionMinutesLeft(underlying string) int {
	cfg := s.configSnapshot()
	loc := cfg.Location()
	now := time.Now()
	if loc != nil {
		now = now.In(loc)
	}
	closeHour, closeMin := 15, 30
	if s.isMCX(underlying) {
		closeHour, closeMin = 23, 55
	}
	closeAt := time.Date(now.Year(), now.Month(), now.Day(), closeHour, closeMin, 0, 0, now.Location())
	if !now.Before(closeAt) {
		return 0
	}
	return int(closeAt.Sub(now).Minutes())
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
