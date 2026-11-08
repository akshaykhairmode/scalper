package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/store"
	"github.com/akshaykhairmode/scalper/internal/webauthn"
)

type webauthnStoredChallenge struct {
	Challenge string
	ExpiresAt time.Time
	Pairing   bool
}

// DeviceStore persists registered WebAuthn / Passkey devices.
type DeviceStore interface {
	SaveDevice(ctx context.Context, dev store.Device) error
	GetDevice(ctx context.Context, id string) (*store.Device, error)
	ListDevices(ctx context.Context) ([]store.Device, error)
	UpdateDeviceUsage(ctx context.Context, id string, signCount uint32, lastUsedAt time.Time) error
	DeleteDevice(ctx context.Context, id string) error
	CountDevices(ctx context.Context) (int, error)
}

func (s *Server) webAuthnForRequest(r *http.Request) *webauthn.WebAuthn {
	rpID := "localhost"
	if r != nil && r.Host != "" {
		h, _, err := net.SplitHostPort(r.Host)
		if err != nil || h == "" {
			h = r.Host
		}
		ip := net.ParseIP(h)
		if ip != nil {
			if ip.IsLoopback() {
				rpID = "localhost"
			} else {
				// IP addresses are not valid WebAuthn RP IDs.
				rpID = ""
			}
		} else {
			rpID = h
		}
	}
	scheme := "http"
	if r != nil && secureRequest(r) {
		scheme = "https"
	}
	origins := []string{"http://localhost:8080", "http://127.0.0.1:8080", "https://localhost"}
	if r != nil && r.Host != "" {
		origins = append(origins, fmt.Sprintf("%s://%s", scheme, r.Host))
	}
	if r != nil {
		if reqOrigin := strings.TrimSpace(r.Header.Get("Origin")); reqOrigin != "" {
			origins = append(origins, reqOrigin)
		}
	}

	return webauthn.New(webauthn.Config{
		RPID:    rpID,
		RPName:  "Scalper Terminal",
		Origins: origins,
	})
}

func (s *Server) storeWebAuthnChallenge(challenge string, pairing bool) {
	s.webauthnChallengesMu.Lock()
	defer s.webauthnChallengesMu.Unlock()
	now := time.Now()
	for c, sc := range s.webauthnChallenges {
		if now.After(sc.ExpiresAt) {
			delete(s.webauthnChallenges, c)
		}
	}
	s.webauthnChallenges[challenge] = webauthnStoredChallenge{
		Challenge: challenge,
		ExpiresAt: now.Add(5 * time.Minute),
		Pairing:   pairing,
	}
}

func (s *Server) takeWebAuthnChallenge(challenge string) (webauthnStoredChallenge, bool) {
	s.webauthnChallengesMu.Lock()
	defer s.webauthnChallengesMu.Unlock()
	sc, ok := s.webauthnChallenges[challenge]
	if !ok {
		return webauthnStoredChallenge{}, false
	}
	delete(s.webauthnChallenges, challenge)
	if time.Now().After(sc.ExpiresAt) {
		return webauthnStoredChallenge{}, false
	}
	return sc, true
}

func (s *Server) createPairToken() (string, time.Time, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(b[:])
	expiresAt := time.Now().Add(10 * time.Minute)

	s.webauthnChallengesMu.Lock()
	defer s.webauthnChallengesMu.Unlock()
	s.webauthnPairTokens[token] = expiresAt
	return token, expiresAt, nil
}

func (s *Server) validPairToken(token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	s.webauthnChallengesMu.Lock()
	defer s.webauthnChallengesMu.Unlock()
	now := time.Now()
	for t, exp := range s.webauthnPairTokens {
		if now.After(exp) {
			delete(s.webauthnPairTokens, t)
		}
	}
	exp, ok := s.webauthnPairTokens[token]
	return ok && now.Before(exp)
}

func (s *Server) consumePairToken(token string) {
	if strings.TrimSpace(token) == "" {
		return
	}
	s.webauthnChallengesMu.Lock()
	defer s.webauthnChallengesMu.Unlock()
	delete(s.webauthnPairTokens, token)
}

func extractPairToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if tok := r.Header.Get("X-Pair-Token"); tok != "" {
		return strings.TrimSpace(tok)
	}
	if tok := r.URL.Query().Get("pair_token"); tok != "" {
		return strings.TrimSpace(tok)
	}
	return ""
}

// POST /api/auth/webauthn/register/begin
func (s *Server) handleWebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if s.deviceStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Device storage unavailable"})
		return
	}

	pairToken := extractPairToken(r)
	isPairing := s.validPairToken(pairToken)
	if !s.isServeAuthenticated(r) && !isPairing {
		_ = writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Authentication or pairing token required"})
		return
	}

	wAuth := s.webAuthnForRequest(r)
	opts, err := wAuth.BeginRegistration("scalper-user", "scalper-user", "Scalper Terminal")
	if err != nil {
		s.log.Error("webauthn begin registration failed", "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to begin registration"})
		return
	}

	s.storeWebAuthnChallenge(opts.Challenge, isPairing)

	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":                     true,
		"options":                opts,
		"challenge":              opts.Challenge,
		"rp":                     opts.RP,
		"user":                   opts.User,
		"pubKeyCredParams":       opts.PubKeyCredParams,
		"authenticatorSelection": opts.AuthenticatorSelection,
		"timeout":                opts.Timeout,
		"attestation":            opts.Attestation,
	})
}

// POST /api/auth/webauthn/register/finish
func (s *Server) handleWebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if s.deviceStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Device storage unavailable"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid request body"})
		return
	}

	// Try extracting pair token and device name from JSON
	var meta struct {
		DeviceName string `json:"deviceName"`
		DevNameAlt string `json:"device_name"`
		PairToken  string `json:"pairToken"`
		PairTokAlt string `json:"pair_token"`
	}
	_ = json.Unmarshal(raw, &meta)

	pairToken := meta.PairToken
	if pairToken == "" {
		pairToken = meta.PairTokAlt
	}
	if pairToken == "" {
		pairToken = extractPairToken(r)
	}

	isPairing := s.validPairToken(pairToken)
	if !s.isServeAuthenticated(r) && !isPairing {
		_ = writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Authentication or pairing token required"})
		return
	}

	// Unmarshal registration response (supports nested under "response" or top-level)
	var regResp webauthn.RegistrationResponse
	if err := json.Unmarshal(raw, &regResp); err != nil || regResp.Response.ClientDataJSON == "" {
		var wrapper struct {
			Response webauthn.RegistrationResponse `json:"response"`
		}
		if wErr := json.Unmarshal(raw, &wrapper); wErr == nil && wrapper.Response.Response.ClientDataJSON != "" {
			regResp = wrapper.Response
		} else {
			_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid registration response data"})
			return
		}
	}

	// Extract challenge from clientDataJSON to look up stored challenge
	clientBytes, err := webauthn.DecodeBase64(regResp.Response.ClientDataJSON)
	if err != nil {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid clientDataJSON encoding"})
		return
	}
	var cData struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(clientBytes, &cData); err != nil || cData.Challenge == "" {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid clientDataJSON structure"})
		return
	}

	sc, ok := s.takeWebAuthnChallenge(cData.Challenge)
	if !ok {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Invalid or expired registration challenge"})
		return
	}

	wAuth := s.webAuthnForRequest(r)
	result, err := wAuth.FinishRegistration(sc.Challenge, regResp)
	if err != nil {
		s.log.Warn("webauthn registration verification failed", "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// Derive friendly device name
	deviceName := strings.TrimSpace(meta.DeviceName)
	if deviceName == "" {
		deviceName = strings.TrimSpace(meta.DevNameAlt)
	}
	if deviceName == "" {
		ua := r.UserAgent()
		switch {
		case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
			deviceName = "Apple Device (Touch ID / Face ID)"
		case strings.Contains(ua, "Macintosh"):
			deviceName = "MacBook (Touch ID)"
		case strings.Contains(ua, "Android"):
			deviceName = "Android Device"
		case strings.Contains(ua, "Windows"):
			deviceName = "Windows PC (Windows Hello)"
		default:
			shortID := result.CredentialID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			deviceName = "Device (" + shortID + ")"
		}
	}

	now := time.Now()
	dev := store.Device{
		ID:         result.CredentialID,
		DeviceName: deviceName,
		PublicKey:  result.PublicKey,
		Algorithm:  result.Algorithm,
		SignCount:  result.SignCount,
		CreatedAt:  now,
		LastUsedAt: now,
	}

	if err := s.deviceStore.SaveDevice(r.Context(), dev); err != nil {
		s.log.Error("save device failed", "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to persist device"})
		return
	}

	s.setDeviceBindingCookie(w, r, dev.ID)

	var sessionToken string
	if sc.Pairing || isPairing {
		s.consumePairToken(pairToken)
		sessionToken = s.issueServeSession(w, r)
	}

	s.log.Info("registered webauthn device", "device_name", dev.DeviceName, "id", dev.ID, "request_id", logging.RequestID(r.Context()))
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"device": dev,
		"token":  sessionToken,
	})
}

