package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/akshaykhairmode/scalper/internal/config"
)

func writeTempConfigFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func newLocalRequest(method, target string, body ...io.Reader) *http.Request {
	var r io.Reader
	if len(body) > 0 {
		r = body[0]
	}
	req := httptest.NewRequest(method, target, r)
	req.Host = "127.0.0.1:8080"
	req.SetBasicAuth("admin", "adminpass")
	return req
}

func sampleValidYAML() string {
	cfg := config.Default()
	cfg.Kotak.MobileNumber = "+919999999999"
	cfg.Kotak.UCC = "TESTUCC"
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(cfg)
	_ = enc.Close()
	return buf.String()
}

func TestSettingsView(t *testing.T) {
	tmpPath := writeTempConfigFile(t, sampleValidYAML())
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodGet, "/settings")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Scalper — Settings &amp; Configuration") {
		t.Errorf("expected page title in body, got: %s", body)
	}
	if !strings.Contains(body, "Trading &amp; Risk Caps") {
		t.Errorf("expected Trading & Risk tab in body")
	}
	if !strings.Contains(body, "Market &amp; Indices") {
		t.Errorf("expected Market & Indices tab in body")
	}
	if !strings.Contains(body, "Raw YAML / Advanced") {
		t.Errorf("expected Raw YAML tab in body")
	}
	if !strings.Contains(body, tmpPath) {
		t.Errorf("expected config path %s in body", tmpPath)
	}
}

func TestGetConfigSuccess(t *testing.T) {
	yamlStr := sampleValidYAML()
	tmpPath := writeTempConfigFile(t, yamlStr)
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodGet, "/api/config")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		OK     bool          `json:"ok"`
		Path   string        `json:"path"`
		YAML   string        `json:"yaml"`
		Config config.Config `json:"config"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}

	if !resp.OK {
		t.Errorf("expected ok: true")
	}
	if resp.Path != tmpPath {
		t.Errorf("expected path %q, got %q", tmpPath, resp.Path)
	}
	if !strings.Contains(resp.YAML, "default_product") {
		t.Errorf("expected default_product in returned yaml")
	}
	if resp.Config.Trading.DefaultProduct != "MIS" {
		t.Errorf("expected default product MIS, got %s", resp.Config.Trading.DefaultProduct)
	}
}

func configWithSecretsYAML(t *testing.T) (string, config.Config) {
	t.Helper()
	cfg := config.Default()
	cfg.Kotak.MobileNumber = "+919999999999"
	cfg.Kotak.UCC = "TESTUCC"
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "super-secret"
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		t.Fatalf("encode yaml: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close yaml: %v", err)
	}
	return buf.String(), cfg
}

func TestConfigResponsesRedactSecrets(t *testing.T) {
	yamlStr, cfg := configWithSecretsYAML(t)
	tmpPath := writeTempConfigFile(t, yamlStr)
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg, configPath: tmpPath})
	defer s.Close()

	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodGet, "/api/config")
	req.SetBasicAuth(cfg.Web.BasicAuth.Username, cfg.Web.BasicAuth.Password)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{cfg.Kotak.MobileNumber, cfg.Kotak.UCC, cfg.Web.BasicAuth.Password} {
		if strings.Contains(body, secret) {
			t.Fatalf("config response leaked %q: %s", secret, body)
		}
	}

	var resp struct {
		YAML   string        `json:"yaml"`
		Config config.Config `json:"config"`
	}
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(resp.YAML, config.SecretMask) ||
		resp.Config.Web.BasicAuth.Password != config.SecretMask ||
		resp.Config.Kotak.MobileNumber != config.SecretMask ||
		resp.Config.Kotak.UCC != config.SecretMask {
		t.Fatalf("config response was not masked: %+v\n%s", resp.Config, resp.YAML)
	}

	settingsRec := httptest.NewRecorder()
	settingsReq := newLocalRequest(http.MethodGet, "/settings")
	settingsReq.SetBasicAuth(cfg.Web.BasicAuth.Username, cfg.Web.BasicAuth.Password)
	s.Handler().ServeHTTP(settingsRec, settingsReq)
	if settingsRec.Code != http.StatusOK {
		t.Fatalf("settings status = %d", settingsRec.Code)
	}
	for _, secret := range []string{cfg.Kotak.MobileNumber, cfg.Kotak.UCC, cfg.Web.BasicAuth.Password} {
		if strings.Contains(settingsRec.Body.String(), secret) {
			t.Fatalf("settings page leaked %q", secret)
		}
	}
}

func TestSaveConfigPreservesMaskedSecrets(t *testing.T) {
	yamlStr, cfg := configWithSecretsYAML(t)
	tmpPath := writeTempConfigFile(t, yamlStr)
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg, configPath: tmpPath})
	defer s.Close()

	getRec := httptest.NewRecorder()
	getReq := newLocalRequest(http.MethodGet, "/api/config")
	getReq.SetBasicAuth(cfg.Web.BasicAuth.Username, cfg.Web.BasicAuth.Password)
	s.Handler().ServeHTTP(getRec, getReq)
	var getResp struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	edited := strings.Replace(getResp.YAML, "default_product: MIS", "default_product: NRML", 1)
	if edited == getResp.YAML {
		edited = strings.Replace(getResp.YAML, `default_product: "MIS"`, `default_product: "NRML"`, 1)
	}

	payload, _ := json.Marshal(map[string]any{"yaml": edited})
	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodPost, "/api/config", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(cfg.Web.BasicAuth.Username, cfg.Web.BasicAuth.Password)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, cfg.Web.BasicAuth.Password) ||
		strings.Contains(body, cfg.Kotak.MobileNumber) || strings.Contains(body, cfg.Kotak.UCC) {
		t.Fatalf("save response leaked a secret: %s", body)
	}

	saved, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	savedCfg, err := config.LoadBytes(saved, tmpPath)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if savedCfg.Web.BasicAuth.Password != cfg.Web.BasicAuth.Password ||
		savedCfg.Kotak.MobileNumber != cfg.Kotak.MobileNumber ||
		savedCfg.Kotak.UCC != cfg.Kotak.UCC {
		t.Fatalf("masked secrets were not preserved: %+v", savedCfg)
	}
	if savedCfg.Trading.DefaultProduct != "NRML" {
		t.Fatalf("expected edited value to be saved, got %q", savedCfg.Trading.DefaultProduct)
	}
}

func TestSaveConfigAcceptsNewSecret(t *testing.T) {
	yamlStr, cfg := configWithSecretsYAML(t)
	tmpPath := writeTempConfigFile(t, yamlStr)
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg, configPath: tmpPath})
	defer s.Close()

	edited := strings.Replace(yamlStr, "password: super-secret", "password: brand-new-secret", 1)
	payload, _ := json.Marshal(map[string]any{"yaml": edited})
	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodPost, "/api/config", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(cfg.Web.BasicAuth.Username, cfg.Web.BasicAuth.Password)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	saved, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(saved), "brand-new-secret") {
		t.Fatalf("new secret was not saved:\n%s", saved)
	}
}

func TestGetConfigFallbackWhenFileMissing(t *testing.T) {
	nonExistentPath := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: nonExistentPath})
	defer s.Close()

	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodGet, "/api/config")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		OK   bool   `json:"ok"`
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected ok: true")
	}
	if !strings.Contains(resp.YAML, "trading") {
		t.Errorf("expected fallback serialized YAML")
	}
}

func TestValidateConfigValid(t *testing.T) {
	yamlStr := sampleValidYAML()
	tmpPath := writeTempConfigFile(t, yamlStr)
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	payload, _ := json.Marshal(map[string]any{
		"yaml":          yamlStr,
		"validate_only": true,
	})
	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodPost, "/api/config", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		OK    bool `json:"ok"`
		Valid bool `json:"valid"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if !resp.OK || !resp.Valid {
		t.Errorf("expected ok: true and valid: true")
	}

	// Verify no backup was created
	if _, err := os.Stat(tmpPath + ".bak"); !os.IsNotExist(err) {
		t.Errorf("expected no backup file for validate_only")
	}
}

