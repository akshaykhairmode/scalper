package agent

import (
	"context"
	"time"

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

// Provider represents an interface for an LLM reasoning agent.
type Provider interface {
	GeneratePlaybook(ctx context.Context, m MarketContext) (*store.Playbook, error)
	Chat(ctx context.Context, prompt string) (string, error)
}
