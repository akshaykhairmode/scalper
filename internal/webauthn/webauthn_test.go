package webauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestCBORBasicTypes(t *testing.T) {
	// Integers: positive, small, medium, large, negative
	testCases := []int64{0, 1, 23, 24, 255, 256, 65535, 65536, 1000000, -1, -7, -24, -25, -1000}
	for _, tc := range testCases {
		b, err := EncodeCBOR(tc)
		if err != nil {
			t.Fatalf("EncodeCBOR(%d): %v", tc, err)
		}
		got, err := DecodeCBOR(b)
		if err != nil {
			t.Fatalf("DecodeCBOR(%d): %v", tc, err)
		}
		gotInt, ok := got.(int64)
		if !ok || gotInt != tc {
			t.Errorf("got %v (%T), want %d", got, got, tc)
		}
	}

	// Strings and bytes
	s := "hello world"
	bStr, err := EncodeCBOR(s)
	if err != nil {
		t.Fatal(err)
	}
	gotStr, err := DecodeCBOR(bStr)
	if err != nil || gotStr != s {
		t.Errorf("got %v, want %q", gotStr, s)
	}

	rawBytes := []byte{1, 2, 3, 4, 5, 254, 255}
	bBytes, err := EncodeCBOR(rawBytes)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := DecodeCBOR(bBytes)
	if err != nil {
		t.Fatal(err)
	}
	gb, ok := gotBytes.([]byte)
	if !ok || string(gb) != string(rawBytes) {
		t.Errorf("got %v, want %v", gotBytes, rawBytes)
	}

	// Booleans and Nil
	for _, val := range []any{true, false, nil} {
		b, err := EncodeCBOR(val)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeCBOR(b)
		if err != nil || got != val {
			t.Errorf("got %v, want %v", got, val)
		}
	}
}

func TestCBORMapsAndArrays(t *testing.T) {
	arr := []any{int64(1), "two", []byte{3, 4}}
	bArr, err := EncodeCBOR(arr)
	if err != nil {
		t.Fatal(err)
	}
	gotArrRaw, err := DecodeCBOR(bArr)
	if err != nil {
		t.Fatal(err)
	}
	gotArr, ok := gotArrRaw.([]any)
	if !ok || len(gotArr) != 3 {
		t.Fatalf("got array %v", gotArrRaw)
	}
	if gotArr[0].(int64) != 1 || gotArr[1].(string) != "two" {
		t.Errorf("unexpected array items: %v", gotArr)
	}

	// Map with mixed keys
	m := map[any]any{
		"fmt":      "none",
		int64(1):   int64(2),
		int64(-7):  "alg",
		"authData": []byte("dummy-data"),
	}
	bMap, err := EncodeCBOR(m)
	if err != nil {
		t.Fatal(err)
	}
	gotMapRaw, err := DecodeCBOR(bMap)
	if err != nil {
		t.Fatal(err)
	}
	gotMap, ok := gotMapRaw.(map[any]any)
	if !ok {
		t.Fatalf("got map %v", gotMapRaw)
	}
	val, ok := lookupStringKey(gotMap, "fmt")
	if !ok || val.(string) != "none" {
		t.Errorf("lookup fmt: %v", val)
	}
	iVal, ok := lookupIntKey(gotMap, 1)
	if !ok || iVal.(int64) != 2 {
		t.Errorf("lookup 1: %v", iVal)
	}
}