func TestValidateConfigSyntaxError(t *testing.T) {
	tmpPath := writeTempConfigFile(t, sampleValidYAML())
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	badYAML := "trading:\n\tdefault_product: MIS\n" // Tabs are illegal in YAML
	payload, _ := json.Marshal(map[string]any{
		"yaml":          badYAML,
		"validate_only": true,
	})
	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodPost, "/api/config", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for syntax error, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.OK {
		t.Errorf("expected ok: false")
	}
}

func TestValidateConfigUnknownField(t *testing.T) {
	tmpPath := writeTempConfigFile(t, sampleValidYAML())
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	unknownFieldYAML := sampleValidYAML() + "\ntrading:\n  unknown_custom_key_foo: 12345\n"
	payload, _ := json.Marshal(map[string]any{
		"yaml":          unknownFieldYAML,
		"validate_only": true,
	})
	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodPost, "/api/config", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestValidateConfigRuleFailure(t *testing.T) {
	tmpPath := writeTempConfigFile(t, sampleValidYAML())
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	// max_daily_loss_inr must be >= 0
	invalidRiskYAML := strings.Replace(sampleValidYAML(), "max_daily_loss_inr: 5000", "max_daily_loss_inr: -100", 1)
	payload, _ := json.Marshal(map[string]any{
		"yaml":          invalidRiskYAML,
		"validate_only": true,
	})
	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodPost, "/api/config", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative cap, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		OK      bool   `json:"ok"`
		Field   string `json:"field"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OK {
		t.Errorf("expected ok: false")
	}
	if resp.Field != "trading.risk.max_daily_loss_inr" {
		t.Errorf("expected field trading.risk.max_daily_loss_inr, got %q", resp.Field)
	}
	if !strings.Contains(resp.Message, "must be >= 0") {
		t.Errorf("expected 'must be >= 0', got %q", resp.Message)
	}
}

func TestSaveConfigSuccessAndBackup(t *testing.T) {
	origYAML := sampleValidYAML()
	tmpPath := writeTempConfigFile(t, origYAML)
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	// Update default_product to NRML
	newYAML := strings.Replace(origYAML, `default_product: "MIS"`, `default_product: "NRML"`, 1)
	if newYAML == origYAML {
		newYAML = strings.Replace(origYAML, `default_product: MIS`, `default_product: "NRML"`, 1)
	}

	payload, _ := json.Marshal(map[string]any{
		"yaml":          newYAML,
		"validate_only": false,
	})
	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodPost, "/api/config", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		OK      bool          `json:"ok"`
		Message string        `json:"message"`
		Backup  string        `json:"backup"`
		Config  config.Config `json:"config"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}

	if !resp.OK {
		t.Errorf("expected ok: true")
	}
	if resp.Config.Trading.DefaultProduct != "NRML" {
		t.Errorf("expected default product NRML, got %s", resp.Config.Trading.DefaultProduct)
	}

	// 1. Verify backup file was created with the ORIGINAL content
	backupData, err := os.ReadFile(tmpPath + ".bak")
	if err != nil {
		t.Fatalf("read backup file: %v", err)
	}
	if string(backupData) != origYAML {
		t.Errorf("backup content does not match original YAML")
	}

	// 2. Verify config file was overwritten with the NEW content
	savedData, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("read saved config file: %v", err)
	}
	if string(savedData) != newYAML {
		t.Errorf("saved config content does not match new YAML")
	}

	// 3. Verify server in-memory configuration was updated
	if s.cfg.Trading.DefaultProduct != "NRML" {
		t.Errorf("server in-memory config not updated: got %s", s.cfg.Trading.DefaultProduct)
	}
}

