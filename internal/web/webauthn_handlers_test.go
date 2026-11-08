package web

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/store"
	"github.com/akshaykhairmode/scalper/internal/webauthn"
)

type mockDeviceStore struct {
	mu      sync.Mutex
	devices map[string]store.Device
}

func newMockDeviceStore() *mockDeviceStore {
	return &mockDeviceStore{devices: make(map[string]store.Device)}
}

func (m *mockDeviceStore) SaveDevice(_ context.Context, dev store.Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices[dev.ID] = dev
	return nil
}

func (m *mockDeviceStore) GetDevice(_ context.Context, id string) (*store.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return nil, nil
	}
	return &d, nil
}

func (m *mockDeviceStore) ListDevices(_ context.Context) ([]store.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []store.Device
	for _, d := range m.devices {
		list = append(list, d)
	}
	return list, nil
}

func (m *mockDeviceStore) UpdateDeviceUsage(_ context.Context, id string, signCount uint32, lastUsedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return errors.New("device not found")
	}
	d.SignCount = signCount
	d.LastUsedAt = lastUsedAt
	m.devices[id] = d
	return nil
}

func (m *mockDeviceStore) DeleteDevice(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.devices, id)
	return nil
}

func (m *mockDeviceStore) CountDevices(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.devices), nil
}

func authCookie(s *Server) *http.Cookie {
	token := "valid-test-serve-token-12345"
	s.serveSessionsMu.Lock()
	s.serveSessions[token] = time.Now().Add(24 * time.Hour)
	s.serveSessionsMu.Unlock()
	return &http.Cookie{
		Name:  serveSessionCookie,
		Value: token,
		Path:  "/",
	}
}

