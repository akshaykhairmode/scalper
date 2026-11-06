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
		if err == nil && h != "" {
			rpID = h
		} else if !strings.Contains(r.Host, ":") {
			rpID = r.Host
		}
	}
	scheme := "http"
	if r != nil && secureRequest(r) {
		scheme = "https"
	}
	var origin string
	if r != nil && r.Host != "" {
		origin = fmt.Sprintf("%s://%s", scheme, r.Host)
	} else {
		origin = "http://localhost:8080"
	}

	return webauthn.New(webauthn.Config{
		RPID:    rpID,
		RPName:  "Scalper Terminal",
		Origins: []string{origin},
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

func (s *Server) consumePairToken(token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	s.webauthnChallengesMu.Lock()
	defer s.webauthnChallengesMu.Unlock()
	exp, ok := s.webauthnPairTokens[token]
	if !ok || time.Now().After(exp) {
		delete(s.webauthnPairTokens, token)
		return false
	}
	delete(s.webauthnPairTokens, token)
	return true
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

	var sessionToken string
	if sc.Pairing || isPairing {
		s.consumePairToken(pairToken)
		tok, err := newServeSessionID()
		if err == nil {
			sessionToken = tok
			exp := time.Now().Add(24 * time.Hour)
			s.serveSessionsMu.Lock()
			s.serveSessions[tok] = exp
			s.serveSessionsMu.Unlock()
			http.SetCookie(w, &http.Cookie{ // #nosec G124
				Name:     serveSessionCookie,
				Value:    tok,
				Path:     "/",
				HttpOnly: true,
				Secure:   secureRequest(r),
				SameSite: http.SameSiteStrictMode,
				Expires:  exp,
			})
		}
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
	for _, d := range devices {
		credIDs = append(credIDs, d.ID)
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
	_ = s.deviceStore.UpdateDeviceUsage(r.Context(), dev.ID, result.SignCount, now)

	// Issue serve session cookie and token
	token, err := newServeSessionID()
	if err != nil {
		s.log.Error("create serve session failed", "error", err, "request_id", logging.RequestID(r.Context()))
		_ = writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to create session"})
		return
	}

	exp := time.Now().Add(24 * time.Hour)
	s.serveSessionsMu.Lock()
	s.serveSessions[token] = exp
	s.serveSessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{ // #nosec G124
		Name:     serveSessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  exp,
	})

	s.clearUnlockFailures(serveUnlockKey(r))
	s.log.Info("biometric login succeeded", "device_name", dev.DeviceName, "request_id", logging.RequestID(r.Context()))

	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"token":       token,
		"device_name": dev.DeviceName,
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

	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"devices":     devices,
		"count":       len(devices),
		"has_devices": len(devices) > 0,
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

	s.log.Info("deleted webauthn device", "id", id, "request_id", logging.RequestID(r.Context()))
	_ = writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// POST /api/devices/pair-token
func (s *Server) handleCreatePairToken(w http.ResponseWriter, r *http.Request) {
	if !s.isServeAuthenticated(r) {
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
	pairURL := fmt.Sprintf("%s://%s/?pair_token=%s", scheme, r.Host, token)

	s.log.Info("created device pairing token", "expires_at", expiresAt, "request_id", logging.RequestID(r.Context()))
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"token":      token,
		"expires_at": expiresAt.Format(time.RFC3339),
		"url":        pairURL,
	})
}