func TestSaveConfigPUTMethod(t *testing.T) {
	origYAML := sampleValidYAML()
	tmpPath := writeTempConfigFile(t, origYAML)
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	payload, _ := json.Marshal(map[string]any{
		"yaml": origYAML,
	})
	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodPut, "/api/config", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for PUT method, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSaveConfigRejectsEmptyAndMalformed(t *testing.T) {
	tmpPath := writeTempConfigFile(t, sampleValidYAML())
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	// Empty YAML
	payload1, _ := json.Marshal(map[string]any{"yaml": "   "})
	rec1 := httptest.NewRecorder()
	req1 := newLocalRequest(http.MethodPost, "/api/config", bytes.NewReader(payload1))
	req1.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty yaml, got %d", rec1.Code)
	}

	// Malformed JSON
	rec2 := httptest.NewRecorder()
	req2 := newLocalRequest(http.MethodPost, "/api/config", strings.NewReader("{not json}"))
	req2.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed json, got %d", rec2.Code)
	}
}

func TestConfigAuthProtectionInServeMode(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "strongpass"

	tmpPath := writeTempConfigFile(t, sampleValidYAML())
	s := newTestServer(t, serverOpts{db: fakeDB{}, cfg: &cfg, configPath: tmpPath})
	defer s.Close()

	// 1. Unauthenticated request to /settings must return 401
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/settings", nil)
	s.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated /settings in serve mode, got %d", rec1.Code)
	}

	// 2. Unauthenticated request to /api/config must return 401
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated /api/config in serve mode, got %d", rec2.Code)
	}

	// 3. Authenticated request with Basic Auth header must succeed
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req3.SetBasicAuth("admin", "strongpass")
	s.Handler().ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("expected 200 with basic auth, got %d: %s", rec3.Code, rec3.Body.String())
	}

	// 4. Authenticated request with session cookie must succeed
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	token, err := newServeSessionID()
	if err != nil {
		t.Fatalf("create serve session: %v", err)
	}
	s.serveSessionsMu.Lock()
	s.serveSessions[token] = time.Now().Add(2 * time.Hour)
	s.serveSessionsMu.Unlock()
	req4.AddCookie(&http.Cookie{Name: "scalper_serve_session", Value: token})
	s.Handler().ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Errorf("expected 200 with serve session cookie, got %d: %s", rec4.Code, rec4.Body.String())
	}
}

func TestConfigCrossOriginProtection(t *testing.T) {
	tmpPath := writeTempConfigFile(t, sampleValidYAML())
	s := newTestServer(t, serverOpts{db: fakeDB{}, configPath: tmpPath})
	defer s.Close()

	// Cross-origin request
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://evil-hacker.com")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for cross-origin host, got %d", rec.Code)
	}
}