// helper to build authentic authenticatorData and attestationObject for tests
//
//nolint:unparam
func makeAttestationObject(t *testing.T, rpID string, privKey *ecdsa.PrivateKey, credID []byte, signCount uint32, up, at bool) ([]byte, []byte) {
	t.Helper()
	rpHash := sha256.Sum256([]byte(rpID))

	var flags byte
	if up {
		flags |= 0x01
	}
	flags |= 0x04 // UV
	if at {
		flags |= 0x40
	}

	authData := make([]byte, 37)
	copy(authData[0:32], rpHash[:])
	authData[32] = flags
	binary.BigEndian.PutUint32(authData[33:37], signCount)

	if at {
		// AAGUID (16 zeros)
		aaguid := make([]byte, 16)
		authData = append(authData, aaguid...)

		// Credential ID length
		var credLenBytes [2]byte
		binary.BigEndian.PutUint16(credLenBytes[:], uint16(len(credID)))
		authData = append(authData, credLenBytes[:]...)
		authData = append(authData, credID...)

		// COSE Key
		// 1: 2 (kty: EC2)
		// 3: -7 (alg: ES256)
		// -1: 1 (crv: P-256)
		// -2: xBytes
		// -3: yBytes
		//nolint:staticcheck // Extracting coordinates for synthesizing test authenticator data
		xBytes := privKey.PublicKey.X.Bytes()
		//nolint:staticcheck // Extracting coordinates for synthesizing test authenticator data
		yBytes := privKey.PublicKey.Y.Bytes()
		// ensure 32 bytes
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
		coseBytes, err := EncodeCBOR(coseMap)
		if err != nil {
			t.Fatalf("EncodeCBOR COSE: %v", err)
		}
		authData = append(authData, coseBytes...)
	}

	attObjMap := map[any]any{
		"fmt":      "none",
		"attStmt":  map[any]any{},
		"authData": authData,
	}
	attObjBytes, err := EncodeCBOR(attObjMap)
	if err != nil {
		t.Fatalf("EncodeCBOR attestationObject: %v", err)
	}

	return authData, attObjBytes
}

