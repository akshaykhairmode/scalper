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
	"sort"
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
		timeout = 90 * time.Second
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
	Reasoning   *openAIReasoning      `json:"reasoning,omitempty"`
}

// openAIReasoning controls OpenRouter's unified reasoning tokens. Structured
// JSON calls disable reasoning so the model returns the answer directly
// instead of spending the token budget on a visible chain-of-thought.
type openAIReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Exclude bool   `json:"exclude,omitempty"`
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

	rawContent, err := c.completeChat(ctx, messages, true, 3000)
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

const candidateGenerationPrompt = `You are System 2, the candidate generator for an intraday Indian index/commodity-options paper-trading system.
You receive a small near-ATM ladder of call and put contracts for one underlying, plus underlying context (spot, day OHLC, VWAP, change, India VIX, PCR/max pain/OI walls when available), order-book microstructure, and any open paper positions. A separate System 1 model (Jev) will independently validate and score every candidate before any paper position is opened.

Rules:
- Only return action "BUY" when the setup has a clear directional edge, acceptable spread, and positive risk/reward. Otherwise return "PASS".
- Never invent contracts, prices, or indicators. Only use the supplied candidates and context.
- Return one decision per supplied candidate.
- Choose at most TWO BUY decisions; PASS the rest. Prefer the contract with the cleanest risk/reward and liquidity.
- "avg_price" is the day's volume-weighted average price when available. "vwap"/EMA/RSI/ATR may be short-window.
- If "indicators_source" is "placeholder", the technical indicators could not be computed from real ticks; do not over-weight them.
- "moneyness_label" (ATM/ITM/OTM) and "days_to_expiry" describe the contract.
- Keep reasoning concise and grounded in the supplied numbers.
- Do not write a thinking process, analysis, numbered steps, or commentary.
- Your entire response must be a single JSON object. Start with "{" and end with "}".

Respond ONLY with valid JSON matching this schema:
{
  "decisions": [
    {
      "instrument_key": "<exact supplied instrument_key>",
      "option_type": "CE" | "PE",
      "strike": "<supplied strike>",
      "action": "BUY" | "PASS",
      "score": <integer 1-5, where 5 is a textbook setup>,
      "confidence": <float 0.0-1.0>,
      "reasoning": "<1-2 sentence tactical rationale>"
    }
  ],
  "market_note": "<optional one-sentence regime note>"
}
Return {"decisions":[]} when no candidate is worth taking. Do not include markdown or conversational filler.`

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

	raw, err := c.completeChat(ctx, messages, true, 2000)
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

// EvaluateCandidates asks the reasoning agent to propose which of the supplied
// ATM contracts (if any) are worth sending to Jev for independent validation.
func (c *Client) EvaluateCandidates(ctx context.Context, req CandidateRequest) ([]CandidateDecision, error) {
	if c == nil || c.httpClient == nil {
		return nil, errors.New("agent: client not initialized")
	}
	if len(req.Candidates) == 0 {
		return nil, nil
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("agent: marshal candidate request: %w", err)
	}

	messages := []openAIChatMessage{
		{Role: "system", Content: candidateGenerationPrompt},
		{Role: "user", Content: string(payload)},
	}

	raw, err := c.completeChat(ctx, messages, true, 4000)
	if err != nil {
		return nil, err
	}

	decisions, err := parseCandidateDecisions(raw)
	if err != nil {
		rawSnippet := raw
		if len(rawSnippet) > 600 {
			rawSnippet = rawSnippet[:600] + "... (truncated)"
		}
		return nil, fmt.Errorf("agent: parse candidate decisions: %w (raw: %s)", err, rawSnippet)
	}
	return decisions, nil
}

func parseCandidateDecisions(raw string) ([]CandidateDecision, error) {
	for _, candidate := range extractJSONCandidates(raw) {
		// Preferred shape: {"decisions":[...], "market_note":"..."}
		var resp CandidateResponse
		if err := json.Unmarshal([]byte(candidate), &resp); err == nil && resp.Decisions != nil {
			return normalizeCandidateDecisions(resp.Decisions), nil
		}

		// Bare array of decisions.
		var arr []CandidateDecision
		if err := json.Unmarshal([]byte(candidate), &arr); err == nil && arr != nil {
			return normalizeCandidateDecisions(arr), nil
		}

		// A single decision object.
		var single CandidateDecision
		if err := json.Unmarshal([]byte(candidate), &single); err == nil && strings.TrimSpace(single.Action) != "" {
			return normalizeCandidateDecisions([]CandidateDecision{single}), nil
		}
	}
	return nil, errors.New("no decisions found in agent response")
}

func normalizeCandidateDecisions(in []CandidateDecision) []CandidateDecision {
	out := make([]CandidateDecision, 0, len(in))
	for _, d := range in {
		action := strings.ToUpper(strings.TrimSpace(d.Action))
		if action != "BUY" && !strings.HasPrefix(action, "BUY_") && action != "LONG" {
			action = "PASS"
		} else {
			action = "BUY"
		}
		d.Action = action
		if d.Score < 1 {
			d.Score = 3
		} else if d.Score > 5 {
			d.Score = 5
		}
		if d.Confidence > 1.0 {
			if d.Confidence > 2.0 && d.Confidence <= 100.0 {
				d.Confidence = d.Confidence / 100.0
			} else {
				d.Confidence = 1.0
			}
		}
		if d.Confidence <= 0 {
			d.Confidence = 0.55
		}
		d.OptionType = strings.ToUpper(strings.TrimSpace(d.OptionType))
		out = append(out, d)
	}
	return out
}