// POST /api/auth/webauthn/login/begin
func (s *Server) handleWebAuthnLoginBegin(w http.ResponseWriter, r *http.Request) {
	if s.deviceStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Device storage unavailable"})
		return
	}

	devices, err := s.deviceStore.ListDevices(r.Context())
	if err != nil {
		s.log.Error("list devices failed", "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to query devices"})
		return
	}
	if len(devices) == 0 {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "No registered devices found. Enroll this device first."})
		return
	}

	var credIDs []string
	hasWebAuthn := false
	hasBoundDevice := false
	for _, d := range devices {
		if d.PublicKey != "bound-device" {
			credIDs = append(credIDs, d.ID)
			hasWebAuthn = true
		} else {
			hasBoundDevice = true
		}
	}

	if !hasWebAuthn {
		// Only bound devices exist (e.g. paired mobile devices)
		_ = writeJSON(w, http.StatusOK, map[string]any{
			"ok":               true,
			"has_webauthn":     false,
			"has_bound_device": hasBoundDevice,
		})
		return
	}

	wAuth := s.webAuthnForRequest(r)
	opts, err := wAuth.BeginAssertion(credIDs)
	if err != nil {
		s.log.Error("webauthn begin assertion failed", "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to begin assertion"})
		return
	}

	s.storeWebAuthnChallenge(opts.Challenge, false)

	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"has_webauthn":     true,
		"has_bound_device": hasBoundDevice,
		"options":          opts,
		"challenge":        opts.Challenge,
		"rpId":             opts.RPID,
		"timeout":          opts.Timeout,
		"userVerification": opts.UserVerification,
		"allowCredentials": opts.AllowCredentials,
	})
}