func TestDeviceManagementEndpoints(t *testing.T) {
	cfg := testConfig()
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "password"

	devStore := newMockDeviceStore()
	s := newTestServer(t, serverOpts{
		cfg:         &cfg,
		deviceStore: devStore,
	})
	defer s.Close()

	// 1. GET /api/devices when unauthenticated -> returns count 0, devices []
	req := localRequest(http.MethodGet, "/api/devices")
	rec := do(t, s, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var res map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&res)
	if res["count"].(float64) != 0 || res["has_devices"].(bool) != false {
		t.Errorf("unexpected unauthenticated response: %v", res)
	}

	// 2. POST /api/devices/pair-token when unauthenticated -> 401
	rec = do(t, s, localRequest(http.MethodPost, "/api/devices/pair-token"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 3. POST /api/devices/pair-token when authenticated -> 200 with token and URL
	req = localRequest(http.MethodPost, "/api/devices/pair-token")
	req.AddCookie(authCookie(s))
	rec = do(t, s, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var pairRes struct {
		OK        bool   `json:"ok"`
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
		URL       string `json:"url"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&pairRes)
	if !pairRes.OK || pairRes.Token == "" || pairRes.URL == "" {
		t.Errorf("invalid pair token response: %+v", pairRes)
	}

	// 4. DELETE /api/devices/{id} when unauthenticated -> 401
	rec = do(t, s, localRequest(http.MethodDelete, "/api/devices/test-dev"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// Add a dummy device to the store
	_ = devStore.SaveDevice(context.Background(), store.Device{
		ID:         "dev-1",
		DeviceName: "MacBook Pro",
		PublicKey:  "pubkey",
		Algorithm:  -7,
		SignCount:  1,
	})

	// 5. GET /api/devices when authenticated -> returns device list
	req = localRequest(http.MethodGet, "/api/devices")
	req.AddCookie(authCookie(s))
	rec = do(t, s, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var listRes struct {
		OK      bool           `json:"ok"`
		Count   int            `json:"count"`
		Devices []store.Device `json:"devices"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&listRes)
	if !listRes.OK || listRes.Count != 1 || len(listRes.Devices) != 1 {
		t.Fatalf("unexpected authenticated device list: %+v", listRes)
	}
	if listRes.Devices[0].DeviceName != "MacBook Pro" {
		t.Errorf("device name = %q, want MacBook Pro", listRes.Devices[0].DeviceName)
	}

	// 6. DELETE /api/devices/{id} when authenticated -> 200
	req = localRequest(http.MethodDelete, "/api/devices/dev-1")
	req.AddCookie(authCookie(s))
	rec = do(t, s, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	cnt, _ := devStore.CountDevices(context.Background())
	if cnt != 0 {
		t.Errorf("device not deleted from store, count = %d", cnt)
	}
}

func makeTestAttestation(privKey *ecdsa.PrivateKey, rpID string, credID []byte) (string, string) {
	rpHash := sha256.Sum256([]byte(rpID))
	authData := make([]byte, 37)
	copy(authData[0:32], rpHash[:])
	authData[32] = 0x01 | 0x04 | 0x40 // UP | UV | AT
	binary.BigEndian.PutUint32(authData[33:37], 0)

	aaguid := make([]byte, 16)
	authData = append(authData, aaguid...)

	var credLenBytes [2]byte
	binary.BigEndian.PutUint16(credLenBytes[:], uint16(len(credID)))
	authData = append(authData, credLenBytes[:]...)
	authData = append(authData, credID...)

	//nolint:staticcheck // Extracting coordinates for synthesizing test authenticator data
	xBytes := privKey.PublicKey.X.Bytes()
	//nolint:staticcheck // Extracting coordinates for synthesizing test authenticator data
	yBytes := privKey.PublicKey.Y.Bytes()
	if len(xBytes) < 32 {
		pad := make([]byte, 32-len(xBytes))
		xBytes = append(pad, xBytes...)
	}
	if len(yBytes) < 32 {
		pad := make([]byte, 32-len(yBytes))
		yBytes = append(pad, yBytes...)
	}

	coseMap := map[any]any{
		int64(1):  int64(2),
		int64(3):  int64(-7),
		int64(-1): int64(1),
		int64(-2): xBytes,
		int64(-3): yBytes,
	}
	coseBytes, _ := encodeTestCBOR(coseMap)
	authData = append(authData, coseBytes...)

	attObjMap := map[any]any{
		"fmt":      "none",
		"attStmt":  map[any]any{},
		"authData": authData,
	}
	attObjBytes, _ := encodeTestCBOR(attObjMap)
	return base64.RawURLEncoding.EncodeToString(authData), base64.RawURLEncoding.EncodeToString(attObjBytes)
}

func TestWebAuthnFullRegistrationAndLoginHandlers(t *testing.T) {
	cfg := testConfig()
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "password"

	devStore := newMockDeviceStore()
	s := newTestServer(t, serverOpts{
		cfg:         &cfg,
		deviceStore: devStore,
	})
	defer s.Close()

	// 1. BeginRegistration when unauthenticated without pair token -> 401
	rec := do(t, s, localRequest(http.MethodPost, "/api/auth/webauthn/register/begin"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// Create pair token
	pTok, _, err := s.createPairToken()
	if err != nil {
		t.Fatal(err)
	}

	// 2. BeginRegistration with pair token header -> 200 OK
	req := localRequest(http.MethodPost, "/api/auth/webauthn/register/begin")
	req.Header.Set("X-Pair-Token", pTok)
	rec = do(t, s, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with pair token, got %d", rec.Code)
	}

	var regBegin struct {
		OK        bool                         `json:"ok"`
		Challenge string                       `json:"challenge"`
		Options   webauthn.RegistrationOptions `json:"options"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&regBegin); err != nil {
		t.Fatal(err)
	}
	if !regBegin.OK || regBegin.Challenge == "" {
		t.Fatalf("invalid regBegin: %+v", regBegin)
	}

	// 3. FinishRegistration with synthesized authenticator response
	privKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	credID := []byte("authenticator-credential-98765")
	_, attObjB64 := makeTestAttestation(privKey, "127.0.0.1", credID)

	clientDataJSON := map[string]any{
		"type":      "webauthn.create",
		"challenge": regBegin.Challenge,
		"origin":    "http://127.0.0.1:8080",
	}
	clientDataBytes, _ := json.Marshal(clientDataJSON)
	cDataB64 := base64.RawURLEncoding.EncodeToString(clientDataBytes)

	finishReqBody, _ := json.Marshal(map[string]any{
		"deviceName": "iPhone PWA",
		"pairToken":  pTok,
		"id":         base64.RawURLEncoding.EncodeToString(credID),
		"rawId":      base64.RawURLEncoding.EncodeToString(credID),
		"type":       "public-key",
		"response": map[string]any{
			"clientDataJSON":    cDataB64,
			"attestationObject": attObjB64,
		},
	})

	req = localRequestWithBody(http.MethodPost, "/api/auth/webauthn/register/finish", finishReqBody)
	req.Header.Set("X-Pair-Token", pTok)
	rec = do(t, s, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("FinishRegistration failed: %d body: %s", rec.Code, rec.Body.String())
	}

	var regFinRes struct {
		OK     bool         `json:"ok"`
		Device store.Device `json:"device"`
		Token  string       `json:"token"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&regFinRes)
	if !regFinRes.OK || regFinRes.Device.ID == "" || regFinRes.Token == "" {
		t.Fatalf("unexpected FinishRegistration response: %+v", regFinRes)
	}
	// Verify session cookie was set for paired device
	var foundCookie bool
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == serveSessionCookie && cookie.Value != "" {
			foundCookie = true
			break
		}
	}
	if !foundCookie {
		t.Error("serveSessionCookie was not set on paired device registration")
	}

	// 4. Login Begin (unauthenticated)
	rec = do(t, s, localRequest(http.MethodPost, "/api/auth/webauthn/login/begin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("Login begin failed: %d, body: %s", rec.Code, rec.Body.String())
	}
	var loginBegin struct {
		OK        bool                      `json:"ok"`
		Challenge string                    `json:"challenge"`
		Options   webauthn.AssertionOptions `json:"options"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&loginBegin)
	if !loginBegin.OK || loginBegin.Challenge == "" {
		t.Fatalf("invalid loginBegin: %+v", loginBegin)
	}

	// 5. Sign Assertion
	cDataAssertJSON := map[string]any{
		"type":      "webauthn.get",
		"challenge": loginBegin.Challenge,
		"origin":    "http://127.0.0.1:8080",
	}
	cDataAssertBytes, _ := json.Marshal(cDataAssertJSON)
	cDataAssertB64 := base64.RawURLEncoding.EncodeToString(cDataAssertBytes)

	rpHash := sha256.Sum256([]byte("127.0.0.1"))
	authDataAssert := make([]byte, 37)
	copy(authDataAssert[0:32], rpHash[:])
	authDataAssert[32] = 0x01 | 0x04
	binary.BigEndian.PutUint32(authDataAssert[33:37], 1)

	cDataHash := sha256.Sum256(cDataAssertBytes)
	signedData := append(authDataAssert, cDataHash[:]...)
	sigHash := sha256.Sum256(signedData)
	sigBytes, _ := ecdsa.SignASN1(rand.Reader, privKey, sigHash[:])

	loginFinBody, _ := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(credID),
		"rawId": base64.RawURLEncoding.EncodeToString(credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    cDataAssertB64,
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authDataAssert),
			"signature":         base64.RawURLEncoding.EncodeToString(sigBytes),
		},
	})

	rec = do(t, s, localRequestWithBody(http.MethodPost, "/api/auth/webauthn/login/finish", loginFinBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("Login finish failed: %d, body: %s", rec.Code, rec.Body.String())
	}
	var loginFinRes struct {
		OK         bool   `json:"ok"`
		Token      string `json:"token"`
		DeviceName string `json:"device_name"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&loginFinRes)
	if !loginFinRes.OK || loginFinRes.Token == "" {
		t.Fatalf("unexpected loginFinRes: %+v", loginFinRes)
	}

	// 6. Test login failure with bad signature
	badLoginFinBody, _ := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(credID),
		"rawId": base64.RawURLEncoding.EncodeToString(credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    cDataAssertB64,
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authDataAssert),
			"signature":         base64.RawURLEncoding.EncodeToString([]byte("invalid")),
		},
	})
	// Put a fresh challenge in s.webauthnChallenges
	s.storeWebAuthnChallenge(loginBegin.Challenge, false)
	rec = do(t, s, localRequestWithBody(http.MethodPost, "/api/auth/webauthn/login/finish", badLoginFinBody))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad signature, got %d", rec.Code)
	}
}

func TestNilDeviceStoreGracefulErrors(t *testing.T) {
	s := newTestServer(t, serverOpts{deviceStore: nil})
	defer s.Close()

	rec := do(t, s, localRequest(http.MethodGet, "/api/devices"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}

	rec = do(t, s, localRequest(http.MethodPost, "/api/auth/webauthn/register/begin"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}

	rec = do(t, s, localRequest(http.MethodPost, "/api/auth/webauthn/login/begin"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func encodeTestCBOR(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeTestCBORItem(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeTestCBORItem(buf *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case int64:
		if val >= 0 {
			if val < 24 {
				buf.WriteByte(byte(val))
			} else {
				buf.WriteByte(24)
				buf.WriteByte(byte(val))
			}
		} else {
			n := -1 - val
			if n < 24 {
				buf.WriteByte(byte(0x20 | n))
			} else {
				buf.WriteByte(byte(0x20 | 24))
				buf.WriteByte(byte(n))
			}
		}
		return nil
	case []byte:
		l := len(val)
		if l < 24 {
			buf.WriteByte(byte(0x40 | l))
		} else if l <= 0xff {
			buf.WriteByte(byte(0x40 | 24))
			buf.WriteByte(byte(l))
		} else {
			buf.WriteByte(byte(0x40 | 25))
			var b [2]byte
			binary.BigEndian.PutUint16(b[:], uint16(l))
			buf.Write(b[:])
		}
		buf.Write(val)
		return nil
	case string:
		l := len(val)
		if l < 24 {
			buf.WriteByte(byte(0x60 | l))
		} else {
			buf.WriteByte(byte(0x60 | 24))
			buf.WriteByte(byte(l))
		}
		buf.WriteString(val)
		return nil
	case map[any]any:
		l := len(val)
		if l < 24 {
			buf.WriteByte(byte(0xa0 | l))
		} else {
			buf.WriteByte(byte(0xa0 | 24))
			buf.WriteByte(byte(l))
		}
		for k, elem := range val {
			if err := encodeTestCBORItem(buf, k); err != nil {
				return err
			}
			if err := encodeTestCBORItem(buf, elem); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("cbor: unsupported encode type %T", v)
	}
}

func TestDeviceBindingBypassesServeLock(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "password123"

	devStore := newMockDeviceStore()
	s := newTestServer(t, serverOpts{cfg: &cfg, deviceStore: devStore, db: fakeDB{}})

	// 1. Unauthenticated request to /healthz without binding -> 401
	rec := do(t, s, localRequest(http.MethodGet, "/healthz"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth or binding, got %d", rec.Code)
	}

	// 2. Bind a device
	now := time.Now()
	testDev := store.Device{
		ID:         "dev-test-1234",
		DeviceName: "iPhone 15 Pro",
		PublicKey:  "bound-key",
		Algorithm:  0,
		SignCount:  1,
		CreatedAt:  now,
		LastUsedAt: now,
	}
	if err := devStore.SaveDevice(context.Background(), testDev); err != nil {
		t.Fatalf("save device: %v", err)
	}

	// 3. Request with scalper_device_binding cookie -> 200 OK without basic auth!
	reqBound := localRequest(http.MethodGet, "/healthz")
	reqBound.AddCookie(&http.Cookie{Name: "scalper_device_binding", Value: testDev.ID})
	recBound := do(t, s, reqBound)
	if recBound.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for bound device, got %d: %s", recBound.Code, recBound.Body.String())
	}

	// 4. GET / with bound device has ServeLocked = false in template data
	reqIndex := localRequest(http.MethodGet, "/")
	reqIndex.AddCookie(&http.Cookie{Name: "scalper_device_binding", Value: testDev.ID})
	recIndex := do(t, s, reqIndex)
	if recIndex.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for index, got %d", recIndex.Code)
	}
	body := recIndex.Body.String()
	if strings.Contains(body, `id="serve-auth-modal"`) {
		t.Errorf("serve-auth-modal should NOT be rendered when device is bound")
	}
}

func TestDeviceBindEndpoint(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "password123"

	devStore := newMockDeviceStore()
	s := newTestServer(t, serverOpts{cfg: &cfg, deviceStore: devStore, db: fakeDB{}})

	// 1. Unauthenticated bind without pairing token -> 401
	unauthBody, _ := json.Marshal(map[string]any{"device_name": "My Phone"})
	rec := do(t, s, localRequestWithBody(http.MethodPost, "/api/devices/bind", unauthBody))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated, got %d", rec.Code)
	}

	// 2. Generate pairing token
	token, _, err := s.createPairToken()
	if err != nil {
		t.Fatalf("create pair token: %v", err)
	}

	// 3. Bind using pairing token -> 200 OK, sets deviceBindingCookie and serveSessionCookie
	bindBody, _ := json.Marshal(map[string]any{
		"device_name": "My Android Phone",
		"pair_token":  token,
	})
	reqBind := localRequestWithBody(http.MethodPost, "/api/devices/bind", bindBody)
	reqBind.Header.Set("X-Pair-Token", token)
	recBind := do(t, s, reqBind)
	if recBind.Code != http.StatusOK {
		t.Fatalf("bind device failed, code %d: %s", recBind.Code, recBind.Body.String())
	}

	var res struct {
		OK     bool         `json:"ok"`
		Device store.Device `json:"device"`
		Token  string       `json:"token"`
	}
	if err := json.Unmarshal(recBind.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal bind response: %v", err)
	}
	if !res.OK || res.Device.ID == "" || res.Device.DeviceName != "My Android Phone" {
		t.Fatalf("unexpected bind result: %+v", res)
	}

	cookies := recBind.Result().Cookies()
	var hasBindingCookie, hasSessionCookie bool
	for _, c := range cookies {
		if c.Name == "scalper_device_binding" && c.Value == res.Device.ID {
			hasBindingCookie = true
		}
		if c.Name == "scalper_serve_session" && c.Value == res.Token {
			hasSessionCookie = true
		}
	}
	if !hasBindingCookie {
		t.Errorf("expected scalper_device_binding cookie to be set")
	}
	if !hasSessionCookie {
		t.Errorf("expected scalper_serve_session cookie to be set")
	}
}

func TestDeviceUnlockEndpoint(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "password123"

	devStore := newMockDeviceStore()
	s := newTestServer(t, serverOpts{cfg: &cfg, deviceStore: devStore, db: fakeDB{}})

	// 1. Missing device ID -> 400 Bad Request
	rec := do(t, s, localRequest(http.MethodPost, "/api/auth/device-unlock"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
	}

	// 2. Unknown device ID -> 401 Unauthorized
	reqUnknown := localRequestWithBody(http.MethodPost, "/api/auth/device-unlock", []byte(`{"device_id":"unknown-dev-id"}`))
	reqUnknown.Header.Set("Content-Type", "application/json")
	recUnknown := do(t, s, reqUnknown)
	if recUnknown.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unknown device, got %d", recUnknown.Code)
	}

	// 3. Register a bound device in store
	now := time.Now()
	testDev := store.Device{
		ID:         "dev-android-test-999",
		DeviceName: "Samsung Galaxy S24",
		PublicKey:  "bound-device",
		Algorithm:  0,
		SignCount:  1,
		CreatedAt:  now,
		LastUsedAt: now,
	}
	if err := devStore.SaveDevice(context.Background(), testDev); err != nil {
		t.Fatalf("save device: %v", err)
	}

	// 4. Valid unlock with JSON body -> 200 OK with token, sets cookies
	bodyBytes, _ := json.Marshal(map[string]any{"device_id": testDev.ID})
	reqValid := localRequestWithBody(http.MethodPost, "/api/auth/device-unlock", bodyBytes)
	reqValid.Header.Set("Content-Type", "application/json")
	recValid := do(t, s, reqValid)
	if recValid.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", recValid.Code, recValid.Body.String())
	}

	var res struct {
		OK         bool   `json:"ok"`
		Token      string `json:"token"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
	}
	if err := json.Unmarshal(recValid.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal unlock response: %v", err)
	}
	if !res.OK || res.Token == "" || res.DeviceID != testDev.ID || res.DeviceName != testDev.DeviceName {
		t.Fatalf("unexpected unlock payload: %+v", res)
	}

	// Verify session token is recorded and works
	if !s.validServeSession(res.Token) {
		t.Errorf("token %q is not marked valid in serveSessions", res.Token)
	}

	// Verify device usage was updated
	savedDev, _ := devStore.GetDevice(context.Background(), testDev.ID)
	if savedDev.LastUsedAt.Before(now) {
		t.Errorf("expected LastUsedAt to be updated")
	}

	// 5. Valid unlock via X-Device-ID header
	reqHdr := localRequest(http.MethodPost, "/api/auth/device-unlock")
	reqHdr.Header.Set("X-Device-ID", testDev.ID)
	recHdr := do(t, s, reqHdr)
	if recHdr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with X-Device-ID header, got %d: %s", recHdr.Code, recHdr.Body.String())
	}

	// 6. Valid unlock via scalper_device_binding cookie
	reqCookie := localRequest(http.MethodPost, "/api/auth/device-unlock")
	reqCookie.AddCookie(&http.Cookie{Name: "scalper_device_binding", Value: testDev.ID})
	recCookie := do(t, s, reqCookie)
	if recCookie.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with binding cookie, got %d: %s", recCookie.Code, recCookie.Body.String())
	}

	// 7. WebAuthn authenticator (PublicKey != "bound-device") must be rejected by device-unlock
	webAuthnDev := store.Device{
		ID:         "webauthn-credential-id-123",
		DeviceName: "MacBook Touch ID",
		PublicKey:  "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE...",
		Algorithm:  -7,
		SignCount:  1,
		CreatedAt:  now,
		LastUsedAt: now,
	}
	if err := devStore.SaveDevice(context.Background(), webAuthnDev); err != nil {
		t.Fatalf("save webauthn dev: %v", err)
	}
	reqWebAuthn := localRequestWithBody(http.MethodPost, "/api/auth/device-unlock", []byte(`{"device_id":"webauthn-credential-id-123"}`))
	reqWebAuthn.Header.Set("Content-Type", "application/json")
	recWebAuthn := do(t, s, reqWebAuthn)
	if recWebAuthn.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for WebAuthn device on device-unlock, got %d: %s", recWebAuthn.Code, recWebAuthn.Body.String())
	}

	// Verify isDeviceBound rejects WebAuthn devices
	reqHdrWebAuthn := localRequest(http.MethodGet, "/api/session")
	reqHdrWebAuthn.Header.Set("X-Device-ID", "webauthn-credential-id-123")
	recHdrWebAuthn := do(t, s, reqHdrWebAuthn)
	if recHdrWebAuthn.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for X-Device-ID with WebAuthn credential, got %d", recHdrWebAuthn.Code)
	}

	// 8. Expired device (> 90 days inactivity) must be rejected
	expiredDev := store.Device{
		ID:         "dev-expired-100-days",
		DeviceName: "Old Android Device",
		PublicKey:  "bound-device",
		Algorithm:  0,
		SignCount:  1,
		CreatedAt:  now.Add(-100 * 24 * time.Hour),
		LastUsedAt: now.Add(-95 * 24 * time.Hour),
	}
	if err := devStore.SaveDevice(context.Background(), expiredDev); err != nil {
		t.Fatalf("save expired dev: %v", err)
	}
	reqExpired := localRequestWithBody(http.MethodPost, "/api/auth/device-unlock", []byte(`{"device_id":"dev-expired-100-days"}`))
	reqExpired.Header.Set("Content-Type", "application/json")
	recExpired := do(t, s, reqExpired)
	if recExpired.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for expired device, got %d: %s", recExpired.Code, recExpired.Body.String())
	}
}

func TestHeaderBasedServeSession(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "password123"

	devStore := newMockDeviceStore()
	s := newTestServer(t, serverOpts{cfg: &cfg, deviceStore: devStore, db: fakeDB{}})

	// Create valid serve session
	token := "custom-serve-token-header-xyz"
	s.serveSessionsMu.Lock()
	s.serveSessions[token] = time.Now().Add(24 * time.Hour)
	s.serveSessionsMu.Unlock()

	// 1. Request to protected endpoint (POST /api/devices/pair-token) without auth -> 401
	recUnauth := do(t, s, localRequest(http.MethodPost, "/api/devices/pair-token"))
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recUnauth.Code)
	}

	// 2. Request using X-Serve-Session header -> 200 OK
	reqHeader := localRequest(http.MethodPost, "/api/devices/pair-token")
	reqHeader.Header.Set("X-Serve-Session", token)
	recHeader := do(t, s, reqHeader)
	if recHeader.Code != http.StatusOK {
		t.Fatalf("expected 200 with X-Serve-Session, got %d: %s", recHeader.Code, recHeader.Body.String())
	}

	// 3. Request using Authorization: Bearer <token> -> 200 OK
	reqBearer := localRequest(http.MethodPost, "/api/devices/pair-token")
	reqBearer.Header.Set("Authorization", "Bearer "+token)
	recBearer := do(t, s, reqBearer)
	if recBearer.Code != http.StatusOK {
		t.Fatalf("expected 200 with Bearer auth, got %d: %s", recBearer.Code, recBearer.Body.String())
	}

	// 4. Request with invalid X-Serve-Session -> 401
	reqInvalid := localRequest(http.MethodPost, "/api/devices/pair-token")
	reqInvalid.Header.Set("X-Serve-Session", "invalid-token-here")
	recInvalid := do(t, s, reqInvalid)
	if recInvalid.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid token, got %d", recInvalid.Code)
	}
}

func TestWebAuthnLoginBeginWithBoundDevice(t *testing.T) {
	cfg := testConfig()
	cfg.App.ServeMode = true
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "password123"

	devStore := newMockDeviceStore()
	s := newTestServer(t, serverOpts{cfg: &cfg, deviceStore: devStore, db: fakeDB{}})

	// Add only a bound-device (e.g. mobile device paired with QR code)
	devStore.SaveDevice(context.Background(), store.Device{
		ID:         "dev-paired-phone",
		DeviceName: "Paired Phone",
		PublicKey:  "bound-device",
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
	})

	// POST /api/auth/webauthn/login/begin
	rec := do(t, s, localRequest(http.MethodPost, "/api/auth/webauthn/login/begin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK             bool `json:"ok"`
		HasWebAuthn    bool `json:"has_webauthn"`
		HasBoundDevice bool `json:"has_bound_device"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected ok: true")
	}
	if res.HasWebAuthn {
		t.Errorf("expected has_webauthn to be false for bound-device only")
	}
	if !res.HasBoundDevice {
		t.Errorf("expected has_bound_device to be true")
	}
}
