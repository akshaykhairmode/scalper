// Package agent implements generic LLM reasoning agent clients.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/store"
)

// Client is a generic OpenAI-compatible LLM client capable of communicating with
// Ollama, OpenRouter, Google Gemini, OpenAI, Groq, DeepSeek, or any local AI server.
type Client struct {
	cfg        Config
	httpClient *http.Client
	endpoint   string
}

// NewClient initializes a new generic LLM reasoning agent client.
func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))

	if baseURL == "" {
		switch provider {
		case "ollama":
			baseURL = "http://localhost:11434/v1"
		case "gemini":
			baseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
		case "openai":
			baseURL = "https://api.openai.com/v1"
		default: // openrouter or generic
			baseURL = "https://openrouter.ai/api/v1"
		}
	}

	endpoint := baseURL
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}

	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		switch provider {
		case "ollama":
			model = "llama3"
		case "gemini":
			model = "gemini-1.5-flash"
		case "openai":
			model = "gpt-4o-mini"
		default:
			model = "nvidia/nemotron-3.5-lightning:free"
		}
	}
	cfg.Model = model

	return &Client{
		cfg:      cfg,
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

type openAIChatRequest struct {
	Model       string                `json:"model"`
	Messages    []openAIChatMessage   `json:"messages"`
	Temperature float64               `json:"temperature"`
	MaxTokens   int                   `json:"max_tokens,omitempty"`
	Format      *openAIResponseFormat `json:"response_format,omitempty"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

type openAIChatResponse struct {
	Choices []openAIChatChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

type openAIChatChoice struct {
	Message openAIChatChoiceMessage `json:"message"`
	Text    string                  `json:"text,omitempty"`
}

type openAIChatChoiceMessage struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Reasoning        string `json:"reasoning,omitempty"`
}

const systemPrompt = `You are an elite derivatives market analyst and Chief Investment Officer for Indian index options (NIFTY, BANKNIFTY, SENSEX).
Analyze the provided market context (Spot price, Put/Call Ratio, Open Interest walls, Max Pain, VIX, and global cues).
Synthesize the technical and macroeconomic structure and output strict JSON matching this schema:

{
  "bias": "BULLISH" | "BEARISH" | "NEUTRAL" | "CHOPPY",
  "regime": "TRENDING" | "BREAKOUT" | "RANGE_BOUND",
  "key_support": <float>,
  "key_resistance": <float>,
  "max_pain": <float>,
  "pcr": <float>,
  "narrative": "<concise 2-3 sentence strategic briefing>",
  "focus_strikes": ["24500 CE", "24500 PE"],
  "jev_criteria": {
    "min_score": 4,
    "min_confidence": 0.80,
    "action_allowed": "ALL" | "BUY_ONLY" | "SELL_ONLY",
    "volatility_risk": "LOW" | "NORMAL" | "HIGH"
  }
}

Respond ONLY with valid JSON. Do not include markdown codeblocks or conversational filler.`

// GeneratePlaybook requests a daily strategic market plan from the configured LLM provider.
func (c *Client) GeneratePlaybook(ctx context.Context, m MarketContext) (*store.Playbook, error) {
	if c == nil || c.httpClient == nil {
		return nil, errors.New("agent: client not initialized")
	}

	userPromptBytes, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("agent: marshal market context: %w", err)
	}

	messages := []openAIChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: string(userPromptBytes)},
	}

	rawContent, err := c.completeChat(ctx, messages, true, 2500)
	if err != nil {
		return nil, err
	}

	cleanJSON := cleanJSONBlock(rawContent)
	var out PlaybookOutput
	if err := json.Unmarshal([]byte(cleanJSON), &out); err != nil {
		rawSnippet := rawContent
		if len(rawSnippet) > 200 {
			rawSnippet = rawSnippet[:200] + "... (truncated)"
		}
		return nil, fmt.Errorf("agent: parse playbook JSON response: %w (raw response: %s)", err, rawSnippet)
	}

	jevRulesJSON, _ := json.Marshal(out.JevCriteria)

	date := m.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	source := strings.TrimSpace(c.cfg.Provider)
	if source == "" {
		source = "agent"
	}

	return &store.Playbook{
		PlaybookDate:  date,
		Bias:          out.Bias,
		Regime:        out.Regime,
		KeySupport:    out.KeySupport,
		KeyResistance: out.KeyResistance,
		MaxPain:       out.MaxPain,
		PCR:           out.PCR,
		Narrative:     out.Narrative,
		JevRulesJSON:  string(jevRulesJSON),
		Source:        source,
		CreatedAt:     time.Now(),
	}, nil
}

// ContractSetup holds input parameters for tactical option scalp evaluation.
type ContractSetup struct {
	Underlying    string  `json:"underlying"`
	SpotPrice     float64 `json:"spot_price"`
	OptionType    string  `json:"option_type"`
	Strike        string  `json:"strike"`
	OptionLTP     float64 `json:"option_ltp"`
	MacroBias     string  `json:"macro_bias"`
	OrderBookDesc string  `json:"order_book_desc"`
}

// ContractDecision is the structured tactical decision returned by the Agent.
type ContractDecision struct {
	Action     string  `json:"action"` // BUY | PASS
	Score      int     `json:"score"`  // 1-5
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

const contractEvalPrompt = `You are an elite intraday option scalp analyst for Indian index options (NIFTY, BANKNIFTY).
Evaluate if entering a scalp position in this specific option contract right now at current LTP offers positive expected value based on spot price, strike distance, and order momentum.
Respond ONLY with a JSON object matching this schema:
{
  "action": "BUY" | "PASS",
  "score": <integer from 1 to 5, where 4-5 is high conviction>,
  "confidence": <float from 0.0 to 1.0>,
  "reasoning": "<concise 1-2 sentence tactical scalp reasoning>"
}
Do not include markdown or conversational text.`

// EvaluateContract asks the LLM reasoning agent to evaluate an intraday options contract.
func (c *Client) EvaluateContract(ctx context.Context, setup ContractSetup) (ContractDecision, error) {
	if c == nil || c.httpClient == nil {
		return ContractDecision{}, errors.New("agent: client not initialized")
	}

	userMsg := fmt.Sprintf("Underlying: %s (Spot: ₹%.2f)\nOption: %s Strike: %s @ ₹%.2f\nMacro Bias: %s\nDepth: %s",
		setup.Underlying, setup.SpotPrice, setup.OptionType, setup.Strike, setup.OptionLTP, setup.MacroBias, setup.OrderBookDesc)

	messages := []openAIChatMessage{
		{Role: "system", Content: contractEvalPrompt},
		{Role: "user", Content: userMsg},
	}

	raw, err := c.completeChat(ctx, messages, true, 1500)
	if err != nil {
		return ContractDecision{}, err
	}

	clean := cleanJSONBlock(raw)
	var decision ContractDecision
	if err := json.Unmarshal([]byte(clean), &decision); err != nil {
		return ContractDecision{}, fmt.Errorf("agent: decode decision: %w (raw: %s)", err, raw)
	}

	action := strings.ToUpper(strings.TrimSpace(decision.Action))
	if action != "BUY" {
		action = "PASS"
	}
	decision.Action = action

	if decision.Score <= 0 {
		decision.Score = 3
	}
	if decision.Confidence <= 0 {
		decision.Confidence = 0.60
	}

	return decision, nil
}

// Chat executes a freeform single-turn prompt against the configured LLM provider.
func (c *Client) Chat(ctx context.Context, prompt string) (string, error) {
	if c == nil || c.httpClient == nil {
		return "", errors.New("agent: client not initialized")
	}

	messages := []openAIChatMessage{
		{Role: "user", Content: prompt},
	}
	return c.completeChat(ctx, messages, false, 500)
}

func (c *Client) completeChat(ctx context.Context, messages []openAIChatMessage, jsonMode bool, maxTokens int) (string, error) {
	reqBody := openAIChatRequest{
		Model:       c.cfg.Model,
		Messages:    messages,
		Temperature: 0.2,
		MaxTokens:   maxTokens,
	}
	if jsonMode {
		reqBody.Format = &openAIResponseFormat{Type: "json_object"}
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("agent: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("agent: create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(c.cfg.APIKey); apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	httpReq.Header.Set("HTTP-Referer", "https://scalper.local")
	httpReq.Header.Set("X-Title", "Scalper Trading Terminal")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("agent: HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("agent: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody := strings.TrimSpace(string(body))
		if len(errBody) > 300 {
			errBody = errBody[:300] + "... (truncated)"
		}
		return "", fmt.Errorf("agent: API returned HTTP %d: %s", resp.StatusCode, errBody)
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", fmt.Errorf("agent: decode response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("agent: API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", errors.New("agent: no completion choices returned by model")
	}

	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	if content == "" {
		if chatResp.Choices[0].Message.ReasoningContent != "" {
			content = strings.TrimSpace(chatResp.Choices[0].Message.ReasoningContent)
		} else if chatResp.Choices[0].Message.Reasoning != "" {
			content = strings.TrimSpace(chatResp.Choices[0].Message.Reasoning)
		} else if chatResp.Choices[0].Text != "" {
			content = strings.TrimSpace(chatResp.Choices[0].Text)
		}
	}

	return content, nil
}

func cleanJSONBlock(input string) string {
	s := strings.TrimSpace(input)
	if start := strings.Index(s, "```"); start != -1 {
		after := s[start+3:]
		if strings.HasPrefix(strings.ToLower(after), "json") {
			after = after[4:]
		}
		if end := strings.Index(after, "```"); end != -1 {
			s = strings.TrimSpace(after[:end])
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end != -1 && end > start {
		s = s[start : end+1]
	}
	return strings.TrimSpace(s)
}
