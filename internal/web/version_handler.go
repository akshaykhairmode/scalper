package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/version"
)

type releaseInfo struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

type versionCheckResponse struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	HasUpdate      bool   `json:"has_update"`
	ReleaseURL     string `json:"release_url"`
	ReleaseName    string `json:"release_name"`
	PublishedAt    string `json:"published_at"`
}

type versionCache struct {
	mu      sync.Mutex
	cached  versionCheckResponse
	expires time.Time
}

var releaseCache versionCache

func (s *Server) handleVersionCheck(w http.ResponseWriter, r *http.Request) {
	releaseCache.mu.Lock()
	if time.Now().Before(releaseCache.expires) && releaseCache.cached.LatestVersion != "" {
		res := releaseCache.cached
		releaseCache.mu.Unlock()
		if err := writeJSON(w, http.StatusOK, res); err != nil {
			s.log.WarnContext(r.Context(), "version check write failed", "error", err)
		}
		return
	}
	releaseCache.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	current := version.Version
	res := versionCheckResponse{
		CurrentVersion: current,
		ReleaseURL:     "https://github.com/akshaykhairmode/scalper/releases/latest",
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/akshaykhairmode/scalper/releases/latest", nil)
	if err != nil {
		if writeErr := writeJSON(w, http.StatusOK, res); writeErr != nil {
			s.log.WarnContext(r.Context(), "version check write failed", "error", writeErr)
		}
		return
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "scalper/"+current)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		if writeErr := writeJSON(w, http.StatusOK, res); writeErr != nil {
			s.log.WarnContext(r.Context(), "version check write failed", "error", writeErr)
		}
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	var gh releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&gh); err != nil {
		if writeErr := writeJSON(w, http.StatusOK, res); writeErr != nil {
			s.log.WarnContext(r.Context(), "version check write failed", "error", writeErr)
		}
		return
	}

	latest := strings.TrimSpace(gh.TagName)
	res.LatestVersion = latest
	res.ReleaseName = gh.Name
	res.PublishedAt = gh.PublishedAt
	if gh.HTMLURL != "" {
		res.ReleaseURL = gh.HTMLURL
	}
	res.HasUpdate = isNewerSemver(latest, current)

	releaseCache.mu.Lock()
	releaseCache.cached = res
	releaseCache.expires = time.Now().Add(15 * time.Minute)
	releaseCache.mu.Unlock()

	if err := writeJSON(w, http.StatusOK, res); err != nil {
		s.log.WarnContext(r.Context(), "version check write failed", "error", err)
	}
}

func parseSemver(v string) []int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V"))
	if v == "" {
		return nil
	}
	if idx := strings.IndexAny(v, "-+"); idx != -1 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	ints := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		ints = append(ints, n)
	}
	return ints
}

func isNewerSemver(latest, current string) bool {
	if latest == "" {
		return false
	}
	if current == "" || current == "dev" || current == "unknown" {
		return false
	}
	l := parseSemver(latest)
	c := parseSemver(current)
	if len(l) == 0 || len(c) == 0 {
		return false
	}
	for len(l) < 3 {
		l = append(l, 0)
	}
	for len(c) < 3 {
		c = append(c, 0)
	}
	for i := 0; i < len(l) && i < len(c); i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}
