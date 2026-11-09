// Package jev implements the TypeSafe AI System One decision model client.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Client handles fast, bounded decision evaluations via TypeSafe AI's Jev model.
type Client struct {
	cfg        Config
	endpoint   string
	httpClient *http.Client
}

// NewClient initializes a new Jev System 1 decision client.
func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 8000 * time.Millisecond
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	} else if strings.HasPrefix(strings.TrimSpace(cfg.APIKey), "sk-or-") && strings.Contains(baseURL, "typesafe.ai") {
		baseURL = "https://openrouter.ai/api/v1"
	}

	isOpenRouter := strings.Contains(baseURL, "openrouter.ai")

	endpoint := baseURL
	if isOpenRouter {
		if !strings.HasSuffix(endpoint, "/decisions") {
			endpoint = "https://openrouter.ai/api/alpha/decisions"
		}
	} else {
		endpoint = strings.TrimSuffix(baseURL, "/system_one")
		if !strings.HasSuffix(endpoint, "/systemone") && !strings.HasSuffix(endpoint, "/decisions") {
			endpoint += "/systemone"
		}
	}

	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		if isOpenRouter {
			model = "~typesafe/jev-latest"
		} else {
			model = "jev-latest"
		}
	} else if isOpenRouter {
		model = normalizeOpenRouterModel(model)
	} else {
		model = strings.TrimPrefix(model, "~typesafe/")
		model = strings.TrimPrefix(model, "typesafe/")
	}
	cfg.Model = model

	return &Client{
		cfg:      cfg,
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// EvaluateSetup sends a structured market state vector to Jev and returns typed tactical signals.
func (c *Client) EvaluateSetup(ctx context.Context, stateJSON string) (EvaluationResult, error) {
	if c == nil || c.httpClient == nil {
		return EvaluationResult{}, errors.New("jev: client not initialized")
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" && !strings.Contains(c.endpoint, "127.0.0.1") && !strings.Contains(c.endpoint, "localhost") {
		return EvaluationResult{}, errors.New("jev: API key not configured")
	}
	start := time.Now()

	reqBody := SystemOneRequest{
		Model: c.cfg.Model,
		State: stateJSON,
		Questions: map[string]Question{
			"action": {
				Type:         "choice",
				Instructions: "Determine immediate intraday order action based on technical momentum, L2 order book balance, and VWAP position.",
				Criteria: map[string]string{
					"BUY":  "Strong bullish breakout or pullback bounce with aggressive bid volume",
					"SELL": "Strong bearish breakdown or rejection with aggressive ask volume",
					"HOLD": "Choppy range, low volume, or indecisive price action",
					"EXIT": "Exhaustion move or momentum breakdown from an existing open position",
				},
			},
			"breakout_prob": {
				Type:         "noul",
				Instructions: "Will price sustain momentum in the signal direction for at least the next 3 bars without hitting the stop loss?",
			},
			"conviction": {
				Type:         "score",
				Instructions: "Rate the technical quality and risk-reward ratio of this setup from 1 (terrible) to 5 (flawless institutional setup).",
				Criteria: []string{
					"Poor risk/reward or high noise",
					"Mediocre setup with conflicting indicators",
					"Standard acceptable setup",
					"High-probability setup with multiple confirmations",
					"A+ institutional liquidity sweep or clean momentum breakout",
				},
			},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("jev: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("jev: create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(c.cfg.APIKey); apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	httpReq.Header.Set("HTTP-Referer", "https://scalper.local")
	httpReq.Header.Set("X-Title", "Scalper Trading Terminal")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("jev: HTTP call failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("jev: read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody := strings.TrimSpace(string(body))
		if len(errBody) > 300 {
			errBody = errBody[:300] + "... (truncated)"
		}
		return EvaluationResult{}, fmt.Errorf("jev: API returned HTTP %d: %s", resp.StatusCode, errBody)
	}

	var sysResp SystemOneResponse
	if err := json.Unmarshal(body, &sysResp); err != nil {
		return EvaluationResult{}, fmt.Errorf("jev: unmarshal response: %w", err)
	}

	if sysResp.Error != nil {
		return EvaluationResult{}, fmt.Errorf("jev: API error: %s", sysResp.Error.Message)
	}

	actionAns, ok := sysResp.Answers["action"]
	if !ok {
		return EvaluationResult{}, errors.New("jev: missing action answer")
	}

	var actionStr string
	if actionAns.Choice != nil && strings.TrimSpace(*actionAns.Choice) != "" {
		actionStr = *actionAns.Choice
	} else if actionAns.Value != nil && strings.TrimSpace(*actionAns.Value) != "" {
		actionStr = *actionAns.Value
	}
	if actionStr == "" {
		return EvaluationResult{}, errors.New("jev: missing action choice answer")
	}

	confAns, ok := sysResp.Answers["breakout_prob"]
	var confidence float64
	if ok {
		if confAns.Noul != nil {
			confidence = confAns.Noul.Float64()
		} else if confAns.Probability != nil {
			confidence = confAns.Probability.Float64()
		} else if confAns.Confidence != nil {
			confidence = confAns.Confidence.Float64()
		}
	}
	if confidence == 0 && actionAns.Confidence != nil {
		confidence = actionAns.Confidence.Float64()
	}

	scoreAns, ok := sysResp.Answers["conviction"]
	var score int
	if ok && scoreAns.Score != nil {
		// Jev score answers are 0-based positions on the criteria scale
		// (0..len(criteria)-1). For five criteria the raw value is 0..4.
		score = jevConvictionScore(scoreAns.Score.Float64())
	} else if ok && scoreAns.Probability != nil {
		score = jevProbabilityScore(scoreAns.Probability.Float64())
	} else if ok && scoreAns.Confidence != nil {
		score = jevProbabilityScore(scoreAns.Confidence.Float64())
	}

	if score < 1 {
		score = 1
	} else if score > 5 {
		score = 5
	}

	duration := int(time.Since(start).Milliseconds())

	return EvaluationResult{
		Action:     strings.ToUpper(strings.TrimSpace(actionStr)),
		Confidence: confidence,
		Score:      score,
		DurationMS: duration,
	}, nil
}

func normalizeOpenRouterModel(model string) string {
	m := strings.TrimSpace(model)
	if strings.HasPrefix(m, "~") {
		return m
	}
	if strings.HasPrefix(m, "typesafe/") {
		if strings.TrimPrefix(m, "typesafe/") == "jev-latest" {
			return "~typesafe/jev-latest"
		}
		return m
	}
	if m == "jev-latest" {
		return "~typesafe/jev-latest"
	}
	return "typesafe/" + m
}

func jevConvictionScore(raw float64) int {
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return 1
	}
	return clampScore(int(math.Round(raw)) + 1)
}

func jevProbabilityScore(raw float64) int {
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return 1
	}
	if raw > 1.0 && raw <= 100.0 {
		raw = raw / 100.0
	}
	return clampScore(int(math.Round(raw*4.0)) + 1)
}

func clampScore(score int) int {
	if score < 1 {
		return 1
	}
	if score > 5 {
		return 5
	}
	return score
}