func TestWebAuthnRegistrationAndAssertionFlow(t *testing.T) {
	rpID := "localhost"
	origin := "http://localhost:8080"
	w := New(Config{
		RPID:    rpID,
		RPName:  "Scalper Test RP",
		Origins: []string{origin},
	})

	// Generate key pair representing the hardware authenticator
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	credID := []byte("my-test-cred-id-12345")

	// 1. BeginRegistration
	regOpts, err := w.BeginRegistration("usr-1", "alice", "Alice Scalper")
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	if regOpts.Challenge == "" {
		t.Fatal("empty challenge")
	}

	// Synthesize clientDataJSON
	cData := clientDataJSON{
		Type:      "webauthn.create",
		Challenge: regOpts.Challenge,
		Origin:    origin,
	}
	cDataBytes, _ := json.Marshal(cData)
	cDataB64 := base64.RawURLEncoding.EncodeToString(cDataBytes)

	_, attObjBytes := makeAttestationObject(t, rpID, privKey, credID, 0, true, true)
	attObjB64 := base64.RawURLEncoding.EncodeToString(attObjBytes)

	regResp := RegistrationResponse{
		ID:    base64.RawURLEncoding.EncodeToString(credID),
		RawID: base64.RawURLEncoding.EncodeToString(credID),
		Type:  "public-key",
		Response: RegistrationResponseData{
			ClientDataJSON:    cDataB64,
			AttestationObject: attObjB64,
		},
	}

	// 2. FinishRegistration
	regResult, err := w.FinishRegistration(regOpts.Challenge, regResp)
	if err != nil {
		t.Fatalf("FinishRegistration: %v", err)
	}
	if regResult.CredentialID != base64.RawURLEncoding.EncodeToString(credID) {
		t.Errorf("CredentialID = %q, want %q", regResult.CredentialID, base64.RawURLEncoding.EncodeToString(credID))
	}
	if regResult.Algorithm != -7 {
		t.Errorf("Algorithm = %d, want -7", regResult.Algorithm)
	}

	// Verify the stored public key works with x509
	pubDer, err := DecodeBase64(regResult.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	parsedPub, err := x509.ParsePKIXPublicKey(pubDer)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}
	ecdsaPub, ok := parsedPub.(*ecdsa.PublicKey)
	if !ok {
		t.Fatal("parsed key is not ECDSA")
	}
	if !ecdsaPub.Equal(&privKey.PublicKey) {
		t.Fatal("public key point mismatch")
	}

	// 3. BeginAssertion
	assertOpts, err := w.BeginAssertion([]string{regResult.CredentialID})
	if err != nil {
		t.Fatalf("BeginAssertion: %v", err)
	}
	if assertOpts.Challenge == "" {
		t.Fatal("empty assertion challenge")
	}

	// 4. Client signs assertion
	cDataAssert := clientDataJSON{
		Type:      "webauthn.get",
		Challenge: assertOpts.Challenge,
		Origin:    origin,
	}
	cDataAssertBytes, _ := json.Marshal(cDataAssert)
	cDataAssertB64 := base64.RawURLEncoding.EncodeToString(cDataAssertBytes)

	// Build assertion authenticatorData (37 bytes)
	rpHash := sha256.Sum256([]byte(rpID))
	authDataAssert := make([]byte, 37)
	copy(authDataAssert[0:32], rpHash[:])
	authDataAssert[32] = 0x01 | 0x04 // UP | UV
	binary.BigEndian.PutUint32(authDataAssert[33:37], 1)

	// Signature over authData || sha256(clientDataJSON)
	cDataHash := sha256.Sum256(cDataAssertBytes)
	signedData := append(authDataAssert, cDataHash[:]...)
	sigHash := sha256.Sum256(signedData)

	sigBytes, err := ecdsa.SignASN1(rand.Reader, privKey, sigHash[:])
	if err != nil {
		t.Fatalf("SignASN1: %v", err)
	}

	assertResp := AssertionResponse{
		ID:    regResult.CredentialID,
		RawID: regResult.CredentialID,
		Type:  "public-key",
		Response: AssertionResponseData{
			ClientDataJSON:    cDataAssertB64,
			AuthenticatorData: base64.RawURLEncoding.EncodeToString(authDataAssert),
			Signature:         base64.RawURLEncoding.EncodeToString(sigBytes),
		},
	}

	storedDev := StoredDevice{
		ID:        regResult.CredentialID,
		PublicKey: regResult.PublicKey,
		Algorithm: regResult.Algorithm,
		SignCount: 0,
	}

	assertResult, err := w.FinishAssertion(assertOpts.Challenge, storedDev, assertResp)
	if err != nil {
		t.Fatalf("FinishAssertion: %v", err)
	}
	if assertResult.SignCount != 1 {
		t.Errorf("SignCount = %d, want 1", assertResult.SignCount)
	}
	if !assertResult.UserVerified {
		t.Error("expected UserVerified to be true")
	}

	// 5. Test Replay Attack (same sign_count = 1 rejected)
	storedDev.SignCount = 1
	_, err = w.FinishAssertion(assertOpts.Challenge, storedDev, assertResp)
	if err == nil {
		t.Error("expected replay attack detection error, got nil")
	}

	// 6. Test Platform Authenticator with zero sign_count (Touch ID, Face ID, Android biometrics)
	// Platform authenticators always return signCount = 0 even if stored SignCount > 0.
	authDataZero := make([]byte, 37)
	copy(authDataZero[0:32], rpHash[:])
	authDataZero[32] = 0x05                            // UP + UV
	binary.BigEndian.PutUint32(authDataZero[33:37], 0) // signCount = 0
	signedDataZero := append(authDataZero, cDataHash[:]...)
	sigHashZero := sha256.Sum256(signedDataZero)
	sigBytesZero, err := ecdsa.SignASN1(rand.Reader, privKey, sigHashZero[:])
	if err != nil {
		t.Fatalf("SignASN1: %v", err)
	}
	assertRespZero := AssertionResponse{
		ID:    regResult.CredentialID,
		RawID: regResult.CredentialID,
		Type:  "public-key",
		Response: AssertionResponseData{
			ClientDataJSON:    cDataAssertB64,
			AuthenticatorData: base64.RawURLEncoding.EncodeToString(authDataZero),
			Signature:         base64.RawURLEncoding.EncodeToString(sigBytesZero),
		},
	}
	storedDev.SignCount = 5
	zeroResult, err := w.FinishAssertion(assertOpts.Challenge, storedDev, assertRespZero)
	if err != nil {
		t.Fatalf("expected TouchID/FaceID zero sign count to succeed, got %v", err)
	}
	if zeroResult.SignCount != 0 {
		t.Errorf("expected SignCount = 0, got %d", zeroResult.SignCount)
	}
}

