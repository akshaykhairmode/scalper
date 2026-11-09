package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAgentGeneratePlaybook(t *testing.T) {
	mockResponse := `{
		"bias": "BULLISH",
		"regime": "TRENDING",
		"key_support": 24450.0,
		"key_resistance": 24650.0,
		"max_pain": 24500.0,
		"pcr": 1.15,
		"narrative": "FII buying continues. Look for long setups above 24,520.",
		"focus_strikes": ["24500 CE"],
		"jev_criteria": {
			"min_score": 4,
			"min_confidence": 0.82,
			"action_allowed": "BUY_ONLY",
			"volatility_risk": "NORMAL"
		}
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("Authorization header missing or invalid: %q", r.Header.Get("Authorization"))
		}
		var req openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("model = %q, want test-model", req.Model)
		}

		resp := openAIChatResponse{
			Choices: []openAIChatChoice{
				{
					Message: openAIChatChoiceMessage{
						Content: "```json\n" + mockResponse + "\n```",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := NewClient(Config{
		Provider: "openrouter",
		BaseURL:  ts.URL,
		APIKey:   "test-api-key",
		Model:    "test-model",
		Timeout:  5 * time.Second,
	})

	ctx := context.Background()
	pb, err := client.GeneratePlaybook(ctx, MarketContext{
		Date:      "2026-11-09",
		Index:     "NIFTY",
		SpotPrice: 24520.0,
		PCR:       1.15,
	})
	if err != nil {
		t.Fatalf("GeneratePlaybook failed: %v", err)
	}

	if pb.Bias != "BULLISH" {
		t.Errorf("Bias = %q, want BULLISH", pb.Bias)
	}
	if pb.KeySupport != 24450.0 {
		t.Errorf("KeySupport = %f, want 24450.0", pb.KeySupport)
	}
	if pb.Source != "openrouter" {
		t.Errorf("Source = %q, want openrouter", pb.Source)
	}
}

func TestAgentChat(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := openAIChatResponse{
			Choices: []openAIChatChoice{
				{
					Message: openAIChatChoiceMessage{
						Content: "Nifty resistance is at 24,600.",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := NewClient(Config{
		Provider: "ollama",
		BaseURL:  ts.URL,
		Model:    "llama3",
	})

	res, err := client.Chat(context.Background(), "Where is resistance?")
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if res != "Nifty resistance is at 24,600." {
		t.Errorf("unexpected chat response: %q", res)
	}
}

func TestDefaultEndpointResolution(t *testing.T) {
	tests := []struct {
		provider     string
		wantEndpoint string
	}{
		{"ollama", "http://localhost:11434/v1/chat/completions"},
		{"openrouter", "https://openrouter.ai/api/v1/chat/completions"},
		{"gemini", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"openai", "https://api.openai.com/v1/chat/completions"},
	}

	for _, tt := range tests {
		c := NewClient(Config{Provider: tt.provider})
		if c.endpoint != tt.wantEndpoint {
			t.Errorf("provider %s endpoint = %q, want %q", tt.provider, c.endpoint, tt.wantEndpoint)
		}
	}
}

func TestCleanJSONBlock(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain_json",
			input: `{"bias": "BULLISH"}`,
			want:  `{"bias": "BULLISH"}`,
		},
		{
			name:  "markdown_fenced",
			input: "```json\n{\"bias\": \"BULLISH\"}\n```",
			want:  `{"bias": "BULLISH"}`,
		},
		{
			name:  "preamble_and_fence",
			input: "Here is your playbook:\n```json\n{\"bias\": \"BEARISH\"}\n```\nHope it helps!",
			want:  `{"bias": "BEARISH"}`,
		},
		{
			name:  "preamble_no_fence",
			input: "Analysis result: {\"bias\": \"NEUTRAL\"}",
			want:  `{"bias": "NEUTRAL"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanJSONBlock(tc.input)
			if got != tc.want {
				t.Errorf("cleanJSONBlock(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestAgentChatReasoningFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := openAIChatResponse{
			Choices: []openAIChatChoice{
				{
					Message: openAIChatChoiceMessage{
						Content:          "", // content empty, reasoning has answer
						ReasoningContent: `{"action": "BUY", "score": 4, "confidence": 0.88, "reasoning": "Strong trend"}`,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := NewClient(Config{
		Provider: "openrouter",
		BaseURL:  ts.URL,
		APIKey:   "test-key",
	})

	dec, err := client.EvaluateContract(context.Background(), ContractSetup{
		Underlying: "NIFTY",
		SpotPrice:  25400,
		OptionType: "CE",
		Strike:     "25400",
		OptionLTP:  150,
	})
	if err != nil {
		t.Fatalf("EvaluateContract failed: %v", err)
	}
	if dec.Action != "BUY" {
		t.Errorf("Action = %q, want BUY", dec.Action)
	}
	if dec.Score != 4 {
		t.Errorf("Score = %d, want 4", dec.Score)
	}
}

func TestNilClientReturnsError(t *testing.T) {
	var c *Client
	ctx := context.Background()
	if _, err := c.GeneratePlaybook(ctx, MarketContext{}); err == nil {
		t.Error("expected error on nil client GeneratePlaybook, got nil")
	}
	if _, err := c.Chat(ctx, "hello"); err == nil {
		t.Error("expected error on nil client Chat, got nil")
	}
}

func TestParseCandidateDecisions(t *testing.T) {
	// Object with a decisions array.
	obj := `{"decisions":[{"instrument_key":"NIFTY24500CE","action":"BUY","score":4,"confidence":80,"reasoning":"ok"}],"market_note":"bullish"}`
	got, err := parseCandidateDecisions(obj)
	if err != nil {
		t.Fatalf("parse object: %v", err)
	}
	if len(got) != 1 || got[0].Action != "BUY" {
		t.Fatalf("unexpected decisions: %+v", got)
	}
	if got[0].Confidence != 0.80 {
		t.Errorf("confidence = %v, want 0.80 (percent normalized)", got[0].Confidence)
	}

	// Bare JSON array.
	arr := `[{"instrument_key":"NIFTY24500PE","action":"LONG","score":9,"confidence":1.4}]`
	got, err = parseCandidateDecisions(arr)
	if err != nil {
		t.Fatalf("parse array: %v", err)
	}
	if len(got) != 1 || got[0].Action != "BUY" {
		t.Fatalf("unexpected array decisions: %+v", got)
	}
	if got[0].Score != 5 {
		t.Errorf("score = %d, want clamped 5", got[0].Score)
	}
	if got[0].Confidence != 1.0 {
		t.Errorf("confidence = %v, want clamped 1.0", got[0].Confidence)
	}

	// Single decision object.
	single := `{"action":"PASS","score":2,"confidence":0.3,"reasoning":"no edge"}`
	got, err = parseCandidateDecisions(single)
	if err != nil {
		t.Fatalf("parse single: %v", err)
	}
	if len(got) != 1 || got[0].Action != "PASS" {
		t.Fatalf("unexpected single decision: %+v", got)
	}
}

func TestParseCandidateDecisionsEmptyDecisions(t *testing.T) {
	got, err := parseCandidateDecisions(`{"decisions":[],"market_note":"no edge"}`)
	if err != nil {
		t.Fatalf("empty decisions should parse without error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no decisions, got %+v", got)
	}
}

func TestParseCandidateDecisionsFromProse(t *testing.T) {
	raw := "Here's a thinking process:\n1. Analyze the candidates.\n2. Check the spread.\n\nFinal answer:\n```json\n" +
		`{"decisions":[{"instrument_key":"NIFTY24500CE","option_type":"CE","action":"BUY","score":4,"confidence":0.8,"reasoning":"breakout"}]}` +
		"\n```\nThat is the plan."
	got, err := parseCandidateDecisions(raw)
	if err != nil {
		t.Fatalf("prose-wrapped JSON should parse: %v", err)
	}
	if len(got) != 1 || got[0].Action != "BUY" || got[0].InstrumentKey != "NIFTY24500CE" {
		t.Fatalf("unexpected decisions: %+v", got)
	}
}

func TestParseCandidateDecisionsFindsAnswerAmongMultipleJSONValues(t *testing.T) {
	raw := `context {"note":"ignore me"} then the answer ` +
		`{"decisions":[{"instrument_key":"NIFTY24500PE","action":"PASS","score":2,"confidence":0.4}]}` +
		` end`
	got, err := parseCandidateDecisions(raw)
	if err != nil {
		t.Fatalf("nested JSON search failed: %v", err)
	}
	if len(got) != 1 || got[0].Action != "PASS" {
		t.Fatalf("unexpected decisions: %+v", got)
	}
}

func TestParseCandidateDecisionsRejectsTruncatedThinking(t *testing.T) {
	_, err := parseCandidateDecisions("Here's a thinking process:\n1. Analyze the user input.\n2. Consider the spot price.")
	if err == nil {
		t.Fatal("expected an error for a response with no JSON")
	}
}

func TestEvaluateCandidatesDisablesReasoningForOpenRouterFree(t *testing.T) {
	var requests []openAIChatRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openAIChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		requests = append(requests, req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"content": `{"decisions":[{"instrument_key":"NIFTY24500CE","option_type":"CE","action":"BUY","score":4,"confidence":0.8}]}`,
			}}},
		})
	}))
	defer ts.Close()

	client := NewClient(Config{
		Provider: "openrouter",
		BaseURL:  ts.URL,
		Model:    "nvidia/nemotron-3.5-lightning:free",
		Timeout:  time.Second,
	})

	_, err := client.EvaluateCandidates(context.Background(), CandidateRequest{
		Underlying: "NIFTY",
		Candidates: []CandidateContract{{
			InstrumentKey: "NIFTY24500CE",
			OptionType:    "CE",
			Strike:        "24500",
			OptionLTP:     100,
		}},
	})
	if err != nil {
		t.Fatalf("EvaluateCandidates: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if requests[0].Format != nil {
		t.Error("free model should not receive response_format")
	}
	if requests[0].Reasoning == nil {
		t.Fatal("expected reasoning control to be sent")
	}
	if requests[0].Reasoning.Effort != "none" || !requests[0].Reasoning.Exclude {
		t.Fatalf("unexpected reasoning control: %+v", requests[0].Reasoning)
	}
}

