package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:                  "0 B",
		512:                "512 B",
		1024:               "1.0 KB",
		1536:               "1.5 KB",
		1024 * 1024:        "1.0 MB",
		1024 * 1024 * 1024: "1.0 GB",
	}
	for value, want := range cases {
		if got := humanBytes(value); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", value, got, want)
		}
	}
}

func TestStorageInfoHandler(t *testing.T) {
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()

	path := filepath.Join(t.TempDir(), "scalper.db")
	if err := os.WriteFile(path, make([]byte, 2048), 0o600); err != nil {
		t.Fatalf("write fake db: %v", err)
	}
	s.cfg.App.DBPath = path

	rec := do(t, s, localRequest(http.MethodGet, "/api/system/storage"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp storageInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode storage response: %v", err)
	}
	if !resp.OK || !resp.Exists {
		t.Fatalf("response = %+v", resp)
	}
	if resp.SizeBytes != 2048 {
		t.Errorf("size_bytes = %d, want 2048", resp.SizeBytes)
	}
	if resp.TotalBytes < 2048 {
		t.Errorf("total_bytes = %d, want >= 2048", resp.TotalBytes)
	}
	if resp.TotalHuman == "" {
		t.Error("total_human is empty")
	}
}
