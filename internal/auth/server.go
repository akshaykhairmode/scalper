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
	store         SessionStore
	pendingMu     sync.Mutex
	pendingLogins map[string]pendingLogin
}

// ServerOption configures an authentication Server.
type ServerOption func(*Server)

// WithSessionStore attaches a persistent store for broker sessions.
func WithSessionStore(store SessionStore) ServerOption {
	return func(s *Server) {
		s.store = store
		if s.sm != nil {
			s.sm.SetStore(store)
		}
		s.restoreSessionFromStore()
	}
}

// NewServer creates an authentication server backed by the Kotak API.
func NewServer(log *slog.Logger, cfg config.Config, consumerKey string, opts ...ServerOption) *Server {
	return NewServerWithAuthenticator(log, NewKotakAuthenticatorWithLogger(log,
		consumerKey, cfg.Kotak.MobileNumber, cfg.Kotak.UCC, cfg.Kotak.NeoFinKey,
	), opts...)
}

// NewServerWithAuthenticator creates an authentication server with a supplied authenticator.
func NewServerWithAuthenticator(log *slog.Logger, kotak KotakAuthenticator, opts ...ServerOption) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		log:           log,
		kotak:         kotak,
		sm:            NewSessionManager(SessionTTL),
		pendingLogins: make(map[string]pendingLogin),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (a *Server) restoreSessionFromStore() {
	if a.store == nil {
		return
	}
	sess, err := a.store.GetLatestSession(context.Background())
	if err != nil {
		a.log.Warn("failed to load persistent session", "event", "session_restore_failed", "error", err)
		return
	}
	if sess == nil {
		return
	}
	now := time.Now()
	if !sess.TokenExpiry.IsZero() && !now.Before(sess.TokenExpiry) {
		return
	}
	a.sm.RestoreSession(sess)
	a.log.Info("restored active Kotak session from database",
		"event", "session_restored",
		"login_method", sess.LoginMethod,
		"expires_in_min", int(time.Until(sess.TokenExpiry).Minutes()),
	)
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
// credentials are retained only in the server-side session manager and persistent store.
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
	if a.store != nil {
		_ = a.store.RevokeAllSessions(ctx)
	}
	session, err := a.sm.NewBrokerSession(trade, LoginMethodMPIN)
	if err != nil {
		current.validating = false
		a.pendingLogins[flowID] = current
		a.pendingMu.Unlock()
		return nil, fmt.Errorf("create authenticated session: %w", err)
	}
	delete(a.pendingLogins, flowID)
	a.pendingMu.Unlock()

	if a.store != nil {
		if err := a.store.SaveSession(ctx, *session); err != nil {
			a.log.Warn("failed to persist session to database", "error", err)
			return nil, fmt.Errorf("persist session: %w", err)
		}
	}

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

// CancelAllLogins discards all pending TOTP sessions.
func (a *Server) CancelAllLogins() {
	a.pendingMu.Lock()
	clear(a.pendingLogins)
	a.pendingMu.Unlock()
}

// RevokeAll revokes all active sessions in memory and persistent storage.
func (a *Server) RevokeAll(ctx context.Context) error {
	a.sm.RevokeAll()
	if a.store != nil {
		return a.store.RevokeAllSessions(ctx)
	}
	return nil
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
			sessionCopy := *session
			current = &sessionCopy
		}
	}
	return current
}

// Close shuts down the session manager background cleanup loop.
func (a *Server) Close() {
	if a != nil && a.sm != nil {
		a.sm.Close()
	}
}

// SessionStatus returns the current authentication status without exposing
// broker tokens, SIDs, or the dynamic API URL.
func (a *Server) SessionStatus() *SessionStatusResponse {
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

	status := &SessionStatusResponse{Authenticated: found}
	if found {
		status.LoginMethod = string(foundSession.LoginMethod)
		status.ExpiresIn = int(time.Until(foundSession.TokenExpiry).Minutes())
		remSecs := int(time.Until(foundSession.TokenExpiry).Seconds())
		if remSecs < 0 {
			remSecs = 0
		}
		status.ExpiresInSeconds = remSecs
		status.ExpiresAt = foundSession.TokenExpiry.UTC().Format(time.RFC3339)
	}
	return status
}
