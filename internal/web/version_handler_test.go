package web

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/version"
)

func TestParseSemver(t *testing.T) {
	cases := []struct {
		input string
		want  []int
	}{
		{"v1.2.3", []int{1, 2, 3}},
		{"1.2.3", []int{1, 2, 3}},
		{"v0.1.0-rc1", []int{0, 1, 0}},
		{"v2.0.0+build123", []int{2, 0, 0}},
		{"", nil},
		{"dev", nil},
		{"invalid", nil},
	}
	for _, tc := range cases {
		got := parseSemver(tc.input)
		if len(got) != len(tc.want) {
			t.Fatalf("parseSemver(%q) = %v, want %v", tc.input, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("parseSemver(%q)[%d] = %d, want %d", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestIsNewerSemver(t *testing.T) {
	cases := []struct {
		latest  string
		current string
		want    bool
	}{
		{"v1.1.0", "v1.0.0", true},
		{"v1.0.1", "v1.0.0", true},
		{"v2.0.0", "v1.9.9", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v1.1.0", false},
		{"v1.0.0", "v1.0", false},
		{"v1.0", "v1.0.0", false},
		{"v1.0.0", "dev", false},
		{"v1.0.0", "unknown", false},
		{"", "v1.0.0", false},
		{"v1.0.0", "", false},
	}
	for _, tc := range cases {
		got := isNewerSemver(tc.latest, tc.current)
		if got != tc.want {
			t.Errorf("isNewerSemver(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestHandleVersionCheck(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})

	// Prime the cache to verify fast response without hitting GitHub
	releaseCache.mu.Lock()
	releaseCache.cached = versionCheckResponse{
		CurrentVersion: version.Version,
		LatestVersion:  "v99.0.0",
		HasUpdate:      true,
		ReleaseURL:     "https://github.com/akshaykhairmode/scalper/releases/tag/v99.0.0",
		ReleaseName:    "v99.0.0",
	}
	releaseCache.expires = time.Now().Add(5 * time.Minute)
	releaseCache.mu.Unlock()

	rec := do(t, s, localRequest(http.MethodGet, "/api/version/check"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var res versionCheckResponse
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.HasUpdate {
		t.Errorf("expected has_update=true")
	}
	if res.LatestVersion != "v99.0.0" {
		t.Errorf("latest = %q, want v99.0.0", res.LatestVersion)
	}
}
