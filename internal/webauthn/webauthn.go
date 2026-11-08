package webauthn

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
)

// Config configures the WebAuthn relying party.
type Config struct {
	RPID      string
	RPName    string
	Origins   []string
	RequireUV bool
}

// WebAuthn coordinates WebAuthn credential registration and assertion.
type WebAuthn struct {
	cfg Config
}

// New creates a new WebAuthn manager.
func New(cfg Config) *WebAuthn {
	if cfg.RPName == "" {
		cfg.RPName = "Scalper Terminal"
	}
	return &WebAuthn{cfg: cfg}
}

// RPEntity represents the Relying Party in WebAuthn options.
type RPEntity struct {
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

// UserEntity represents the user being registered.
type UserEntity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// PubKeyCredParam specifies an allowed public key credential parameter.
type PubKeyCredParam struct {
	Type string `json:"type"`
	Alg  int64  `json:"alg"`
}

// AuthenticatorSelection configures authenticator selection criteria.
type AuthenticatorSelection struct {
	AuthenticatorAttachment string `json:"authenticatorAttachment,omitempty"`
	UserVerification        string `json:"userVerification,omitempty"`
	ResidentKey             string `json:"residentKey,omitempty"`
	RequireResidentKey      bool   `json:"requireResidentKey,omitempty"`
}

// CredentialDescriptor identifies an allowed credential.
type CredentialDescriptor struct {
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	Transports []string `json:"transports,omitempty"`
}

// RegistrationOptions contains the options sent to navigator.credentials.create.
type RegistrationOptions struct {
	Challenge              string                 `json:"challenge"`
	RP                     RPEntity               `json:"rp"`
	User                   UserEntity             `json:"user"`
	PubKeyCredParams       []PubKeyCredParam      `json:"pubKeyCredParams"`
	AuthenticatorSelection AuthenticatorSelection `json:"authenticatorSelection"`
	Timeout                int                    `json:"timeout"`
	Attestation            string                 `json:"attestation"`
}

// AssertionOptions contains the options sent to navigator.credentials.get.
type AssertionOptions struct {
	Challenge        string                 `json:"challenge"`
	Timeout          int                    `json:"timeout"`
	RPID             string                 `json:"rpId,omitempty"`
	UserVerification string                 `json:"userVerification,omitempty"`
	AllowCredentials []CredentialDescriptor `json:"allowCredentials,omitempty"`
}

// RegistrationResponse is the payload received from navigator.credentials.create.
type RegistrationResponse struct {
	ID       string                   `json:"id"`
	RawID    string                   `json:"rawId"`
	Type     string                   `json:"type"`
	Response RegistrationResponseData `json:"response"`
}

// RegistrationResponseData contains the raw creation response from the authenticator.
type RegistrationResponseData struct {
	ClientDataJSON    string `json:"clientDataJSON"`
	AttestationObject string `json:"attestationObject"`
}

// AssertionResponse is the payload received from navigator.credentials.get.
type AssertionResponse struct {
	ID       string                `json:"id"`
	RawID    string                `json:"rawId"`
	Type     string                `json:"type"`
	Response AssertionResponseData `json:"response"`
}

// AssertionResponseData contains the assertion response from the authenticator.
type AssertionResponseData struct {
	ClientDataJSON    string `json:"clientDataJSON"`
	AuthenticatorData string `json:"authenticatorData"`
	Signature         string `json:"signature"`
	UserHandle        string `json:"userHandle,omitempty"`
}

// RegistrationResult contains the verified credential details to be saved.
type RegistrationResult struct {
	CredentialID string
	PublicKey    string // Base64URL encoded PKIX DER
	Algorithm    int64  // -7 (ES256)
	SignCount    uint32
	AAGUID       []byte
}

// StoredDevice holds the stored device details required for assertion verification.
type StoredDevice struct {
	ID        string
	PublicKey string
	Algorithm int64
	SignCount uint32
}

// AssertionResult contains verified assertion details.
type AssertionResult struct {
	CredentialID string
	SignCount    uint32
	UserVerified bool
}

type clientDataJSON struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// GenerateChallenge generates a cryptographically secure 32-byte URL-safe base64 challenge.
func GenerateChallenge() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("webauthn: generate challenge: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// DecodeBase64 flexibly decodes URL-safe or standard Base64, with or without padding.
func DecodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// BeginRegistration generates credential creation options for enrolling a new device.
func (w *WebAuthn) BeginRegistration(userID, userName, userDisplayName string) (*RegistrationOptions, error) {
	challenge, err := GenerateChallenge()
	if err != nil {
		return nil, err
	}

	if userID == "" {
		userID = "scalper-user"
	}
	if userName == "" {
		userName = "scalper-user"
	}
	if userDisplayName == "" {
		userDisplayName = "Scalper Terminal"
	}

	uidBytes := []byte(userID)
	opts := &RegistrationOptions{
		Challenge: challenge,
		RP: RPEntity{
			Name: w.cfg.RPName,
			ID:   w.cfg.RPID,
		},
		User: UserEntity{
			ID:          base64.RawURLEncoding.EncodeToString(uidBytes),
			Name:        userName,
			DisplayName: userDisplayName,
		},
		PubKeyCredParams: []PubKeyCredParam{
			{Type: "public-key", Alg: -7}, // ES256
		},
		AuthenticatorSelection: AuthenticatorSelection{
			UserVerification: "preferred",
			ResidentKey:      "preferred",
		},
		Timeout:     60000,
		Attestation: "none",
	}
	return opts, nil
}

// FinishRegistration parses and verifies the registration response.
func (w *WebAuthn) FinishRegistration(expectedChallenge string, res RegistrationResponse) (*RegistrationResult, error) {
	if res.Response.ClientDataJSON == "" || res.Response.AttestationObject == "" {
		return nil, errors.New("webauthn: missing clientDataJSON or attestationObject")
	}

	// 1. Parse clientDataJSON
	clientDataBytes, err := DecodeBase64(res.Response.ClientDataJSON)
	if err != nil {
		return nil, fmt.Errorf("webauthn: decode clientDataJSON: %w", err)
	}
	var cData clientDataJSON
	if err := json.Unmarshal(clientDataBytes, &cData); err != nil {
		return nil, fmt.Errorf("webauthn: unmarshal clientDataJSON: %w", err)
	}

	// 2. Verify type == "webauthn.create"
	if cData.Type != "webauthn.create" {
		return nil, fmt.Errorf("webauthn: invalid clientData type %q, want webauthn.create", cData.Type)
	}

	// 3. Verify challenge
	if subtle.ConstantTimeCompare([]byte(cData.Challenge), []byte(expectedChallenge)) != 1 {
		return nil, errors.New("webauthn: registration challenge mismatch")
	}

	// 4. Verify origin
	if err := w.verifyOrigin(cData.Origin); err != nil {
		return nil, err
	}

	// 5. Decode attestationObject CBOR
	attObjBytes, err := DecodeBase64(res.Response.AttestationObject)
	if err != nil {
		return nil, fmt.Errorf("webauthn: decode attestationObject: %w", err)
	}
	attVal, err := DecodeCBOR(attObjBytes)
	if err != nil {
		return nil, fmt.Errorf("webauthn: parse attestationObject CBOR: %w", err)
	}
	attMap, ok := attVal.(map[any]any)
	if !ok {
		return nil, errors.New("webauthn: attestationObject is not a map")
	}

	authDataRaw, ok := lookupStringKey(attMap, "authData")
	if !ok {
		return nil, errors.New("webauthn: missing authData in attestationObject")
	}
	authData, ok := authDataRaw.([]byte)
	if !ok {
		return nil, errors.New("webauthn: authData is not a byte slice")
	}

	// 6. Verify authData
	if len(authData) < 37 {
		return nil, errors.New("webauthn: authData too short")
	}

	// Verify RP ID hash if RPID is set
	if w.cfg.RPID != "" {
		expectedHash := sha256.Sum256([]byte(w.cfg.RPID))
		match := subtle.ConstantTimeCompare(authData[0:32], expectedHash[:]) == 1
		if !match && (w.cfg.RPID == "127.0.0.1" || w.cfg.RPID == "localhost") {
			alt := "localhost"
			if w.cfg.RPID == "localhost" {
				alt = "127.0.0.1"
			}
			altHash := sha256.Sum256([]byte(alt))
			match = subtle.ConstantTimeCompare(authData[0:32], altHash[:]) == 1
		}
		if !match {
			return nil, errors.New("webauthn: RP ID hash mismatch")
		}
	}

	flags := authData[32]
	// Verify UP flag (User Present, bit 0)
	if flags&0x01 == 0 {
		return nil, errors.New("webauthn: user present (UP) flag not set")
	}
	// Verify AT flag (Attested credential data included, bit 6)
	if flags&0x40 == 0 {
		return nil, errors.New("webauthn: attested credential data flag (AT) not set")
	}

	signCount := binary.BigEndian.Uint32(authData[33:37])

	// Attested credential data starts at offset 37:
	// 37..53: AAGUID (16 bytes)
	// 53..55: credential ID length (2 bytes, big-endian)
	if len(authData) < 55 {
		return nil, errors.New("webauthn: authData truncated before credential ID length")
	}
	aaguid := authData[37:53]
	credLen := int(binary.BigEndian.Uint16(authData[53:55]))
	if len(authData) < 55+credLen {
		return nil, errors.New("webauthn: authData truncated before credential ID")
	}
	credID := authData[55 : 55+credLen]
	coseKeyBytes := authData[55+credLen:]

	// 7. Parse COSE public key from coseKeyBytes
	coseVal, rem, err := decodeCBORItem(coseKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("webauthn: decode COSE key: %w", err)
	}
	_ = rem
	coseMap, ok := coseVal.(map[any]any)
	if !ok {
		return nil, errors.New("webauthn: COSE key is not a map")
	}

	// COSE key fields:
	// 1: kty (2 = EC2)
	// 3: alg (-7 = ES256)
	// -1: crv (1 = P-256)
	// -2: x coordinate ([]byte)
	// -3: y coordinate ([]byte)
	ktyVal, ok := lookupIntKey(coseMap, 1)
	if !ok {
		return nil, errors.New("webauthn: missing kty in COSE key")
	}
	kty, err := asInt64(ktyVal)
	if err != nil || kty != 2 {
		return nil, fmt.Errorf("webauthn: unsupported COSE key type: %v (want 2/EC2)", ktyVal)
	}

	algVal, ok := lookupIntKey(coseMap, 3)
	if !ok {
		return nil, errors.New("webauthn: missing alg in COSE key")
	}
	alg, err := asInt64(algVal)
	if err != nil || alg != -7 {
		return nil, fmt.Errorf("webauthn: unsupported COSE algorithm: %v (want -7/ES256)", algVal)
	}

	crvVal, ok := lookupIntKey(coseMap, -1)
	if !ok {
		return nil, errors.New("webauthn: missing crv in COSE key")
	}
	crv, err := asInt64(crvVal)
	if err != nil || crv != 1 {
		return nil, fmt.Errorf("webauthn: unsupported curve: %v (want 1/P-256)", crvVal)
	}

	xRaw, ok := lookupIntKey(coseMap, -2)
	if !ok {
		return nil, errors.New("webauthn: missing x coordinate in COSE key")
	}
	xBytes, ok := xRaw.([]byte)
	if !ok || len(xBytes) == 0 {
		return nil, errors.New("webauthn: invalid x coordinate in COSE key")
	}

	yRaw, ok := lookupIntKey(coseMap, -3)
	if !ok {
		return nil, errors.New("webauthn: missing y coordinate in COSE key")
	}
	yBytes, ok := yRaw.([]byte)
	if !ok || len(yBytes) == 0 {
		return nil, errors.New("webauthn: invalid y coordinate in COSE key")
	}

	curve := elliptic.P256()
	xInt := new(big.Int).SetBytes(xBytes)
	yInt := new(big.Int).SetBytes(yBytes)
	//nolint:staticcheck // Constructing P-256 public key from raw coordinates
	if !curve.IsOnCurve(xInt, yInt) {
		return nil, errors.New("webauthn: public key point is not on curve P-256")
	}

	//nolint:staticcheck // Constructing P-256 public key from raw coordinates
	pubKey := &ecdsa.PublicKey{
		Curve: curve,
		X:     xInt,
		Y:     yInt,
	}
	derBytes, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		return nil, fmt.Errorf("webauthn: marshal PKIX public key: %w", err)
	}

	credIDEncoded := base64.RawURLEncoding.EncodeToString(credID)
	// If the browser returned an ID, ensure it matches
	if res.ID != "" {
		resIDBytes, err := DecodeBase64(res.ID)
		if err == nil && !bytes.Equal(resIDBytes, credID) {
			return nil, errors.New("webauthn: credential ID mismatch between response and authData")
		}
	}

	return &RegistrationResult{
		CredentialID: credIDEncoded,
		PublicKey:    base64.RawURLEncoding.EncodeToString(derBytes),
		Algorithm:    alg,
		SignCount:    signCount,
		AAGUID:       aaguid,
	}, nil
}

// BeginAssertion generates credential assertion options for logging in.
func (w *WebAuthn) BeginAssertion(allowedCredentialIDs []string) (*AssertionOptions, error) {
	challenge, err := GenerateChallenge()
	if err != nil {
		return nil, err
	}

	var allowList []CredentialDescriptor
	for _, id := range allowedCredentialIDs {
		if strings.TrimSpace(id) != "" {
			allowList = append(allowList, CredentialDescriptor{
				Type: "public-key",
				ID:   id,
			})
		}
	}

	opts := &AssertionOptions{
		Challenge:        challenge,
		Timeout:          60000,
		RPID:             w.cfg.RPID,
		UserVerification: "preferred",
		AllowCredentials: allowList,
	}
	return opts, nil
}

// FinishAssertion parses and verifies an authentication assertion response.
func (w *WebAuthn) FinishAssertion(expectedChallenge string, storedDevice StoredDevice, res AssertionResponse) (*AssertionResult, error) {
	if res.Response.ClientDataJSON == "" || res.Response.AuthenticatorData == "" || res.Response.Signature == "" {
		return nil, errors.New("webauthn: missing clientDataJSON, authenticatorData, or signature")
	}

	// 1. Verify credential ID matches stored device
	resIDBytes, err := DecodeBase64(res.ID)
	if err != nil {
		return nil, fmt.Errorf("webauthn: decode credential ID: %w", err)
	}
	storedIDBytes, err := DecodeBase64(storedDevice.ID)
	if err == nil {
		if subtle.ConstantTimeCompare(resIDBytes, storedIDBytes) != 1 {
			return nil, errors.New("webauthn: credential ID mismatch")
		}
	} else if res.ID != storedDevice.ID {
		return nil, errors.New("webauthn: credential ID mismatch")
	}

	// 2. Parse clientDataJSON
	clientDataBytes, err := DecodeBase64(res.Response.ClientDataJSON)
	if err != nil {
		return nil, fmt.Errorf("webauthn: decode clientDataJSON: %w", err)
	}
	var cData clientDataJSON
	if err := json.Unmarshal(clientDataBytes, &cData); err != nil {
		return nil, fmt.Errorf("webauthn: unmarshal clientDataJSON: %w", err)
	}

	// 3. Verify type == "webauthn.get"
	if cData.Type != "webauthn.get" {
		return nil, fmt.Errorf("webauthn: invalid clientData type %q, want webauthn.get", cData.Type)
	}

	// 4. Verify challenge
	if subtle.ConstantTimeCompare([]byte(cData.Challenge), []byte(expectedChallenge)) != 1 {
		return nil, errors.New("webauthn: assertion challenge mismatch")
	}

	// 5. Verify origin
	if err := w.verifyOrigin(cData.Origin); err != nil {
		return nil, err
	}

	// 6. Parse and verify authenticatorData
	authData, err := DecodeBase64(res.Response.AuthenticatorData)
	if err != nil {
		return nil, fmt.Errorf("webauthn: decode authenticatorData: %w", err)
	}
	if len(authData) < 37 {
		return nil, errors.New("webauthn: authenticatorData too short")
	}

	// Verify RP ID hash
	if w.cfg.RPID != "" {
		expectedHash := sha256.Sum256([]byte(w.cfg.RPID))
		match := subtle.ConstantTimeCompare(authData[0:32], expectedHash[:]) == 1
		if !match && (w.cfg.RPID == "127.0.0.1" || w.cfg.RPID == "localhost") {
			alt := "localhost"
			if w.cfg.RPID == "localhost" {
				alt = "127.0.0.1"
			}
			altHash := sha256.Sum256([]byte(alt))
			match = subtle.ConstantTimeCompare(authData[0:32], altHash[:]) == 1
		}
		if !match {
			return nil, errors.New("webauthn: RP ID hash mismatch")
		}
	}

	flags := authData[32]
	// Verify UP flag (User Present, bit 0)
	if flags&0x01 == 0 {
		return nil, errors.New("webauthn: user present (UP) flag not set")
	}

	// Check UV flag (User Verified, bit 2)
	isUV := (flags & 0x04) != 0
	if w.cfg.RequireUV && !isUV {
		return nil, errors.New("webauthn: user verified (UV) flag required but not set")
	}

	// 7. Verify sign_count to prevent replay attacks (only when supported by authenticator)
	// Platform authenticators (Apple Touch ID, Face ID, Android Biometrics, Windows Hello)
	// do not increment signature counters and always return signCount == 0.
	// As per W3C WebAuthn spec, only check if both signCount and storedDevice.SignCount are non-zero.
	signCount := binary.BigEndian.Uint32(authData[33:37])
	if signCount > 0 && storedDevice.SignCount > 0 {
		if signCount <= storedDevice.SignCount {
			return nil, fmt.Errorf("webauthn: sign count did not increase (stored=%d, got=%d)", storedDevice.SignCount, signCount)
		}
	}

	// 8. Verify signature over authenticatorData || sha256(clientDataJSON)
	pubKeyBytes, err := DecodeBase64(storedDevice.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("webauthn: decode stored public key: %w", err)
	}
	parsedKey, err := x509.ParsePKIXPublicKey(pubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("webauthn: parse stored PKIX public key: %w", err)
	}
	ecdsaPub, ok := parsedKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("webauthn: stored key is not ECDSA")
	}

	clientDataHash := sha256.Sum256(clientDataBytes)
	signedData := make([]byte, 0, len(authData)+len(clientDataHash))
	signedData = append(signedData, authData...)
	signedData = append(signedData, clientDataHash[:]...)
	digest := sha256.Sum256(signedData)

	sigBytes, err := DecodeBase64(res.Response.Signature)
	if err != nil {
		return nil, fmt.Errorf("webauthn: decode signature: %w", err)
	}

	valid := ecdsa.VerifyASN1(ecdsaPub, digest[:], sigBytes)
	if !valid && len(sigBytes) == 64 {
		// Try raw IEEE P1363 (r || s)
		r := new(big.Int).SetBytes(sigBytes[:32])
		s := new(big.Int).SetBytes(sigBytes[32:])
		valid = ecdsa.Verify(ecdsaPub, digest[:], r, s)
	}
	if !valid {
		return nil, errors.New("webauthn: invalid assertion signature")
	}

	return &AssertionResult{
		CredentialID: res.ID,
		SignCount:    signCount,
		UserVerified: isUV,
	}, nil
}

func (w *WebAuthn) verifyOrigin(origin string) error {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return errors.New("webauthn: empty clientData origin")
	}

	// 1. Check against explicit allowed origins
	for _, o := range w.cfg.Origins {
		if o == origin {
			return nil
		}
	}

	// 2. Allow Capacitor and mobile webview schemes
	if strings.HasPrefix(origin, "capacitor://") ||
		strings.HasPrefix(origin, "ionic://") ||
		strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "https://localhost") {
		return nil
	}

	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("webauthn: parse origin URL %q: %w", origin, err)
	}

	// 3. Match hostname against RPID
	if w.cfg.RPID != "" {
		host := u.Hostname()
		if host == w.cfg.RPID {
			return nil
		}
		if (w.cfg.RPID == "localhost" || w.cfg.RPID == "127.0.0.1") && (host == "localhost" || host == "127.0.0.1") {
			return nil
		}
		return fmt.Errorf("webauthn: origin host %q does not match RP ID %q", host, w.cfg.RPID)
	}

	return nil
}