// POST /api/auth/webauthn/login/finish
func (s *Server) handleWebAuthnLoginFinish(w http.ResponseWriter, r *http.Request) {
	if s.deviceStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Device storage unavailable"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid request body"})
		return
	}

	var assertResp webauthn.AssertionResponse
	if err := json.Unmarshal(raw, &assertResp); err != nil || assertResp.Response.ClientDataJSON == "" {
		var wrapper struct {
			Response webauthn.AssertionResponse `json:"response"`
		}
		if wErr := json.Unmarshal(raw, &wrapper); wErr == nil && wrapper.Response.Response.ClientDataJSON != "" {
			assertResp = wrapper.Response
		} else {
			_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid assertion response data"})
			return
		}
	}

	clientBytes, err := webauthn.DecodeBase64(assertResp.Response.ClientDataJSON)
	if err != nil {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid clientDataJSON encoding"})
		return
	}
	var cData struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(clientBytes, &cData); err != nil || cData.Challenge == "" {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid clientDataJSON structure"})
		return
	}

	sc, ok := s.takeWebAuthnChallenge(cData.Challenge)
	if !ok {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Invalid or expired assertion challenge"})
		return
	}

	// Look up device
	dev, err := s.deviceStore.GetDevice(r.Context(), assertResp.ID)
	if err != nil {
		s.log.Error("get device failed", "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to look up device"})
		return
	}
	if dev == nil {
		// Try matching across all devices using decoded bytes in case encoding differs
		devices, listErr := s.deviceStore.ListDevices(r.Context())
		if listErr == nil {
			targetBytes, _ := webauthn.DecodeBase64(assertResp.ID)
			for _, d := range devices {
				dBytes, _ := webauthn.DecodeBase64(d.ID)
				if len(targetBytes) > 0 && len(dBytes) > 0 && string(targetBytes) == string(dBytes) {
					dev = &d
					break
				}
			}
		}
	}
	if dev == nil {
		_ = writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Device not recognized"})
		return
	}

	stored := webauthn.StoredDevice{
		ID:        dev.ID,
		PublicKey: dev.PublicKey,
		Algorithm: dev.Algorithm,
		SignCount: dev.SignCount,
	}

	wAuth := s.webAuthnForRequest(r)
	result, err := wAuth.FinishAssertion(sc.Challenge, stored, assertResp)
	if err != nil {
		s.log.Warn("webauthn assertion failed", "device", dev.DeviceName, "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Biometric verification failed: " + err.Error()})
		return
	}

	now := time.Now()
	newSignCount := dev.SignCount
	if result.SignCount > 0 {
		newSignCount = result.SignCount
	}
	_ = s.deviceStore.UpdateDeviceUsage(r.Context(), dev.ID, newSignCount, now)

	s.setDeviceBindingCookie(w, r, dev.ID)

	token := s.issueServeSession(w, r)
	if token == "" {
		s.log.Error("create serve session failed", "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to create session"})
		return
	}

	s.clearUnlockFailures(serveUnlockKey(r))
	s.log.Info("biometric login succeeded", "device_name", dev.DeviceName, "request_id", logging.RequestID(r.Context()))

	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"token":       token,
		"device_name": dev.DeviceName,
		"device_id":   dev.ID,
	})
}

// GET /api/devices
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	if s.deviceStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Device storage unavailable"})
		return
	}

	if !s.isServeAuthenticated(r) {
		// Unauthenticated callers only get the count to see if biometric unlock is available
		count, err := s.deviceStore.CountDevices(r.Context())
		if err != nil {
			s.log.Error("count devices failed", "error", err, "request_id", logging.RequestID(r.Context()))
			_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to count devices"})
			return
		}
		_ = writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"has_devices": count > 0,
			"count":       count,
			"devices":     []any{},
		})
		return
	}

	devices, err := s.deviceStore.ListDevices(r.Context())
	if err != nil {
		s.log.Error("list devices failed", "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to list devices"})
		return
	}

	var currentDevID string
	if cookie, err := r.Cookie(deviceBindingCookie); err == nil {
		currentDevID = cookie.Value
	}

	type deviceItem struct {
		store.Device
		IsCurrent bool `json:"is_current"`
	}
	items := make([]deviceItem, len(devices))
	for i, d := range devices {
		items[i] = deviceItem{
			Device:    d,
			IsCurrent: d.ID == currentDevID,
		}
	}

	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"devices":           items,
		"count":             len(items),
		"has_devices":       len(items) > 0,
		"current_device_id": currentDevID,
	})
}

// DELETE /api/devices/{id}
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if s.deviceStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Device storage unavailable"})
		return
	}
	if !s.isServeAuthenticated(r) {
		_ = writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Authentication required"})
		return
	}

	id := r.PathValue("id")
	if id == "" {
		id = strings.TrimPrefix(r.URL.Path, "/api/devices/")
	}
	if strings.TrimSpace(id) == "" {
		_ = writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "device ID is required"})
		return
	}

	if err := s.deviceStore.DeleteDevice(r.Context(), id); err != nil {
		s.log.Error("delete device failed", "id", id, "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to delete device"})
		return
	}

	if cookie, err := r.Cookie(deviceBindingCookie); err == nil && cookie.Value == id {
		s.clearDeviceBindingCookie(w, r)
	}

	s.log.Info("deleted webauthn device", "id", id, "request_id", logging.RequestID(r.Context()))
	_ = writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// POST /api/devices/bind
