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
		noul := 0.84
		score := 4

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
	if res.Score != 4 {
		t.Errorf("Score = %d, want 4", res.Score)
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
	if client.cfg.Model != "typesafe/jev-latest" {
		t.Errorf("model = %q, want typesafe/jev-latest", client.cfg.Model)
	}

	// Auto-redirect to OpenRouter when OpenRouter key is provided even if BaseURL was left as TypeSafe default
	skClient := NewClient(Config{
		BaseURL: "https://api.typesafe.ai/v1",
		APIKey:  "sk-or-v1-abcdef123456",
	})
	if skClient.endpoint != "https://openrouter.ai/api/alpha/decisions" {
		t.Errorf("endpoint = %q, want https://openrouter.ai/api/alpha/decisions", skClient.endpoint)
	}
	if skClient.cfg.Model != "typesafe/jev-latest" {
		t.Errorf("model = %q, want typesafe/jev-latest", skClient.cfg.Model)
	}
}

func TestNilJevClientReturnsError(t *testing.T) {
	var c *Client
	if _, err := c.EvaluateSetup(context.Background(), `{}`); err == nil {
		t.Fatal("expected error on nil client EvaluateSetup, got nil")
	}
}