// extractJSONCandidates returns every plausible JSON value found in a model
// reply, preferring larger (outer) values. Reasoning models sometimes wrap the
// requested JSON in prose or a thinking process, so callers try each candidate.
func extractJSONCandidates(raw string) []string {
	var values []string
	seen := make(map[string]bool)

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		if s[0] != '{' && s[0] != '[' {
			return
		}
		seen[s] = true
		values = append(values, s)
	}

	// Fenced code blocks anywhere in the reply.
	rest := raw
	for {
		start := strings.Index(rest, "```")
		if start == -1 {
			break
		}
		after := rest[start+3:]
		if strings.HasPrefix(strings.ToLower(after), "json") {
			after = after[4:]
		}
		end := strings.Index(after, "```")
		if end == -1 {
			break
		}
		add(after[:end])
		rest = after[end+3:]
	}

	// Balanced JSON values embedded in prose.
	for i := 0; i < len(raw); i++ {
		if raw[i] != '{' && raw[i] != '[' {
			continue
		}
		if value, _, ok := balancedJSON(raw, i); ok {
			add(value)
		}
	}

	sort.SliceStable(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

// balancedJSON returns the balanced JSON value starting at start, honoring
// quoted strings and escapes so braces inside string literals are ignored.
func balancedJSON(s string, start int) (string, int, bool) {
	if start < 0 || start >= len(s) {
		return "", start, false
	}
	open := s[start]
	var closing byte
	switch open {
	case '{':
		closing = '}'
	case '[':
		closing = ']'
	default:
		return "", start, false
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}

		if c == '"' {
			inString = true
			continue
		}
		if c == open {
			depth++
			continue
		}
		if c == closing {
			depth--
			if depth == 0 {
				return s[start : i+1], i, true
			}
		}
	}
	return "", start, false
}

// Chat executes a freeform single-turn prompt against the configured LLM provider.
func (c *Client) Chat(ctx context.Context, prompt string) (string, error) {
	if c == nil || c.httpClient == nil {
		return "", errors.New("agent: client not initialized")
	}

	messages := []openAIChatMessage{
		{Role: "user", Content: prompt},
	}
	return c.completeChat(ctx, messages, false, 800)
}

func (c *Client) completeChat(ctx context.Context, messages []openAIChatMessage, jsonMode bool, maxTokens int) (string, error) {
	useJSONMode := jsonMode && c.supportsJSONMode()
	// Structured JSON tasks disable reasoning on OpenRouter when the model
	// supports it, so the answer is not buried under a thinking trace.
	disableReasoning := jsonMode && c.isOpenRouter()

	body, status, err := c.doChat(ctx, messages, useJSONMode, disableReasoning, maxTokens)
	if err != nil {
		return "", err
	}

	// Some models reject reasoning controls even though they are reasoning
	// capable; retry once without the parameter.
	if disableReasoning && isReasoningRejection(status, body) {
		disableReasoning = false
		body, status, err = c.doChat(ctx, messages, useJSONMode, false, maxTokens)
		if err != nil {
			return "", err
		}
	}

	// Some free/reasoning models reject response_format. Retry once without it.
	if useJSONMode && isJSONModeRejection(status, body) {
		body, status, err = c.doChat(ctx, messages, false, disableReasoning, maxTokens)
		if err != nil {
			return "", err
		}
	}

	if status < 200 || status >= 300 {
		errBody := strings.TrimSpace(string(body))
		if len(errBody) > 300 {
			errBody = errBody[:300] + "... (truncated)"
		}
		return "", fmt.Errorf("agent: API returned HTTP %d: %s", status, errBody)
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

func (c *Client) supportsJSONMode() bool {
	if strings.EqualFold(strings.TrimSpace(c.cfg.Provider), "ollama") {
		return false
	}
	// OpenRouter's free variants (e.g. nemotron:free) do not advertise
	// response_format/structured_outputs, so skip the extra round trip.
	if strings.Contains(strings.ToLower(c.cfg.Model), ":free") {
		return false
	}
	return true
}

func (c *Client) isOpenRouter() bool {
	if strings.EqualFold(strings.TrimSpace(c.cfg.Provider), "openrouter") {
		return true
	}
	return strings.Contains(strings.ToLower(c.endpoint), "openrouter.ai")
}

func (c *Client) doChat(ctx context.Context, messages []openAIChatMessage, jsonMode, disableReasoning bool, maxTokens int) ([]byte, int, error) {
	reqBody := openAIChatRequest{
		Model:       c.cfg.Model,
		Messages:    messages,
		Temperature: 0.2,
		MaxTokens:   maxTokens,
	}
	if jsonMode {
		reqBody.Format = &openAIResponseFormat{Type: "json_object"}
	}
	if disableReasoning {
		reqBody.Reasoning = &openAIReasoning{Effort: "none", Exclude: true}
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("agent: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("agent: create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(c.cfg.APIKey); apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	httpReq.Header.Set("HTTP-Referer", "https://scalper.local")
	httpReq.Header.Set("X-Title", "Scalper Trading Terminal")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("agent: HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("agent: read response: %w", err)
	}
	return body, resp.StatusCode, nil
}

func isJSONModeRejection(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity && status != http.StatusNotFound {
		return false
	}
	s := strings.ToLower(string(body))
	return strings.Contains(s, "response_format") ||
		strings.Contains(s, "structured") ||
		strings.Contains(s, "json mode") ||
		strings.Contains(s, "json_object")
}

func isReasoningRejection(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity && status != http.StatusNotFound {
		return false
	}
	s := strings.ToLower(string(body))
	return strings.Contains(s, "reasoning") ||
		strings.Contains(s, "thinking") ||
		strings.Contains(s, "effort")
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