func (s *Server) handleBindDevice(w http.ResponseWriter, r *http.Request) {
	if s.deviceStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Device storage unavailable"})
		return
	}

	pairToken := extractPairToken(r)
	isPairing := s.validPairToken(pairToken)
	if !s.isServeAuthenticated(r) && !isPairing {
		_ = writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Authentication or pairing token required"})
		return
	}

	var req struct {
		DeviceName string `json:"deviceName"`
		DevNameAlt string `json:"device_name"`
		PairToken  string `json:"pairToken"`
		PairTokAlt string `json:"pair_token"`
	}
	if requireJSONContentType(r) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.PairToken != "" && s.validPairToken(req.PairToken) {
		isPairing = true
		pairToken = req.PairToken
	} else if req.PairTokAlt != "" && s.validPairToken(req.PairTokAlt) {
		isPairing = true
		pairToken = req.PairTokAlt
	}

	deviceName := strings.TrimSpace(req.DeviceName)
	if deviceName == "" {
		deviceName = strings.TrimSpace(req.DevNameAlt)
	}
	if deviceName == "" {
		ua := r.UserAgent()
		switch {
		case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
			deviceName = "Apple Device (Mobile)"
		case strings.Contains(ua, "Android"):
			deviceName = "Android Device"
		case strings.Contains(ua, "Macintosh"):
			deviceName = "Mac Browser"
		case strings.Contains(ua, "Windows"):
			deviceName = "Windows PC"
		default:
			deviceName = "Bound Device"
		}
	}

	rawID, err := newServeSessionID()
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to create device ID"})
		return
	}
	devID := "dev-" + rawID[:16]
	now := time.Now()
	dev := store.Device{
		ID:         devID,
		DeviceName: deviceName,
		PublicKey:  "bound-device",
		Algorithm:  0,
		SignCount:  1,
		CreatedAt:  now,
		LastUsedAt: now,
	}
	if err := s.deviceStore.SaveDevice(r.Context(), dev); err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to save device"})
		return
	}

	s.setDeviceBindingCookie(w, r, devID)
	if isPairing {
		s.consumePairToken(pairToken)
	}
	sessionToken := s.issueServeSession(w, r)

	s.log.Info("bound device", "device_name", dev.DeviceName, "id", dev.ID, "request_id", logging.RequestID(r.Context()))
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"device": dev,
		"token":  sessionToken,
	})
}

// POST /api/devices/pair-token
func (s *Server) handleCreatePairToken(w http.ResponseWriter, r *http.Request) {
	if !s.isServeAuthenticated(r) && !s.isDeviceBound(r) {
		_ = writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Authentication required"})
		return
	}

	token, expiresAt, err := s.createPairToken()
	if err != nil {
		s.log.Error("create pair token failed", "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to generate pairing token"})
		return
	}

	scheme := "http"
	if secureRequest(r) {
		scheme = "https"
	}
	host := r.Host
	if reqHost, reqPort, err := net.SplitHostPort(host); err == nil {
		if isLoopbackHost(reqHost) {
			if lanIP := getOutboundLANIP(); lanIP != "" {
				host = net.JoinHostPort(lanIP, reqPort)
			}
		}
	} else if isLoopbackHost(host) {
		if lanIP := getOutboundLANIP(); lanIP != "" {
			host = lanIP
		}
	}
	pairURL := fmt.Sprintf("%s://%s/?pair_token=%s", scheme, host, token)

	s.log.Info("created device pairing token", "expires_at", expiresAt, "url", pairURL, "request_id", logging.RequestID(r.Context()))
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"token":      token,
		"expires_at": expiresAt.Format(time.RFC3339),
		"url":        pairURL,
	})
}

func isLoopbackHost(host string) bool {
	h := strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

func getOutboundLANIP() string {
	// Attempt OS routing table lookup for outbound interface (UDP connectionless, no packets sent)
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		defer func() { _ = conn.Close() }()
		if localAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			if ip := localAddr.IP.To4(); ip != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}

	// Fallback: interface scan skipping loopback and point-to-point (VPN/tunnel) adapters
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}