func TestWebAuthnRegistrationEdgeCases(t *testing.T) {
	rpID := "localhost"
	origin := "http://localhost:8080"
	w := New(Config{RPID: rpID, Origins: []string{origin}})

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	credID := []byte("cred-test")

	// Missing data
	if _, err := w.FinishRegistration("challenge", RegistrationResponse{}); err == nil {
		t.Error("expected error for empty response")
	}

	// Challenge mismatch
	_, attObj := makeAttestationObject(t, rpID, privKey, credID, 0, true, true)
	cData := clientDataJSON{Type: "webauthn.create", Challenge: "different", Origin: origin}
	cDataBytes, _ := json.Marshal(cData)
	resp := RegistrationResponse{
		ID: base64.RawURLEncoding.EncodeToString(credID),
		Response: RegistrationResponseData{
			ClientDataJSON:    base64.RawURLEncoding.EncodeToString(cDataBytes),
			AttestationObject: base64.RawURLEncoding.EncodeToString(attObj),
		},
	}
	if _, err := w.FinishRegistration("expected", resp); err == nil {
		t.Error("expected challenge mismatch error")
	}

	// Wrong type (webauthn.get instead of webauthn.create)
	cData.Type = "webauthn.get"
	cData.Challenge = "expected"
	cDataBytes, _ = json.Marshal(cData)
	resp.Response.ClientDataJSON = base64.RawURLEncoding.EncodeToString(cDataBytes)
	if _, err := w.FinishRegistration("expected", resp); err == nil {
		t.Error("expected wrong type error")
	}

	// Origin mismatch
	cData.Type = "webauthn.create"
	cData.Origin = "https://evil.attacker.com"
	cDataBytes, _ = json.Marshal(cData)
	resp.Response.ClientDataJSON = base64.RawURLEncoding.EncodeToString(cDataBytes)
	if _, err := w.FinishRegistration("expected", resp); err == nil {
		t.Error("expected origin mismatch error")
	}

	// Missing UP flag
	_, attObjNoUP := makeAttestationObject(t, rpID, privKey, credID, 0, false, true)
	cData.Origin = origin
	cDataBytes, _ = json.Marshal(cData)
	resp.Response.ClientDataJSON = base64.RawURLEncoding.EncodeToString(cDataBytes)
	resp.Response.AttestationObject = base64.RawURLEncoding.EncodeToString(attObjNoUP)
	if _, err := w.FinishRegistration("expected", resp); err == nil {
		t.Error("expected missing UP flag error")
	}

	// Missing AT flag
	_, attObjNoAT := makeAttestationObject(t, rpID, privKey, credID, 0, true, false)
	resp.Response.AttestationObject = base64.RawURLEncoding.EncodeToString(attObjNoAT)
	if _, err := w.FinishRegistration("expected", resp); err == nil {
		t.Error("expected missing AT flag error")
	}
}

