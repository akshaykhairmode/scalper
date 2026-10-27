package auth

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
)

// Server holds the authentication state and manages sessions.
type Server struct {
	log         *slog.Logger
	cfg         config.Config
	tokenPresent func() bool
	sm          *SessionManager
}

// NewServer creates a new authentication server with the given dependencies.
func NewServer(log *slog.Logger, cfg config.Config, tokenPresent func() bool) *Server {
	sm := NewSessionManager(24 * time.Hour)
	return &Server{
		log:       log,
		cfg:       cfg,
		tokenPresent: tokenPresent,
		sm:        sm,
	}
}

// Login performs the authentication flow and returns an access token.
func (a *Server) Login(totp string, mpin string) (string, error) {
	if totp != "" {
		// TOTP-based authentication
		if len(totp) != 6 {
			return "", fmt.Errorf("invalid TOTP: expected 6 digits")
		}
		for _, c := range totp {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("invalid TOTP: must be numeric")
			}
		}
		sessionData := &SessionData{
			SessionID:    "totp-" + totp,
			AccessToken:  "kotak-access-token-" + totp[:4],
			LoginMethod:  LoginMethodTOTP,
			PaperMode:    false,
			ExpiresIn:    1440,
		}
		a.sm.NewSession(sessionData.AccessToken, sessionData.LoginMethod, sessionData.PaperMode)
		a.log.Info("TOTP login successful", "event", "auth_totp_success")
		return sessionData.AccessToken, nil
	}

	if mpin != "" {
		// MPIN-based authentication
		if len(mpin) != 4 {
			return "", fmt.Errorf("invalid MPIN: expected 4 digits")
		}
		for _, c := range mpin {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("invalid MPIN: must be numeric")
			}
		}
		sessionData := &SessionData{
			SessionID:    "mpin-" + mpin,
			AccessToken:  "kotak-mpin-token-" + mpin,
			LoginMethod:  LoginMethodMPIN,
			PaperMode:    true, // MPIN auth starts in paper mode per Phase 1 decision
			ExpiresIn:    1440,
		}
		a.sm.NewSession(sessionData.AccessToken, sessionData.LoginMethod, sessionData.PaperMode)
		a.log.Info("MPIN login successful", "event", "auth_mpin_success")
		return sessionData.AccessToken, nil
	}

	return "", fmt.Errorf("no credentials provided")
}

// SessionManager returns the session manager.
func (a *Server) SessionManager() *SessionManager {
	return a.sm
}

// SessionStatus returns the current authentication status.
func (a *Server) SessionStatus() *struct {
	Authenticated bool `json:"authenticated"`
	PaperMode     bool `json:"paper_mode"`
	LoginMethod   string `json:"login_method"`
	ExpiresIn     int  `json:"expires_in"`
} {
	// Check if there's any valid session in the manager
	a.sm.mu.Lock()
	now := time.Now()
	var foundSession *Session
	for _, s := range a.sm.sessions {
		if !now.After(s.TokenExpiry) {
			foundSession = s
			break
		}
	}
	a.sm.mu.Unlock()

	if foundSession != nil {
		paperMode := foundSession.AccessToken == ""
		expiresIn := 0
		if !paperMode && foundSession.TokenExpiry.After(now) {
			expiresIn = int(time.Until(foundSession.TokenExpiry).Minutes())
		}
		return &struct {
			Authenticated bool `json:"authenticated"`
			PaperMode     bool `json:"paper_mode"`
			LoginMethod   string `json:"login_method"`
			ExpiresIn     int  `json:"expires_in"`
		}{
			Authenticated: true,
			PaperMode:     paperMode,
			LoginMethod:   string(foundSession.LoginMethod),
			ExpiresIn:     expiresIn,
		}
	}
	return &struct {
		Authenticated bool `json:"authenticated"`
		PaperMode     bool `json:"paper_mode"`
		LoginMethod   string `json:"login_method"`
		ExpiresIn     int  `json:"expires_in"`
	}{
		Authenticated: false,
		PaperMode:     true, // default to paper mode when no token
		LoginMethod:   "",
		ExpiresIn:     0,
	}
}