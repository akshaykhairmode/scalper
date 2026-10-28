package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"log/slog"

	"github.com/akshaykhairmode/scalper/internal/logging"
)

const kotakLoginURL = "https://mis.kotaksecurities.com"

var (
	ErrMissingAccessToken = errors.New("KOTAK_ACCESS_TOKEN is required to sign in")
	ErrNoPendingLogin     = errors.New("TOTP login expired; start again")
)

type InputError struct {
	Message string
}

func (e *InputError) Error() string { return e.Message }

type APIError struct {
	Step       string
	HTTPStatus int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Kotak %s request failed (HTTP %d)", e.Step, e.HTTPStatus)
}

type ViewSession struct {
	Token string
	SID   string
}

type TradeSession struct {
	Token   string
	SID     string
	BaseURL string
}

type KotakAuthenticator interface {
	LoginTOTP(context.Context, string) (ViewSession, error)
	ValidateMPIN(context.Context, ViewSession, string) (TradeSession, error)
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type kotakClient struct {
	log         *slog.Logger
	accessToken string
	mobile      string
	ucc         string
	neoFinKey   string
	loginURL    string
	http        httpDoer
}

func NewKotakAuthenticator(accessToken, mobile, ucc, neoFinKey string) KotakAuthenticator {
	return NewKotakAuthenticatorWithLogger(slog.Default(), accessToken, mobile, ucc, neoFinKey)
}

func NewKotakAuthenticatorWithLogger(log *slog.Logger, accessToken, mobile, ucc, neoFinKey string) KotakAuthenticator {
	if log == nil {
		log = slog.Default()
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &kotakClient{
		log:         log,
		accessToken: strings.TrimSpace(accessToken),
		mobile:      mobile,
		ucc:         ucc,
		neoFinKey:   neoFinKey,
		loginURL:    kotakLoginURL,
		http:        client,
	}
}

func (c *kotakClient) LoginTOTP(ctx context.Context, totp string) (ViewSession, error) {
	if c.accessToken == "" {
		return ViewSession{}, ErrMissingAccessToken
	}
	body := struct {
		MobileNumber string `json:"mobileNumber"`
		UCC          string `json:"ucc"`
		TOTP         string `json:"totp"`
	}{
		MobileNumber: c.mobile,
		UCC:          c.ucc,
		TOTP:         totp,
	}
	data, err := c.post(ctx, "/login/1.0/tradeApiLogin", body, "TOTP login")
	if err != nil {
		return ViewSession{}, err
	}
	if data.Token == "" || data.SID == "" {
		return ViewSession{}, errors.New("Kotak TOTP login response is missing its view session")
	}
	return ViewSession{Token: data.Token, SID: data.SID}, nil
}

func (c *kotakClient) ValidateMPIN(ctx context.Context, view ViewSession, mpin string) (TradeSession, error) {
	if c.accessToken == "" {
		return TradeSession{}, ErrMissingAccessToken
	}
	body := struct {
		MPIN string `json:"mpin"`
	}{MPIN: mpin}
	data, err := c.post(ctx, "/login/1.0/tradeApiValidate", body, "MPIN validation",
		func(req *http.Request) {
			req.Header.Set("sid", view.SID)
			req.Header.Set("Auth", view.Token)
		})
	if err != nil {
		return TradeSession{}, err
	}
	baseURL, err := validateBaseURL(data.BaseURL)
	if err != nil {
		return TradeSession{}, err
	}
	if data.Token == "" || data.SID == "" {
		return TradeSession{}, errors.New("Kotak MPIN response is missing its trade session")
	}
	return TradeSession{Token: data.Token, SID: data.SID, BaseURL: baseURL}, nil
}

type loginResponse struct {
	Token   string `json:"token"`
	SID     string `json:"sid"`
	BaseURL string `json:"baseUrl"`
	Status  string `json:"status"`
	Data    struct {
		Token   string `json:"token"`
		SID     string `json:"sid"`
		BaseURL string `json:"baseUrl"`
		Status  string `json:"status"`
	} `json:"data"`
}

func (c *kotakClient) post(ctx context.Context, path string, body any, step string, headers ...func(*http.Request)) (loginResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return loginResponse{}, fmt.Errorf("encode Kotak %s request: %w", step, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.loginURL+path, bytes.NewReader(payload))
	if err != nil {
		return loginResponse{}, fmt.Errorf("create Kotak %s request: %w", step, err)
	}
	req.Header.Set("Authorization", c.accessToken)
	req.Header.Set("neo-fin-key", c.neoFinKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for _, setHeader := range headers {
		setHeader(req)
	}

	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logAPIExchange(req, nil, nil, started, err)
		return loginResponse{}, fmt.Errorf("send Kotak %s request: %w", step, err)
	}
	defer resp.Body.Close()
	const maxResponseSize = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		c.logAPIExchange(req, resp, nil, started, err)
		return loginResponse{}, fmt.Errorf("read Kotak %s response: %w", step, err)
	}
	if len(raw) > maxResponseSize {
		c.logAPIExchange(req, resp, nil, started, errors.New("response exceeds size limit"))
		return loginResponse{}, fmt.Errorf("Kotak %s response exceeds size limit", step)
	}
	c.logAPIExchange(req, resp, raw, started, nil)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return loginResponse{}, &APIError{Step: step, HTTPStatus: resp.StatusCode}
	}
	var result loginResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return loginResponse{}, fmt.Errorf("decode Kotak %s response: %w", step, err)
	}

	status := result.Data.Status
	if status == "" {
		status = result.Status
	}
	if status != "" && !strings.EqualFold(status, "success") && !strings.EqualFold(status, "ok") {
		return loginResponse{}, &APIError{Step: step, HTTPStatus: http.StatusUnauthorized}
	}
	if result.Data.Token != "" {
		result.Token = result.Data.Token
	}
	if result.Data.SID != "" {
		result.SID = result.Data.SID
	}
	if result.Data.BaseURL != "" {
		result.BaseURL = result.Data.BaseURL
	}
	return result, nil
}