func TestWebAuthnAssertionEdgeCases(t *testing.T) {
	rpID := "localhost"
	origin := "http://localhost:8080"
	w := New(Config{RPID: rpID, Origins: []string{origin}, RequireUV: true})

	privKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	derBytes, _ := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	storedDev := StoredDevice{
		ID:        "cred-1",
		PublicKey: base64.RawURLEncoding.EncodeToString(derBytes),
		Algorithm: -7,
		SignCount: 5,
	}

	// Invalid signature
	cData := clientDataJSON{Type: "webauthn.get", Challenge: "chal", Origin: origin}
	cDataBytes, _ := json.Marshal(cData)
	authData := make([]byte, 37)
	rpHash := sha256.Sum256([]byte(rpID))
	copy(authData[:32], rpHash[:])
	authData[32] = 0x01 | 0x04 // UP | UV
	binary.BigEndian.PutUint32(authData[33:37], 10)

	resp := AssertionResponse{
		ID: storedDev.ID,
		Response: AssertionResponseData{
			ClientDataJSON:    base64.RawURLEncoding.EncodeToString(cDataBytes),
			AuthenticatorData: base64.RawURLEncoding.EncodeToString(authData),
			Signature:         base64.RawURLEncoding.EncodeToString([]byte("bad-signature")),
		},
	}
	if _, err := w.FinishAssertion("chal", storedDev, resp); err == nil {
		t.Error("expected invalid signature error")
	}

	// Credential ID mismatch
	resp.ID = "different-cred"
	if _, err := w.FinishAssertion("chal", storedDev, resp); err == nil {
		t.Error("expected credential ID mismatch error")
	}

	// Missing UV when RequireUV is true
	resp.ID = storedDev.ID
	authDataNoUV := make([]byte, 37)
	copy(authDataNoUV[:32], rpHash[:])
	authDataNoUV[32] = 0x01 // UP only, no UV
	binary.BigEndian.PutUint32(authDataNoUV[33:37], 10)

	// Valid signature but missing UV
	cDataHash := sha256.Sum256(cDataBytes)
	signedData := append(authDataNoUV, cDataHash[:]...)
	sigHash := sha256.Sum256(signedData)
	sigBytes, _ := ecdsa.SignASN1(rand.Reader, privKey, sigHash[:])

	resp.Response.AuthenticatorData = base64.RawURLEncoding.EncodeToString(authDataNoUV)
	resp.Response.Signature = base64.RawURLEncoding.EncodeToString(sigBytes)
	if _, err := w.FinishAssertion("chal", storedDev, resp); err == nil {
		t.Error("expected missing UV error")
	}
}

func TestRawP1363Signature(t *testing.T) {
	rpID := "localhost"
	origin := "http://localhost:8080"
	w := New(Config{RPID: rpID, Origins: []string{origin}})

	privKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	derBytes, _ := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	storedDev := StoredDevice{
		ID:        "cred-p1363",
		PublicKey: base64.RawURLEncoding.EncodeToString(derBytes),
		Algorithm: -7,
		SignCount: 0,
	}

	cData := clientDataJSON{Type: "webauthn.get", Challenge: "chal-p1363", Origin: origin}
	cDataBytes, _ := json.Marshal(cData)
	authData := make([]byte, 37)
	rpHash := sha256.Sum256([]byte(rpID))
	copy(authData[:32], rpHash[:])
	authData[32] = 0x01 | 0x04
	binary.BigEndian.PutUint32(authData[33:37], 1)

	cDataHash := sha256.Sum256(cDataBytes)
	signedData := append(authData, cDataHash[:]...)
	sigHash := sha256.Sum256(signedData)

	r, s, err := ecdsa.Sign(rand.Reader, privKey, sigHash[:])
	if err != nil {
		t.Fatal(err)
	}
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	rawSig := make([]byte, 64)
	copy(rawSig[32-len(rBytes):32], rBytes)
	copy(rawSig[64-len(sBytes):64], sBytes)

	resp := AssertionResponse{
		ID: storedDev.ID,
		Response: AssertionResponseData{
			ClientDataJSON:    base64.RawURLEncoding.EncodeToString(cDataBytes),
			AuthenticatorData: base64.RawURLEncoding.EncodeToString(authData),
			Signature:         base64.RawURLEncoding.EncodeToString(rawSig),
		},
	}

	res, err := w.FinishAssertion("chal-p1363", storedDev, resp)
	if err != nil {
		t.Fatalf("FinishAssertion with raw P1363 signature: %v", err)
	}
	if res.SignCount != 1 {
		t.Errorf("SignCount = %d, want 1", res.SignCount)
	}
}

func TestFlexibleBase64Decoding(t *testing.T) {
	data := []byte("hello-webauthn-base64-test-1234567890")
	std := base64.StdEncoding.EncodeToString(data)
	urlStd := base64.URLEncoding.EncodeToString(data)
	rawURL := base64.RawURLEncoding.EncodeToString(data)
	rawStd := base64.RawStdEncoding.EncodeToString(data)

	for _, s := range []string{std, urlStd, rawURL, rawStd} {
		decoded, err := DecodeBase64(s)
		if err != nil {
			t.Fatalf("DecodeBase64(%q): %v", s, err)
		}
		if string(decoded) != string(data) {
			t.Errorf("got %q, want %q", decoded, data)
		}
	}
}
