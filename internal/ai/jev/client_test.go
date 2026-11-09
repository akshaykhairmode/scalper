package jev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJevEvaluateSetupSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization header = %q, want Bearer test-key", r.Header.Get("Authorization"))
		}

		var req SystemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if req.State == "" {
			t.Error("state was empty")
		}

		choice := "BUY"
		noul := FlexFloat(0.84)
		score := FlexFloat(4)

		resp := SystemOneResponse{
			Answers: map[string]Answer{
				"action":        {Choice: &choice},
				"breakout_prob": {Noul: &noul},
				"conviction":    {Score: &score},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := NewClient(Config{
		BaseURL: ts.URL,
		APIKey:  "test-key",
		Timeout: 2 * time.Second,
	})

	res, err := client.EvaluateSetup(context.Background(), `{"spot": 24520, "vwap": 24500}`)
	if err != nil {
		t.Fatalf("EvaluateSetup failed: %v", err)
	}

	if res.Action != "BUY" {
		t.Errorf("Action = %q, want BUY", res.Action)
	}
	if res.Confidence != 0.84 {
		t.Errorf("Confidence = %f, want 0.84", res.Confidence)
	}
	// Jev conviction scores are 0-based positions on the criteria scale, so
	// raw 4 (the fifth criterion of five) maps to 5/5.
	if res.Score != 5 {
		t.Errorf("Score = %d, want 5", res.Score)
	}
	if res.DurationMS < 0 {
		t.Errorf("DurationMS = %d, want >= 0", res.DurationMS)
	}
}

func TestJevEvaluateSetupError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": {"message": "invalid api key"}}`))
	}))
	defer ts.Close()

	client := NewClient(Config{
		BaseURL: ts.URL,
		APIKey:  "bad-key",
	})

	_, err := client.EvaluateSetup(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected error on HTTP 401, got nil")
	}
}

func TestJevOpenRouterResolution(t *testing.T) {
	client := NewClient(Config{
		BaseURL: "https://openrouter.ai/api/v1",
		APIKey:  "test-openrouter-key",
	})

	if client.endpoint != "https://openrouter.ai/api/alpha/decisions" {
		t.Errorf("endpoint = %q, want https://openrouter.ai/api/alpha/decisions", client.endpoint)
	}
	if client.cfg.Model != "~typesafe/jev-latest" {
		t.Errorf("model = %q, want ~typesafe/jev-latest", client.cfg.Model)
	}

	// Auto-redirect to OpenRouter when OpenRouter key is provided even if BaseURL was left as TypeSafe default
	skClient := NewClient(Config{
		BaseURL: "https://api.typesafe.ai/v1",
		APIKey:  "sk-or-v1-abcdef123456",
	})
	if skClient.endpoint != "https://openrouter.ai/api/alpha/decisions" {
		t.Errorf("endpoint = %q, want https://openrouter.ai/api/alpha/decisions", skClient.endpoint)
	}
	if skClient.cfg.Model != "~typesafe/jev-latest" {
		t.Errorf("model = %q, want ~typesafe/jev-latest", skClient.cfg.Model)
	}
}

func TestNilJevClientReturnsError(t *testing.T) {
	var c *Client
	if _, err := c.EvaluateSetup(context.Background(), `{}`); err == nil {
		t.Fatal("expected error on nil client EvaluateSetup, got nil")
	}
}

func TestJevOpenRouterDecisionsResponseParsing(t *testing.T) {
	// Raw JSON returned by OpenRouter typesafe/jev-1.13 decisions API with continuous float score 0.09
	rawOpenRouterJSON := `{
		"model": "typesafe/jev-1.13",
		"answers": {
			"action": {
				"type": "choice",
				"choice": "HOLD",
				"confidence": 0.85
			},
			"breakout_prob": {
				"type": "noul",
				"probability": 0.35
			},
			"conviction": {
				"type": "score",
				"score": 0.09
			}
		}
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rawOpenRouterJSON))
	}))
	defer ts.Close()

	client := NewClient(Config{
		BaseURL: ts.URL,
		APIKey:  "test-openrouter-key",
		Model:   "typesafe/jev-1.13",
	})

	res, err := client.EvaluateSetup(context.Background(), `{"spot": 25400}`)
	if err != nil {
		t.Fatalf("unexpected error parsing OpenRouter decisions response: %v", err)
	}

	if res.Action != "HOLD" {
		t.Errorf("Action = %q, want HOLD", res.Action)
	}
	if res.Confidence != 0.35 {
		t.Errorf("Confidence = %f, want 0.35", res.Confidence)
	}
	// 0.09 is on 0..1 scale, mapped to 1..5 scale -> 1
	if res.Score != 1 {
		t.Errorf("Score = %d, want 1", res.Score)
	}
}

func TestJevConvictionScoreMapping(t *testing.T) {
	cases := []struct {
		raw  float64
		want int
	}{
		{0, 1},
		{1, 2},
		{2, 3},
		{3, 4},
		{4, 5},
		{9, 5},
		{-3, 1},
	}
	for _, tc := range cases {
		if got := jevConvictionScore(tc.raw); got != tc.want {
			t.Errorf("jevConvictionScore(%v) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestJevProbabilityScoreMapping(t *testing.T) {
	cases := []struct {
		raw  float64
		want int
	}{
		{0.0, 1},
		{0.25, 2},
		{0.5, 3},
		{0.75, 4},
		{1.0, 5},
		{84, 4},
		{50, 3},
		{1.5, 1},
	}
	for _, tc := range cases {
		if got := jevProbabilityScore(tc.raw); got != tc.want {
			t.Errorf("jevProbabilityScore(%v) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestNormalizeOpenRouterModel(t *testing.T) {
	cases := map[string]string{
		"jev-latest":           "~typesafe/jev-latest",
		"typesafe/jev-latest":  "~typesafe/jev-latest",
		"~typesafe/jev-latest": "~typesafe/jev-latest",
		"jev-1.13":             "typesafe/jev-1.13",
	}
	for in, want := range cases {
		if got := normalizeOpenRouterModel(in); got != want {
			t.Errorf("normalizeOpenRouterModel(%q) = %q, want %q", in, got, want)
		}
	}
}
