package scrip

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/errs"
	"github.com/akshaykhairmode/scalper/internal/kotakhttp"
)

const (
	// FilePathsPath is the Scrip Master listing endpoint, relative to the
	// dynamic base URL returned by TOTP validation.
	FilePathsPath = "/script-details/1.0/masterscrip/file-paths"

	maxListingBytes  = 1 << 20  // 1 MiB
	maxCSVBytes      = 64 << 20 // 64 MiB; nse_fo is ~17 MiB
	defaultHTTPLimit = 30 * time.Second
)

// HTTPDoer is the subset of http.Client used here, so tests can inject a fake.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client fetches the Scrip Master listing and CSV files.
type Client struct {
	HTTP        HTTPDoer
	ConsumerKey string
}

// NewClient builds a client with a bounded timeout. An empty consumer key
// fails closed: the listing endpoint requires it.
func NewClient(consumerKey string) *Client {
	return &Client{
		HTTP:        kotakhttp.NewClient(defaultHTTPLimit),
		ConsumerKey: strings.TrimSpace(consumerKey),
	}
}

// FileList is the decoded file-paths response.
type FileList struct {
	Paths      []string
	BaseFolder string
}

// ListFiles calls the file-paths endpoint and returns every CSV URL.
func (c *Client) ListFiles(ctx context.Context, baseURL string) (FileList, error) {
	if c == nil || c.HTTP == nil {
		return FileList{}, fmt.Errorf("scrip: http client is not configured")
	}
	if c.ConsumerKey == "" {
		return FileList{}, fmt.Errorf("scrip: consumer key is required: %w", errs.ErrNotLoggedIn)
	}
	endpoint, err := joinURL(baseURL, FilePathsPath)
	if err != nil {
		return FileList{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return FileList{}, fmt.Errorf("scrip: build file-paths request: %w", err)
	}
	req.Header.Set("Authorization", c.ConsumerKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return FileList{}, fmt.Errorf("scrip: file-paths request: %w", err)
	}
	defer func() {
		// The body is drained below; a close error is not actionable.
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Debug("failed to close scrip response body", "error", cerr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			slog.Debug("failed to read error response body", "error", readErr)
		}
		return FileList{}, &errs.BrokerError{
			Op:         "scrip file-paths",
			StatusCode: resp.StatusCode,
			Message:    sanitizeMessage(body),
			Retryable:  retryableStatus(resp.StatusCode),
			Raw:        body,
		}
	}

	var payload struct {
		Data struct {
			FilesPaths []string `json:"filesPaths"`
			BaseFolder string   `json:"baseFolder"`
		} `json:"data"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxListingBytes))
	if err := dec.Decode(&payload); err != nil {
		return FileList{}, fmt.Errorf("scrip: decode file-paths response: %w", err)
	}
	if len(payload.Data.FilesPaths) == 0 {
		return FileList{}, fmt.Errorf("scrip: file-paths response contained no files")
	}
	for _, p := range payload.Data.FilesPaths {
		if _, err := url.ParseRequestURI(p); err != nil {
			return FileList{}, fmt.Errorf("scrip: invalid file path %q: %w", p, err)
		}
	}
	return FileList{Paths: payload.Data.FilesPaths, BaseFolder: payload.Data.BaseFolder}, nil
}

// Download fetches one CSV. The consumer key is deliberately not forwarded:
// these are broker-provided download URLs and may point at a CDN.
func (c *Client) Download(ctx context.Context, fileURL string) ([]byte, error) {
	if c == nil || c.HTTP == nil {
		return nil, fmt.Errorf("scrip: http client is not configured")
	}
	u, err := url.Parse(fileURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("scrip: invalid CSV URL %q", fileURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("scrip: build CSV request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scrip: download %s: %w", fileURL, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Debug("failed to close scrip CSV download response body", "error", cerr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			slog.Debug("failed to read scrip CSV download error body", "error", readErr)
		}
		return nil, &errs.BrokerError{
			Op:         "scrip download",
			StatusCode: resp.StatusCode,
			Message:    sanitizeMessage(body),
			Retryable:  retryableStatus(resp.StatusCode),
			Raw:        body,
		}
	}
	limited := io.LimitReader(resp.Body, maxCSVBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("scrip: read %s: %w", fileURL, err)
	}
	if len(data) > maxCSVBytes {
		return nil, fmt.Errorf("scrip: %s exceeds the %d byte limit", fileURL, maxCSVBytes)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("scrip: %s is empty", fileURL)
	}
	return data, nil
}

func joinURL(baseURL, path string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("scrip: base URL is empty")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("scrip: parse base URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("scrip: base URL %q must be an absolute http(s) URL", baseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func sanitizeMessage(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 512 {
		s = s[:512] + "…"
	}
	return s
}

// SegmentForURL derives the exchange segment from a Scrip Master file name.
// The listing uses both plain names (nse_fo.csv) and transformed ones
// (nse_cm-v1.csv); the older listing also used nse_com.csv for cash.
func SegmentForURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	name := strings.ToLower(u.Path)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, ".csv")
	name = strings.TrimSuffix(name, "-v1")
	switch {
	case strings.HasPrefix(name, "nse_fo"):
		return SegmentNSEFO, true
	case strings.HasPrefix(name, "bse_fo"):
		return SegmentBSEFO, true
	case strings.HasPrefix(name, "nse_cm"), strings.HasPrefix(name, "nse_com"):
		return SegmentNSECM, true
	case strings.HasPrefix(name, "bse_cm"):
		return SegmentBSECM, true
	case strings.HasPrefix(name, "mcx_fo"):
		return SegmentMCXFO, true
	default:
		return "", false
	}
}

// SelectFiles picks the best URL per required segment. Exact and -v1 names
// outrank the legacy nse_com fallback; the first URL wins ties.
func SelectFiles(paths []string, required []string) (map[string]string, error) {
	want := make(map[string]bool, len(required))
	for _, s := range required {
		want[s] = true
	}
	type choice struct {
		url   string
		score int
	}
	best := make(map[string]choice, len(required))
	for _, p := range paths {
		segment, ok := SegmentForURL(p)
		if !ok || !want[segment] {
			continue
		}
		score := fileScore(p, segment)
		if prev, ok := best[segment]; !ok || score > prev.score {
			best[segment] = choice{url: p, score: score}
		}
	}
	out := make(map[string]string, len(best))
	var missing []string
	for _, segment := range required {
		c, ok := best[segment]
		if !ok {
			missing = append(missing, segment)
			continue
		}
		out[segment] = c.url
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("scrip: listing is missing segments %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func fileScore(raw, segment string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	name := strings.ToLower(u.Path)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case segment + ".csv":
		return 3
	case segment + "-v1.csv":
		return 2
	default:
		return 1
	}
}
