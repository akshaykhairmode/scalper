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

	"github.com/akshaykhairmode/scalper/internal/kotakhttp"
	"github.com/akshaykhairmode/scalper/internal/logging"
)

const kotakLoginURL = "https://mis.kotaksecurities.com"

var (
	// ErrMissingConsumerKey indicates that Kotak authentication cannot proceed.
	ErrMissingConsumerKey = errors.New("KOTAK_CONSUMER_KEY is required to sign in")
	// ErrNoPendingLogin indicates that no active TOTP-authenticated flow exists.
	ErrNoPendingLogin = errors.New("TOTP login expired; start again")
)

// InputError represents invalid user-provided authentication input.
type InputError struct {
	Message string
}

func (e *InputError) Error() string { return e.Message }

// APIError represents an unsuccessful response from Kotak's login API.
type APIError struct {
	Step       string
	HTTPStatus int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Kotak %s request failed (HTTP %d)", e.Step, e.HTTPStatus)
}

// ViewSession contains temporary credentials returned after TOTP validation.
type ViewSession struct {
	Token string
	SID   string
}

// TradeSession contains credentials returned after MPIN validation.
type TradeSession struct {
	Token   string
	SID     string
	BaseURL string
	FeedURL string
}

// KotakAuthenticator exchanges TOTP and MPIN values for Kotak sessions.
type KotakAuthenticator interface {
	LoginTOTP(context.Context, string) (ViewSession, error)
	ValidateMPIN(context.Context, ViewSession, string) (TradeSession, error)
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type kotakClient struct {
	log         *slog.Logger
	consumerKey string
	mobile      string
	ucc         string
	neoFinKey   string
	loginURL    string
	http        httpDoer
}

// NewKotakAuthenticatorWithLogger creates a Kotak authenticator with a logger.
func NewKotakAuthenticatorWithLogger(log *slog.Logger, consumerKey, mobile, ucc, neoFinKey string) KotakAuthenticator {
	if log == nil {
		log = slog.Default()
	}
	client := kotakhttp.NewClient(15 * time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &kotakClient{
		log:         log,
		consumerKey: strings.TrimSpace(consumerKey),
		mobile:      mobile,
		ucc:         ucc,
		neoFinKey:   neoFinKey,
		loginURL:    kotakLoginURL,
		http:        client,
	}
}

func (c *kotakClient) LoginTOTP(ctx context.Context, totp string) (ViewSession, error) {
	if c.consumerKey == "" {
		return ViewSession{}, ErrMissingConsumerKey
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
		return ViewSession{}, errors.New("kotak TOTP login response is missing its view session")
	}
	return ViewSession{Token: data.Token, SID: data.SID}, nil
}

func (c *kotakClient) ValidateMPIN(ctx context.Context, view ViewSession, mpin string) (TradeSession, error) {
	if c.consumerKey == "" {
		return TradeSession{}, ErrMissingConsumerKey
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
		return TradeSession{}, errors.New("kotak MPIN response is missing its trade session")
	}
	feedURL := data.FeedURL
	if dataCenterURL, err := c.resolveFeedURL(ctx, data.DataCenter); err != nil {
		c.log.Warn("Kotak SFeed endpoint lookup failed; using session/default endpoint",
			"event", "sfeed_endpoint_lookup_failed", "error", err)
	} else if dataCenterURL != "" {
		validatedURL, err := validateFeedURL(dataCenterURL)
		if err != nil {
			c.log.Warn("Kotak SFeed endpoint lookup returned an invalid URL; using session/default endpoint",
				"event", "sfeed_endpoint_invalid", "error", err)
		} else {
			feedURL = validatedURL
		}
	}
	if feedURL != "" {
		feedURL, err = validateFeedURL(feedURL)
		if err != nil {
			return TradeSession{}, err
		}
	}
	return TradeSession{Token: data.Token, SID: data.SID, BaseURL: baseURL, FeedURL: feedURL}, nil
}

type loginResponse struct {
	Token      string `json:"token"`
	SID        string `json:"sid"`
	BaseURL    string `json:"baseUrl"`
	Status     string `json:"status"`
	DataCenter string `json:"dataCenter"`
	FeedURL    string `json:"feedUrl"`
	Data       struct {
		Token      string `json:"token"`
		SID        string `json:"sid"`
		BaseURL    string `json:"baseUrl"`
		Status     string `json:"status"`
		DataCenter string `json:"dataCenter"`
		FeedURL    string `json:"feedUrl"`
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
	req.Header.Set("Authorization", c.consumerKey)
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
	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.Warn("close Kotak response body", "event", "auth_response_close_failed", "error", err)
		}
	}()
	const maxResponseSize = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		c.logAPIExchange(req, resp, nil, started, err)
		return loginResponse{}, fmt.Errorf("read Kotak %s response: %w", step, err)
	}
	if len(raw) > maxResponseSize {
		c.logAPIExchange(req, resp, nil, started, errors.New("response exceeds size limit"))
		return loginResponse{}, fmt.Errorf("kotak %s response exceeds size limit", step)
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
	if result.Data.DataCenter != "" {
		result.DataCenter = result.Data.DataCenter
	}
	if result.Data.FeedURL != "" {
		result.FeedURL = result.Data.FeedURL
	}
	return result, nil
}

const (
	sfeedConfigURL  = "https://lapi.kotaksecurities.com/5config/config"
	sfeedSDKVersion = "3.0.13"
)

func (c *kotakClient) resolveFeedURL(ctx context.Context, dataCenter string) (string, error) {
	dataCenter = strings.TrimSpace(dataCenter)
	if dataCenter == "" {
		return "", nil
	}
	endpoint, err := url.Parse(sfeedConfigURL)
	if err != nil {
		return "", fmt.Errorf("parse Kotak SFeed config URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("appVersion", sfeedSDKVersion)
	query.Set("platform", "api")
	query.Set("environment", "prod")
	endpoint.RawQuery = query.Encode()
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create Kotak SFeed config request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request Kotak SFeed config: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.Warn("close Kotak SFeed config response body", "event", "sfeed_config_close_failed", "error", err)
		}
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("kotak SFeed config request failed (HTTP %d)", resp.StatusCode)
	}
	const maxConfigBytes = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigBytes+1))
	if err != nil {
		return "", fmt.Errorf("read Kotak SFeed config response: %w", err)
	}
	if len(raw) > maxConfigBytes {
		return "", errors.New("kotak SFeed config response exceeds size limit")
	}
	var response struct {
		Data struct {
			Configs map[string]string `json:"configs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("decode Kotak SFeed config response: %w", err)
	}
	configs := response.Data.Configs
	source := configs[dataCenter+"_broadcast_source"]
	if source == "" {
		return "", nil
	}
	return configs[dataCenter+"_"+source+"_broadcast_endpoint"], nil
}

func (c *kotakClient) logAPIExchange(req *http.Request, resp *http.Response, responseBody []byte, started time.Time, err error) {
	var requestBody []byte
	if getBody := req.GetBody; getBody != nil {
		body, bodyErr := getBody()
		if bodyErr == nil {
			requestBody, bodyErr = io.ReadAll(body)
			if closeErr := body.Close(); closeErr != nil && c.log != nil {
				c.log.Debug("failed to close request body", "error", closeErr)
			}
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
		return "", errors.New("kotak MPIN response contains an invalid base URL")
	}
	host := strings.ToLower(base.Hostname())
	if host != "kotaksecurities.com" && !strings.HasSuffix(host, ".kotaksecurities.com") {
		return "", errors.New("kotak MPIN response contains an untrusted base URL")
	}
	return strings.TrimRight(base.String(), "/"), nil
}

func validateFeedURL(raw string) (string, error) {
	feed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || feed.Scheme != "wss" || feed.Host == "" || feed.User != nil ||
		feed.RawQuery != "" || feed.Fragment != "" {
		return "", errors.New("kotak MPIN response contains an invalid SFeed URL")
	}
	host := strings.ToLower(feed.Hostname())
	if host != "kotaksecurities.com" && !strings.HasSuffix(host, ".kotaksecurities.com") {
		return "", errors.New("kotak MPIN response contains an untrusted SFeed URL")
	}
	return strings.TrimRight(feed.String(), "/"), nil
}