func TestEvaluateCandidatesRetriesWithoutReasoning(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		var req openAIChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if count == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"reasoning effort is not supported for this model"}}`))
			return
		}
		if req.Reasoning != nil {
			t.Error("retry must omit the reasoning parameter")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"content": `{"decisions":[{"instrument_key":"NIFTY24500CE","action":"PASS","score":2,"confidence":0.4}]}`,
			}}},
		})
	}))
	defer ts.Close()

	client := NewClient(Config{
		Provider: "openrouter",
		BaseURL:  ts.URL,
		Model:    "nvidia/nemotron-3.5-lightning:free",
		Timeout:  time.Second,
	})

	decisions, err := client.EvaluateCandidates(context.Background(), CandidateRequest{
		Underlying: "NIFTY",
		Candidates: []CandidateContract{{InstrumentKey: "NIFTY24500CE", OptionType: "CE", Strike: "24500", OptionLTP: 100}},
	})
	if err != nil {
		t.Fatalf("EvaluateCandidates should retry without reasoning: %v", err)
	}
	if count != 2 {
		t.Fatalf("requests = %d, want 2", count)
	}
	if len(decisions) != 1 || decisions[0].Action != "PASS" {
		t.Fatalf("unexpected decisions: %+v", decisions)
	}
}
