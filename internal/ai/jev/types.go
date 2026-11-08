package jev

import "time"

// Config holds connection settings for TypeSafe AI Jev decision models.
type Config struct {
	BaseURL string        `json:"base_url"` // default: https://api.typesafe.ai/v1
	APIKey  string        `json:"api_key"`
	Model   string        `json:"model"` // default: jev-latest
	Timeout time.Duration `json:"timeout"`
}

// Question defines a schema-constrained query for the Jev model.
type Question struct {
	Type         string `json:"type"` // "choice", "noul", "score"
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// SystemOneRequest is the payload sent to /v1/system_one.
type SystemOneRequest struct {
	State     string              `json:"state"`
	Questions map[string]Question `json:"questions"`
	Model     string              `json:"model,omitempty"`
}

// Answer is a typed answer returned by Jev for a specific question.
type Answer struct {
	Choice *string  `json:"choice,omitempty"`
	Noul   *float64 `json:"noul,omitempty"`
	Score  *int     `json:"score,omitempty"`
}

// SystemOneResponse is the response returned from TypeSafe AI.
type SystemOneResponse struct {
	Answers map[string]Answer `json:"answers"`
	Error   *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// EvaluationResult contains the parsed tactical signals from Jev.
type EvaluationResult struct {
	Action     string  `json:"action"`     // BUY, SELL, HOLD, EXIT
	Confidence float64 `json:"confidence"` // 0.0 to 1.0 probability of success
	Score      int     `json:"score"`      // Conviction score 1 to 5
	DurationMS int     `json:"duration_ms"`
}
