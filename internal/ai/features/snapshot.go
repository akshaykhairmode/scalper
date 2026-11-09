package features

import "encoding/json"

// StateSnapshot represents the compact state vector compiled from real-time features and sent to Jev.
type StateSnapshot struct {
	Timestamp       string           `json:"timestamp"`
	Underlying      string           `json:"underlying"`
	SpotPrice       float64          `json:"spot_price"`
	Strike          string           `json:"strike"`
	StrikePrice     float64          `json:"strike_price,omitempty"`
	OptionType      string           `json:"option_type"` // CE or PE
	OptionLTP       float64          `json:"option_ltp"`
	Expiry          string           `json:"expiry,omitempty"`
	DaysToExpiry    float64          `json:"days_to_expiry,omitempty"`
	MoneynessPct    float64          `json:"moneyness_pct,omitempty"`
	VWAP            float64          `json:"vwap"`
	EMA9            float64          `json:"ema_9"`
	EMA21           float64          `json:"ema_21"`
	RSI14           float64          `json:"rsi_14"`
	ATR14           float64          `json:"atr_14"`
	DistFromVWAPPct float64          `json:"dist_from_vwap_pct"`
	OrderBook       OrderBookMetrics `json:"order_book"`
	OI              int64            `json:"oi"`
	Volume          int64            `json:"volume,omitempty"`
	PCR             float64          `json:"pcr,omitempty"`
	MacroRegime     string           `json:"macro_regime,omitempty"`
	MacroBias       string           `json:"macro_bias,omitempty"`
	AgentSignal     string           `json:"agent_signal,omitempty"`
	AgentScore      int              `json:"agent_score,omitempty"`
	AgentConfidence float64          `json:"agent_confidence,omitempty"`
	AgentReasoning  string           `json:"agent_reasoning,omitempty"`
	TargetPct       float64          `json:"target_pct,omitempty"`
	StopLossPct     float64          `json:"stop_loss_pct,omitempty"`
	Timeframe       string           `json:"timeframe,omitempty"`
}

// ToJSON serializes the state snapshot into a JSON string suitable for Jev prompt evaluation.
func (s StateSnapshot) ToJSON() (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
