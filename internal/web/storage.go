package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

type storageInfoResponse struct {
	OK         bool   `json:"ok"`
	Path       string `json:"path,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	SizeHuman  string `json:"size_human"`
	WALBytes   int64  `json:"wal_bytes"`
	WALHuman   string `json:"wal_human"`
	SHMBytes   int64  `json:"shm_bytes"`
	SHMHuman   string `json:"shm_human"`
	TotalBytes int64  `json:"total_bytes"`
	TotalHuman string `json:"total_human"`
	Exists     bool   `json:"exists"`
}

// handleStorageInfo reports the SQLite database footprint for the settings page.
func (s *Server) handleStorageInfo(w http.ResponseWriter, _ *http.Request) {
	path := ""
	if provider, ok := s.db.(interface{ Path() string }); ok {
		path = provider.Path()
	}
	if path == "" {
		path = s.configSnapshot().App.DBPath
	}
	if path != "" {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}

	dbBytes, dbExists := fileSize(path)
	walBytes, _ := fileSize(path + "-wal")
	shmBytes, _ := fileSize(path + "-shm")
	total := dbBytes + walBytes + shmBytes

	resp := storageInfoResponse{
		OK:         true,
		Path:       path,
		SizeBytes:  dbBytes,
		SizeHuman:  humanBytes(dbBytes),
		WALBytes:   walBytes,
		WALHuman:   humanBytes(walBytes),
		SHMBytes:   shmBytes,
		SHMHuman:   humanBytes(shmBytes),
		TotalBytes: total,
		TotalHuman: humanBytes(total),
		Exists:     dbExists,
	}
	if err := writeJSON(w, http.StatusOK, resp); err != nil {
		s.log.Warn("write storage info", "event", "http_write_failed", "error", err)
	}
}

func fileSize(path string) (int64, bool) {
	if path == "" {
		return 0, false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0, false
	}
	return info.Size(), true
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = int64(1024)
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := unit, 0
	for value := n / unit; value >= unit && exp < 5; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
