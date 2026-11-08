// Package jev implements the TypeSafe AI System One decision model client.
package jev

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
		timeout = 1500 * time.Millisecond
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.typesafe.ai/v1"
	}

	endpoint := baseURL
	if !strings.HasSuffix(endpoint, "/system_one") {
		endpoint += "/system_one"
	}

	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "jev-latest"
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
	if !ok || actionAns.Choice == nil {
		return EvaluationResult{}, errors.New("jev: missing action choice answer")
	}

	confAns, ok := sysResp.Answers["breakout_prob"]
	var confidence float64
	if ok && confAns.Noul != nil {
		confidence = *confAns.Noul
	}

	scoreAns, ok := sysResp.Answers["conviction"]
	var score int
	if ok && scoreAns.Score != nil {
		score = *scoreAns.Score
	}

	duration := int(time.Since(start).Milliseconds())

	return EvaluationResult{
		Action:     strings.ToUpper(strings.TrimSpace(*actionAns.Choice)),
		Confidence: confidence,
		Score:      score,
		DurationMS: duration,
	}, nil
}
