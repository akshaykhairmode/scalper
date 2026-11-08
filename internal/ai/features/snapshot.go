package features

import "encoding/json"

// StateSnapshot represents the compact state vector compiled from real-time features and sent to Jev.
type StateSnapshot struct {
	Timestamp       string           `json:"timestamp"`
	Underlying      string           `json:"underlying"`
	SpotPrice       float64          `json:"spot_price"`
	Strike          string           `json:"strike"`
	OptionType      string           `json:"option_type"` // CE or PE
	OptionLTP       float64          `json:"option_ltp"`
	VWAP            float64          `json:"vwap"`
	EMA9            float64          `json:"ema_9"`
	EMA21           float64          `json:"ema_21"`
	RSI14           float64          `json:"rsi_14"`
	ATR14           float64          `json:"atr_14"`
	DistFromVWAPPct float64          `json:"dist_from_vwap_pct"`
	OrderBook       OrderBookMetrics `json:"order_book"`
	OI              int64            `json:"oi"`
	PCR             float64          `json:"pcr"`
	MacroRegime     string           `json:"macro_regime"`
	MacroBias       string           `json:"macro_bias"`
}

// ToJSON serializes the state snapshot into a JSON string suitable for Jev prompt evaluation.
func (s StateSnapshot) ToJSON() (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
