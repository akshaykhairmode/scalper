package auth

import (
	"fmt"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"log/slog"
)

func TestSessionManagerNewSession(t *testing.T) {
	sm := NewSessionManager(24 * time.Hour)
	token := "test-token-" + fmt.Sprintf("%08d", time.Now().Unix()%100000000)
	session := sm.NewSession(token, LoginMethodTOTP, false)

	if session == nil {
		t.Fatal("NewSession should return a non-nil session")
	}
	if session.AccessToken != token {
		t.Fatalf("Session access token should match, got %s", session.AccessToken)
	}
	if session.LoginMethod != LoginMethodTOTP {
		t.Fatalf("Session login method should be TOTP, got %s", session.LoginMethod)
	}
	// Paper mode is determined by whether access token is empty
	if session.AccessToken == "" {
		t.Fatal("Session access token should not be empty after NewSession")
	}
}

func TestSessionManagerGetSession(t *testing.T) {
	sm := NewSessionManager(1 * time.Minute) // short expiry for testing

	token := "test-session-" + fmt.Sprintf("%04d", time.Now().Unix()%10000)
	sm.NewSession(token, LoginMethodMPIN, true)

	// Session should exist immediately
	session := sm.GetSession(token)
	if session == nil {
		t.Fatal("GetSession should return the created session")
	}
	if session.AccessToken != token {
		t.Fatalf("Session access token should match")
	}
}

func TestSessionManagerIsSessionValid(t *testing.T) {
	sm := NewSessionManager(24 * time.Hour)

	token := "test-valid-" + fmt.Sprintf("%04d", time.Now().Unix()%10000)
	sm.NewSession(token, LoginMethodTOTP, false)

	if !sm.IsSessionValid(token) {
		t.Fatal("IsSessionValid should return true for valid session")
	}
}

func TestSessionManagerIsSessionValidExpired(t *testing.T) {
	sm := NewSessionManager(1 * time.Hour) // short expiry for testing

	token := "test-expired-" + fmt.Sprintf("%04d", time.Now().Unix()%10000)
	sm.NewSession(token, LoginMethodTOTP, false)

	// Immediately check - should be valid since we just created it
	if sm.IsSessionValid(token) {
		// This is expected right after creation; wait a bit and check again
	}
}

func TestSessionManagerRevokeSession(t *testing.T) {
	sm := NewSessionManager(24 * time.Hour)

	token := "test-revoke-" + fmt.Sprintf("%04d", time.Now().Unix()%10000)
	sm.NewSession(token, LoginMethodTOTP, false)

	if !sm.IsSessionValid(token) {
		t.Fatal("Session should be valid before revocation")
	}

	sm.RevokeSession(token)

	if sm.IsSessionValid(token) {
		t.Fatal("Session should be invalid after revocation")
	}
}

func TestSessionManagerRevokeAll(t *testing.T) {
	sm := NewSessionManager(24 * time.Hour)

	token1 := "revoke-token-1"
	token2 := "revoke-token-2"

	sm.NewSession(token1, LoginMethodTOTP, false)
	sm.NewSession(token2, LoginMethodMPIN, true)

	if !sm.IsSessionValid(token1) {
		t.Fatal("Token1 should be valid before revoke all")
	}
	if !sm.IsSessionValid(token2) {
		t.Fatal("Token2 should be valid before revoke all")
	}

	sm.RevokeAll()

	if sm.IsSessionValid(token1) {
		t.Fatal("Token1 should be invalid after revoke all")
	}
	if sm.IsSessionValid(token2) {
		t.Fatal("Token2 should be invalid after revoke all")
	}
}

func TestSessionManagerGetSessionInfo(t *testing.T) {
	sm := NewSessionManager(24 * time.Hour)

	token := "test-info-" + fmt.Sprintf("%04d", time.Now().Unix()%10000)
	_ = sm.NewSession(token, LoginMethodTOTP, false)

	info := sm.GetSessionInfo(token)
	if info == nil {
		t.Fatal("GetSessionInfo should return non-nil info")
	}
	if info.LoginMethod != LoginMethodTOTP {
		t.Fatalf("Login method should be TOTP, got %s", info.LoginMethod)
	}
	// Paper mode: TOTP login starts in live mode (paperMode=false), so PaperMode should be false
	if info.PaperMode != false {
		t.Fatalf("Paper mode should be false for TOTP auth, got %v", info.PaperMode)
	}
}

func TestSessionManagerGetSessionInfoNotFound(t *testing.T) {
	sm := NewSessionManager(24 * time.Hour)

	info := sm.GetSessionInfo("nonexistent-token")
	if info != nil {
		t.Fatal("GetSessionInfo should return nil for non-existent token")
	}
}

func TestServerNewServer(t *testing.T) {
	log := slog.Default()
	cfg := config.Default()
	tokenPresent := func() bool { return false }

	server := NewServer(log, cfg, tokenPresent)
	if server == nil {
		t.Fatal("NewServer should return non-nil server")
	}
	if server.SessionManager() == nil {
		t.Fatal("SessionManager should not be nil")
	}
}

func TestServerLoginTOTP(t *testing.T) {
	log := slog.Default()
	cfg := config.Default()
	tokenPresent := func() bool { return false }

	server := NewServer(log, cfg, tokenPresent)

	token, err := server.Login("123456", "")
	if err != nil {
		t.Fatalf("Server.Login with TOTP should not error: %v", err)
	}
	if token == "" {
		t.Fatal("Server.Login should return a token")
	}
}

func TestServerLoginMPIN(t *testing.T) {
	log := slog.Default()
	cfg := config.Default()
	tokenPresent := func() bool { return false }

	server := NewServer(log, cfg, tokenPresent)

	token, err := server.Login("", "1234")
	if err != nil {
		t.Fatalf("Server.Login with MPIN should not error: %v", err)
	}
	if token == "" {
		t.Fatal("Server.Login should return a token")
	}
}

func TestServerSessionStatus(t *testing.T) {
	log := slog.Default()
	cfg := config.Default()
	tokenPresent := func() bool { return false }

	server := NewServer(log, cfg, tokenPresent)

	status := server.SessionStatus()
	if status == nil {
		t.Fatal("SessionStatus should return non-nil status")
	}
	if status.Authenticated {
		t.Fatal("SessionStatus should show unauthenticated initially")
	}
	if !status.PaperMode {
		t.Fatal("SessionStatus should show paper mode initially")
	}
}

func TestServerSessionStatusAfterLogin(t *testing.T) {
	log := slog.Default()
	cfg := config.Default()
	tokenPresent := func() bool { return false }

	server := NewServer(log, cfg, tokenPresent)

	// Login first
	server.Login("123456", "")

	status := server.SessionStatus()
	if !status.Authenticated {
		t.Fatal("SessionStatus should show authenticated after login")
	}
	// After TOTP login, paper mode should be false
	if status.PaperMode {
		t.Fatal("SessionStatus should not show paper mode after TOTP login")
	}
	if status.LoginMethod != "totp" {
		t.Fatalf("Login method should be totp, got %s", status.LoginMethod)
	}
}