func (c *kotakClient) logAPIExchange(req *http.Request, resp *http.Response, responseBody []byte, started time.Time, err error) {
	var requestBody []byte
	if getBody := req.GetBody; getBody != nil {
		body, bodyErr := getBody()
		if bodyErr == nil {
			requestBody, bodyErr = io.ReadAll(body)
			_ = body.Close()
		}
		if bodyErr != nil {
			requestBody = []byte("[request body unavailable]")
		}
	} else {
		requestBody = []byte("[request body unavailable]")
	}
	safeRequestBody := redactedJSONForLog(requestBody)
	log := c.log
	if log == nil {
		log = slog.Default()
	}
	args := []any{
		"event", "kotak_api_exchange",
		"method", req.Method,
		"url", logging.RedactURL(req.URL.String()),
		"request_headers", logging.RedactHeaders(req.Header),
		"request_body", safeRequestBody,
		"duration_ms", time.Since(started).Milliseconds(),
	}
	if resp != nil {
		args = append(args,
			"status", resp.StatusCode,
			"response_headers", logging.RedactHeaders(resp.Header),
			"response_body", redactedJSONForLog(responseBody))
	}
	if err != nil {
		args = append(args, "error", err)
		log.Error("Kotak trade API exchange", args...)
		return
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		log.Error("Kotak trade API exchange", args...)
		return
	}
	log.Info("Kotak trade API exchange", args...)
}

func redactedJSONForLog(raw []byte) string {
	redacted, err := logging.RedactJSON(raw)
	if err != nil {
		return "[non-JSON body omitted]"
	}
	return string(redacted)
}

func validateBaseURL(raw string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil ||
		base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("Kotak MPIN response contains an invalid base URL")
	}
	host := strings.ToLower(base.Hostname())
	if host != "kotaksecurities.com" && !strings.HasSuffix(host, ".kotaksecurities.com") {
		return "", errors.New("Kotak MPIN response contains an untrusted base URL")
	}
	return strings.TrimRight(base.String(), "/"), nil
}
