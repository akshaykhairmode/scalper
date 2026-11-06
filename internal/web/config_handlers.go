package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/errs"
	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/version"
)

type settingsPageData struct {
	Title       string
	Version     string
	Commit      string
	ConfigPath  string
	ServeMode   bool
	Now         string
	InitialYAML string
	InitialJSON template.JS
}

func (s *Server) handleSettingsView(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	cfgPath := filepath.Clean(s.configPath)
	currentCfg := s.cfg
	s.configMu.RUnlock()

	data, err := os.ReadFile(cfgPath) // #nosec G304
	var rawYAML string
	if err == nil {
		rawYAML = string(data)
	} else {
		// Fallback serialize in-memory config if file does not exist on disk
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		_ = enc.Encode(&currentCfg)
		_ = enc.Close()
		rawYAML = buf.String()
	}

	jsonBytes, _ := json.Marshal(currentCfg)

	page := settingsPageData{
		Title:       "Scalper — Settings & Configuration",
		Version:     version.Version,
		Commit:      version.Commit,
		ConfigPath:  cfgPath,
		ServeMode:   s.serveMode,
		Now:         s.now().In(currentCfg.Location()).Format("15:04:05 IST"),
		InitialYAML: rawYAML,
		InitialJSON: template.JS(jsonBytes), // #nosec G203
	}

	var buf strings.Builder
	if err := s.settingsTmpl.ExecuteTemplate(&buf, "settings.html", page); err != nil {
		s.log.Error("render settings", "event", "http_render_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		_ = writeJSONError(w, http.StatusInternalServerError, "render failed", logging.RequestID(r.Context()))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, buf.String())
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	cfgPath := filepath.Clean(s.configPath)
	s.configMu.RUnlock()

	data, err := os.ReadFile(cfgPath) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			s.configMu.RLock()
			currentCfg := s.cfg
			s.configMu.RUnlock()

			var buf bytes.Buffer
			enc := yaml.NewEncoder(&buf)
			enc.SetIndent(2)
			_ = enc.Encode(&currentCfg)
			_ = enc.Close()
			data = buf.Bytes()
		} else {
			s.log.Error("read config file", "event", "config_read_failed", "path", cfgPath, "error", err,
				"request_id", logging.RequestID(r.Context()))
			_ = writeJSONError(w, http.StatusInternalServerError, "failed to read configuration file: "+err.Error(), logging.RequestID(r.Context()))
			return
		}
	}

	parsedCfg, err := config.LoadBytes(data, cfgPath)
	if err != nil {
		_ = writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"path":  cfgPath,
			"yaml":  string(data),
			"error": err.Error(),
		})
		return
	}

	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"path":     cfgPath,
		"yaml":     string(data),
		"config":   parsedCfg,
		"warnings": parsedCfg.Warnings(),
	})
}

type updateConfigRequest struct {
	YAML         string `json:"yaml"`
	ValidateOnly bool   `json:"validate_only"`
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	// Restrict payload size defensively to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req updateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":      false,
			"error":   "invalid JSON request: " + err.Error(),
			"message": "invalid JSON request: " + err.Error(),
		})
		return
	}

	trimmedYAML := strings.TrimSpace(req.YAML)
	if trimmedYAML == "" {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":      false,
			"error":   "yaml content must not be empty",
			"message": "yaml content must not be empty",
		})
		return
	}

	s.configMu.RLock()
	cfgPath := s.configPath
	s.configMu.RUnlock()

	// 1. Strict YAML syntax and unknown fields check using config.LoadBytes
	newCfg, err := config.LoadBytes([]byte(req.YAML), cfgPath)
	if err != nil {
		var ce *errs.ConfigError
		field := ""
		msg := err.Error()
		if errors.As(err, &ce) {
			field = ce.Field
			if ce.Err != nil {
				msg = ce.Err.Error()
			}
		}
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":      false,
			"error":   err.Error(),
			"field":   field,
			"message": msg,
		})
		return
	}

	// 2. Strict domain rules validation using config.Validate()
	if err := newCfg.Validate(); err != nil {
		var ce *errs.ConfigError
		field := ""
		msg := err.Error()
		if errors.As(err, &ce) {
			field = ce.Field
			if ce.Err != nil {
				msg = ce.Err.Error()
			}
		}
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":      false,
			"error":   err.Error(),
			"field":   field,
			"message": msg,
		})
		return
	}

	// 3. If validation-only check, return valid without writing to disk
	if req.ValidateOnly {
		_ = writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"valid":    true,
			"message":  "Configuration is valid",
			"config":   newCfg,
			"warnings": newCfg.Warnings(),
		})
		return
	}

	// 4. Safe write: lock to prevent race conditions
	s.configMu.Lock()
	defer s.configMu.Unlock()

	cleanPath := filepath.Clean(cfgPath)

	// 4a. Create backup copy (e.g. config.yaml.bak)
	backupPath := cleanPath + ".bak"
	if currentData, rerr := os.ReadFile(cleanPath); rerr == nil { // #nosec G304
		if berr := os.WriteFile(backupPath, currentData, 0o600); berr != nil { // #nosec G304 G703
			s.log.Error("create config backup failed", "event", "config_backup_failed", "backup", backupPath, "error", berr,
				"request_id", logging.RequestID(r.Context()))
			_ = writeJSONError(w, http.StatusInternalServerError, "failed to create backup file: "+berr.Error(), logging.RequestID(r.Context()))
			return
		}
	}

	// 4b. Write temp file and atomically rename
	tmpPath := cleanPath + ".tmp"
	if werr := os.WriteFile(tmpPath, []byte(req.YAML), 0o600); werr != nil { // #nosec G304 G703
		s.log.Error("write temp config failed", "event", "config_write_failed", "tmp", tmpPath, "error", werr,
			"request_id", logging.RequestID(r.Context()))
		_ = writeJSONError(w, http.StatusInternalServerError, "failed to write temporary config: "+werr.Error(), logging.RequestID(r.Context()))
		return
	}

	if rerr := os.Rename(tmpPath, cleanPath); rerr != nil {
		_ = os.Remove(tmpPath)
		s.log.Error("rename temp config failed", "event", "config_rename_failed", "path", cleanPath, "error", rerr,
			"request_id", logging.RequestID(r.Context()))
		_ = writeJSONError(w, http.StatusInternalServerError, "failed to overwrite config file: "+rerr.Error(), logging.RequestID(r.Context()))
		return
	}

	// 4c. Update runtime active server configuration
	s.cfg = newCfg
	if boot, berr := newBootstrap(newCfg, s.mpin != ""); berr == nil {
		s.bootstrap = boot
	}

	s.log.Info("configuration updated", "event", "config_saved", "path", cleanPath, "backup", backupPath,
		"request_id", logging.RequestID(r.Context()))

	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"message":  "Configuration saved successfully",
		"backup":   backupPath,
		"config":   newCfg,
		"warnings": newCfg.Warnings(),
	})
}
