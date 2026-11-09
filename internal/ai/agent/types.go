package agent

import (
	"context"
	"time"

	"github.com/akshaykhairmode/scalper/internal/ai/features"
	"github.com/akshaykhairmode/scalper/internal/store"
)

// Config holds settings for communicating with any OpenAI-compatible reasoning agent
// (Ollama, OpenRouter, Google Gemini, OpenAI, Groq, DeepSeek, LocalAI, vLLM).
type Config struct {
	Provider string        `json:"provider"` // openrouter, ollama, gemini, openai, generic
	BaseURL  string        `json:"base_url"` // Base URL ending before or at /v1
	APIKey   string        `json:"api_key"`
	Model    string        `json:"model"`
	Timeout  time.Duration `json:"timeout"`
}

// MarketContext represents the pre-market or intraday snapshot sent to the reasoning agent.
type MarketContext struct {
	Date            string  `json:"date"`
	Index           string  `json:"index"`
	SpotPrice       float64 `json:"spot_price"`
	PCR             float64 `json:"pcr"`
	MaxPain         float64 `json:"max_pain"`
	CallOIWall      float64 `json:"call_oi_wall"`
	PutOIWall       float64 `json:"put_oi_wall"`
	IndiaVIX        float64 `json:"india_vix"`
	GiftNiftyChange float64 `json:"gift_nifty_change"`
	GlobalSentiment string  `json:"global_sentiment"`
	ExtraNotes      string  `json:"extra_notes"`
}

// PlaybookOutput is the structured JSON output schema expected from the LLM.
type PlaybookOutput struct {
	Bias          string   `json:"bias"`   // BULLISH, BEARISH, NEUTRAL, CHOPPY
	Regime        string   `json:"regime"` // TRENDING, BREAKOUT, RANGE_BOUND
	KeySupport    float64  `json:"key_support"`
	KeyResistance float64  `json:"key_resistance"`
	MaxPain       float64  `json:"max_pain"`
	PCR           float64  `json:"pcr"`
	Narrative     string   `json:"narrative"`
	FocusStrikes  []string `json:"focus_strikes"`
	JevCriteria   struct {
		MinScore       int     `json:"min_score"`
		MinConfidence  float64 `json:"min_confidence"`
		ActionAllowed  string  `json:"action_allowed"` // ALL, BUY_ONLY, SELL_ONLY
		VolatilityRisk string  `json:"volatility_risk"`
	} `json:"jev_criteria"`
}

// CandidateContract is one option contract presented to the reasoning agent.
type CandidateContract struct {
	InstrumentKey   string                    `json:"instrument_key,omitempty"`
	OptionType      string                    `json:"option_type"`
	Strike          string                    `json:"strike"`
	StrikePrice     float64                   `json:"strike_price,omitempty"`
	OptionLTP       float64                   `json:"option_ltp"`
	Expiry          string                    `json:"expiry,omitempty"`
	DaysToExpiry    float64                   `json:"days_to_expiry,omitempty"`
	MoneynessPct    float64                   `json:"moneyness_pct,omitempty"`
	VWAP            float64                   `json:"vwap,omitempty"`
	EMA9            float64                   `json:"ema_9,omitempty"`
	EMA21           float64                   `json:"ema_21,omitempty"`
	RSI14           float64                   `json:"rsi_14,omitempty"`
	ATR14           float64                   `json:"atr_14,omitempty"`
	DistFromVWAPPct float64                   `json:"dist_from_vwap_pct,omitempty"`
	OI              int64                     `json:"oi,omitempty"`
	Volume          int64                     `json:"volume,omitempty"`
	OrderBook       features.OrderBookMetrics `json:"order_book"`
}

// CandidateRequest is the market context sent to the reasoning agent to propose
// candidate paper trades for one underlying.
type CandidateRequest struct {
	Timestamp  string              `json:"timestamp"`
	Underlying string              `json:"underlying"`
	SpotPrice  float64             `json:"spot_price"`
	MacroBias  string              `json:"macro_bias,omitempty"`
	Regime     string              `json:"regime,omitempty"`
	Narrative  string              `json:"narrative,omitempty"`
	Candidates []CandidateContract `json:"candidates"`
}

// CandidateDecision is the agent's verdict for one candidate contract.
type CandidateDecision struct {
	InstrumentKey string  `json:"instrument_key,omitempty"`
	OptionType    string  `json:"option_type,omitempty"`
	Strike        string  `json:"strike,omitempty"`
	Action        string  `json:"action"` // BUY | PASS
	Score         int     `json:"score"`  // 1-5
	Confidence    float64 `json:"confidence"`
	Reasoning     string  `json:"reasoning,omitempty"`
}

// CandidateResponse is the structured JSON schema returned by the agent.
type CandidateResponse struct {
	Decisions  []CandidateDecision `json:"decisions"`
	MarketNote string              `json:"market_note,omitempty"`
}

// Provider represents an interface for an LLM reasoning agent.
type Provider interface {
	GeneratePlaybook(ctx context.Context, m MarketContext) (*store.Playbook, error)
	Chat(ctx context.Context, prompt string) (string, error)
}
