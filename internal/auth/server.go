package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
)

const pendingLoginMaxAge = 5 * time.Minute

type pendingLogin struct {
	view       ViewSession
	expiresAt  time.Time
	validating bool
}

// Server performs Kotak authentication and manages the resulting local session.
type Server struct {
	log           *slog.Logger
	kotak         KotakAuthenticator
	sm            *SessionManager
	pendingMu     sync.Mutex
	pendingLogins map[string]pendingLogin
}

func NewServer(log *slog.Logger, cfg config.Config, accessToken string) *Server {
	return NewServerWithAuthenticator(log, NewKotakAuthenticatorWithLogger(log,
		accessToken, cfg.Kotak.MobileNumber, cfg.Kotak.UCC, cfg.Kotak.NeoFinKey,
	))
}

func NewServerWithAuthenticator(log *slog.Logger, kotak KotakAuthenticator) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		log:           log,
		kotak:         kotak,
		sm:            NewSessionManager(24 * time.Hour),
		pendingLogins: make(map[string]pendingLogin),
	}
}

// BeginLogin exchanges the TOTP and stores the short-lived view session on the
// server. flowID is a random, HttpOnly cookie value created by the web handler.
func (a *Server) BeginLogin(ctx context.Context, flowID, totp string) error {
	if flowID == "" {
		return &InputError{Message: "login flow is missing; start again"}
	}
	if err := validateDigits("TOTP", totp, 6); err != nil {
		return err
	}
	view, err := a.kotak.LoginTOTP(ctx, totp)
	if err != nil {
		return err
	}
	a.pendingMu.Lock()
	now := time.Now()
	for id, pending := range a.pendingLogins {
		if now.After(pending.expiresAt) {
			delete(a.pendingLogins, id)
		}
	}
	a.pendingLogins[flowID] = pendingLogin{
		view:      view,
		expiresAt: now.Add(pendingLoginMaxAge),
	}
	a.pendingMu.Unlock()
	a.log.Info("Kotak TOTP accepted; awaiting MPIN", "event", "auth_totp_success")
	return nil
}

// CompleteLogin exchanges the MPIN for the Kotak trade session. Broker
// credentials are retained only in the server-side session manager.
func (a *Server) CompleteLogin(ctx context.Context, flowID, mpin string) (*Session, error) {
	if err := validateDigits("MPIN", mpin, 6); err != nil {
		return nil, err
	}
	a.pendingMu.Lock()
	pending, ok := a.pendingLogins[flowID]
	if !ok || time.Now().After(pending.expiresAt) {
		delete(a.pendingLogins, flowID)
		a.pendingMu.Unlock()
		return nil, ErrNoPendingLogin
	}
	if pending.validating {
		a.pendingMu.Unlock()
		return nil, errors.New("MPIN validation is already in progress")
	}
	pending.validating = true
	a.pendingLogins[flowID] = pending
	a.pendingMu.Unlock()

	trade, err := a.kotak.ValidateMPIN(ctx, pending.view, mpin)
	if err != nil {
		a.resetPendingValidation(flowID)
		return nil, err
	}

	a.pendingMu.Lock()
	current, ok := a.pendingLogins[flowID]
	if !ok {
		a.pendingMu.Unlock()
		return nil, ErrNoPendingLogin
	}
	if !current.validating || current.view != pending.view ||
		!current.expiresAt.Equal(pending.expiresAt) {
		a.pendingMu.Unlock()
		return nil, ErrNoPendingLogin
	}
	if time.Now().After(current.expiresAt) {
		delete(a.pendingLogins, flowID)
		a.pendingMu.Unlock()
		return nil, ErrNoPendingLogin
	}
	a.sm.RevokeAll()
	session, err := a.sm.NewBrokerSession(trade, LoginMethodMPIN)
	if err != nil {
		current.validating = false
		a.pendingLogins[flowID] = current
		a.pendingMu.Unlock()
		return nil, fmt.Errorf("create authenticated session: %w", err)
	}
	delete(a.pendingLogins, flowID)
	a.pendingMu.Unlock()
	a.log.Info("Kotak login successful", "event", "auth_login_success")
	return session, nil
}

func (a *Server) resetPendingValidation(flowID string) {
	a.pendingMu.Lock()
	if pending, ok := a.pendingLogins[flowID]; ok {
		pending.validating = false
		a.pendingLogins[flowID] = pending
	}
	a.pendingMu.Unlock()
}

func (a *Server) CancelAllLogins() {
	a.pendingMu.Lock()
	clear(a.pendingLogins)
	a.pendingMu.Unlock()
}

func validateDigits(name, value string, length int) error {
	if len(value) != length {
		return &InputError{Message: fmt.Sprintf("invalid %s: expected %d digits", name, length)}
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return &InputError{Message: fmt.Sprintf("invalid %s: must be numeric", name)}
		}
	}
	return nil
}

// SessionManager returns the session manager.
func (a *Server) SessionManager() *SessionManager {
	return a.sm
}

// CurrentSession returns a copy of the most recently authenticated broker
// session. Broker credentials are never included in HTTP responses or logs.
func (a *Server) CurrentSession() *Session {
	a.sm.mu.Lock()
	defer a.sm.mu.Unlock()
	now := time.Now()
	var current *Session
	for _, session := range a.sm.sessions {
		if now.After(session.TokenExpiry) {
			continue
		}
		if current == nil || session.CreatedAt.After(current.CreatedAt) {
			copy := *session
			current = &copy
		}
	}
	return current
}

// SessionStatus returns the current authentication status without exposing
// broker tokens, SIDs, or the dynamic API URL.
func (a *Server) SessionStatus() *struct {
	Authenticated bool   `json:"authenticated"`
	PaperMode     bool   `json:"paper_mode"`
	LoginMethod   string `json:"login_method"`
	ExpiresIn     int    `json:"expires_in"`
} {
	a.sm.mu.Lock()
	now := time.Now()
	var foundSession Session
	found := false
	for _, session := range a.sm.sessions {
		if !now.After(session.TokenExpiry) &&
			(!found || session.CreatedAt.After(foundSession.CreatedAt)) {
			foundSession = *session
			found = true
		}
	}
	a.sm.mu.Unlock()

	status := &struct {
		Authenticated bool   `json:"authenticated"`
		PaperMode     bool   `json:"paper_mode"`
		LoginMethod   string `json:"login_method"`
		ExpiresIn     int    `json:"expires_in"`
	}{Authenticated: found, PaperMode: true}
	if found {
		status.LoginMethod = string(foundSession.LoginMethod)
		status.ExpiresIn = int(time.Until(foundSession.TokenExpiry).Minutes())
	}
	return status
}
