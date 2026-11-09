package features

import "github.com/akshaykhairmode/scalper/internal/market"

// OrderBookMetrics contains quantitative microstructure metrics derived from L2 depth.
type OrderBookMetrics struct {
	BestBid          float64 `json:"best_bid"`
	BestAsk          float64 `json:"best_ask"`
	TotalBidQty      int64   `json:"total_bid_qty"`
	TotalAskQty      int64   `json:"total_ask_qty"`
	ImbalanceRatio   float64 `json:"imbalance_ratio"`   // (BidQty - AskQty) / (BidQty + AskQty), between -1.0 and 1.0
	Spread           float64 `json:"spread"`            // Ask[0] - Bid[0]
	SpreadPct        float64 `json:"spread_pct"`        // (Spread / LTP) * 100
	WeightedMidpoint float64 `json:"weighted_midpoint"` // Microprice weighted by top-of-book quantities
}

// OptionInfo carries contract metadata and quote fields that are not part of the
// order-book microstructure but materially improve AI signal quality.
type OptionInfo struct {
	InstrumentKey string  `json:"instrument_key,omitempty"`
	Underlying    string  `json:"underlying,omitempty"`
	Expiry        string  `json:"expiry,omitempty"`
	Strike        float64 `json:"strike,omitempty"`
	OptionType    string  `json:"option_type,omitempty"`
	LotSize       int64   `json:"lot_size,omitempty"`
	LTP           float64 `json:"ltp,omitempty"`
	PreviousClose float64 `json:"previous_close,omitempty"`
	Volume        int64   `json:"volume,omitempty"`
	OI            int64   `json:"oi,omitempty"`
	Bid           float64 `json:"bid,omitempty"`
	Ask           float64 `json:"ask,omitempty"`
}

// CalculateOrderBookMetrics derives order flow and liquidity metrics from top-5 market depth levels.
func CalculateOrderBookMetrics(ltp float64, bids, asks []market.DepthLevel) OrderBookMetrics {
	var m OrderBookMetrics
	for _, b := range bids {
		m.TotalBidQty += b.Quantity
	}
	for _, a := range asks {
		m.TotalAskQty += a.Quantity
	}
	total := m.TotalBidQty + m.TotalAskQty
	if total > 0 {
		m.ImbalanceRatio = float64(m.TotalBidQty-m.TotalAskQty) / float64(total)
	}

	bestBid := 0.0
	bestAsk := 0.0
	if len(bids) > 0 {
		bestBid = bids[0].Price
	}
	if len(asks) > 0 {
		bestAsk = asks[0].Price
	}

	m.BestBid = bestBid
	m.BestAsk = bestAsk

	if bestBid > 0 && bestAsk > 0 && bestAsk >= bestBid {
		m.Spread = bestAsk - bestBid
		if ltp > 0 {
			m.SpreadPct = (m.Spread / ltp) * 100
		}
		topVol := bids[0].Quantity + asks[0].Quantity
		if topVol > 0 {
			m.WeightedMidpoint = (bestBid*float64(asks[0].Quantity) + bestAsk*float64(bids[0].Quantity)) / float64(topVol)
		} else {
			m.WeightedMidpoint = (bestBid + bestAsk) / 2.0
		}
	} else {
		m.WeightedMidpoint = ltp
	}

	return m
}
