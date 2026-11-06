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
	"reflect"
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
		if err := enc.Encode(&currentCfg); err != nil {
			s.log.Error("encode fallback settings yaml", "event", "config_encode_failed", "error", err,
				"request_id", logging.RequestID(r.Context()))
			rawYAML = ""
		} else if err := enc.Close(); err != nil {
			s.log.Error("close fallback settings yaml", "event", "config_encode_failed", "error", err,
				"request_id", logging.RequestID(r.Context()))
			rawYAML = ""
		} else {
			rawYAML = buf.String()
		}
	}

	redactedYAML, err := config.RedactYAMLSecrets([]byte(rawYAML))
	if err != nil {
		s.log.Warn("redact settings yaml", "event", "config_redact_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		redactedYAML = []byte("# Could not parse config.yaml. Secrets were hidden; edit the file directly.\n")
	}

	jsonBytes, err := json.Marshal(config.RedactSecrets(currentCfg))
	if err != nil {
		s.log.Error("encode settings config", "event", "config_encode_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		_ = writeJSONError(w, http.StatusInternalServerError, "render failed", logging.RequestID(r.Context()))
		return
	}

	page := settingsPageData{
		Title:       "Scalper — Settings & Configuration",
		Version:     version.Version,
		Commit:      version.Commit,
		ConfigPath:  cfgPath,
		ServeMode:   s.serveMode,
		Now:         s.now().In(currentCfg.Location()).Format("15:04:05 IST"),
		InitialYAML: string(redactedYAML),
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
			if err := enc.Encode(&currentCfg); err != nil {
				s.log.Error("encode fallback config", "event", "config_encode_failed", "error", err,
					"request_id", logging.RequestID(r.Context()))
				_ = writeJSONError(w, http.StatusInternalServerError, "failed to read configuration", logging.RequestID(r.Context()))
				return
			}
			if err := enc.Close(); err != nil {
				s.log.Error("close fallback config", "event", "config_encode_failed", "error", err,
					"request_id", logging.RequestID(r.Context()))
				_ = writeJSONError(w, http.StatusInternalServerError, "failed to read configuration", logging.RequestID(r.Context()))
				return
			}
			data = buf.Bytes()
		} else {
			s.log.Error("read config file", "event", "config_read_failed", "path", cfgPath, "error", err,
				"request_id", logging.RequestID(r.Context()))
			_ = writeJSONError(w, http.StatusInternalServerError, "failed to read configuration", logging.RequestID(r.Context()))
			return
		}
	}

	redactedYAML, redactErr := config.RedactYAMLSecrets(data)
	if redactErr != nil {
		s.log.Warn("redact config yaml", "event", "config_redact_failed", "path", cfgPath, "error", redactErr,
			"request_id", logging.RequestID(r.Context()))
	}

	parsedCfg, err := config.LoadBytes(data, cfgPath)
	if err != nil {
		resp := map[string]any{
			"ok":    true,
			"path":  cfgPath,
			"error": err.Error(),
		}
		if redactErr == nil {
			resp["yaml"] = string(redactedYAML)
		}
		_ = writeJSON(w, http.StatusOK, resp)
		return
	}

	resp := map[string]any{
		"ok":       true,
		"path":     cfgPath,
		"config":   config.RedactSecrets(parsedCfg),
		"warnings": parsedCfg.Warnings(),
	}
	if redactErr == nil {
		resp["yaml"] = string(redactedYAML)
	}
	_ = writeJSON(w, http.StatusOK, resp)
}

type updateConfigRequest struct {
	YAML         string `json:"yaml"`
	ValidateOnly bool   `json:"validate_only"`
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if !requireJSONContentType(r) {
		_ = writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{
			"ok":    false,
			"error": "Content-Type must be application/json",
		})
		return
	}
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

	// Masked values returned by GET /api/config mean "keep the stored secret".
	// Restore them before validation so the mask itself is never persisted.
	currentYAML, readErr := os.ReadFile(cfgPath) // #nosec G304
	if readErr != nil && !os.IsNotExist(readErr) {
		s.log.Warn("read config before update", "event", "config_read_failed", "path", cfgPath,
			"error", readErr, "request_id", logging.RequestID(r.Context()))
	}
	restoredYAML, err := config.RestoreYAMLSecrets([]byte(req.YAML), currentYAML)
	if err != nil {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":      false,
			"error":   "invalid YAML configuration",
			"message": "invalid YAML configuration",
		})
		return
	}

	// 1. Strict YAML syntax and unknown fields check using config.LoadBytes
	newCfg, err := config.LoadBytes(restoredYAML, cfgPath)
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
			"config":   config.RedactSecrets(newCfg),
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
	if werr := os.WriteFile(tmpPath, restoredYAML, 0o600); werr != nil { // #nosec G304 G703
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
	oldCfg := s.cfg
	s.cfg = newCfg
	if boot, berr := newBootstrap(newCfg, s.mpin != ""); berr == nil {
		s.bootstrap = boot
	}
	if oldCfg.Web.BasicAuth != newCfg.Web.BasicAuth {
		s.revokeAllServeSessions()
	}
	restartRequired := restartRequiredFields(oldCfg, newCfg)

	s.log.Info("configuration updated", "event", "config_saved", "path", cleanPath, "backup", backupPath,
		"request_id", logging.RequestID(r.Context()))

	resp := map[string]any{
		"ok":               true,
		"message":          "Configuration saved successfully",
		"backup":           backupPath,
		"config":           config.RedactSecrets(newCfg),
		"warnings":         newCfg.Warnings(),
		"restart_required": restartRequired,
	}
	if redactedYAML, rerr := config.RedactYAMLSecrets(restoredYAML); rerr == nil {
		resp["yaml"] = string(redactedYAML)
	} else {
		s.log.Warn("redact saved config yaml", "event", "config_redact_failed", "path", cleanPath,
			"error", rerr, "request_id", logging.RequestID(r.Context()))
	}
	_ = writeJSON(w, http.StatusOK, resp)
}

// restartRequiredFields reports saved changes that the running process cannot
// apply without a restart. Auth and trading settings are applied live.
func restartRequiredFields(oldCfg, newCfg config.Config) []string {
	var fields []string
	if oldCfg.App.Listen != newCfg.App.Listen {
		fields = append(fields, "app.listen")
	}
	if oldCfg.App.DBPath != newCfg.App.DBPath {
		fields = append(fields, "app.db_path")
	}
	if !reflect.DeepEqual(oldCfg.Logging, newCfg.Logging) {
		fields = append(fields, "logging")
	}
	if !reflect.DeepEqual(oldCfg.Market, newCfg.Market) {
		fields = append(fields, "market")
	}
	if !reflect.DeepEqual(oldCfg.Scrip, newCfg.Scrip) {
		fields = append(fields, "scrip")
	}
	if oldCfg.Web.OpenBrowser != newCfg.Web.OpenBrowser {
		fields = append(fields, "web.open_browser")
	}
	return fields
}
