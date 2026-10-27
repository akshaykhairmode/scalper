package auth

import (
	"sync"
	"time"
)

// LoginMethod represents the authentication method used.
type LoginMethod string

const (
	// LoginMethodTOTP is TOTP-based authentication.
	LoginMethodTOTP LoginMethod = "totp"
	// LoginMethodMPIN is MPIN-based authentication.
	LoginMethodMPIN LoginMethod = "mpin"
	// LoginMethodManual is manual username/MPIN login.
	LoginMethodManual LoginMethod = "manual"
)

// Session represents an authenticated user session.
type Session struct {
	AccessToken    string    `json:"access_token"`
	TokenExpiry    time.Time `json:"-"`
	TOTP           string    `json:"-"`
	MPINRedacted   string    `json:"-"`
	LoginMethod    LoginMethod `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	LastActivity   time.Time `json:"last_activity"`
}

// SessionData is the data returned to the client upon login.
type SessionData struct {
	SessionID     string    `json:"session_id"`
	AccessToken   string    `json:"access_token"`
	LoginMethod   LoginMethod `json:"login_method"`
	PaperMode     bool      `json:"paper_mode"`
	ExpiresIn     int       `json:"expires_in"`
}

// SessionManager manages authenticated sessions in memory.
type SessionManager struct {
	mu        sync.Mutex
	sessions  map[string]*Session
	maxAge    time.Duration
	cleanupMu sync.Mutex // protects cleanup goroutine
}

// NewSessionManager creates a new session manager with the given max age.
func NewSessionManager(maxAge time.Duration) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		maxAge:   maxAge,
	}
	go sm.cleanupLoop()
	return sm
}

// GetSession returns the session for the given access token, or nil if not found/expired.
func (sm *SessionManager) GetSession(accessToken string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, exists := sm.sessions[accessToken]
	if !exists {
		return nil
	}

	if time.Now().After(session.TokenExpiry) {
		delete(sm.sessions, accessToken)
		return nil
	}

	session.LastActivity = time.Now()
	return session
}

// NewSession creates a new session and stores it.
func (sm *SessionManager) NewSession(accessToken string, loginMethod LoginMethod, paperMode bool) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := &Session{
		AccessToken:    accessToken,
		TokenExpiry:    time.Now().Add(24 * time.Hour),
		TOTP:           "",
		MPINRedacted:   "***",
		LoginMethod:    loginMethod,
		CreatedAt:      time.Now(),
		LastActivity:   time.Now(),
	}

	sm.sessions[accessToken] = session
	return session
}

// RenewSession renews the token expiry for a session.
func (sm *SessionManager) RenewSession(accessToken string, newExpiry time.Time) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, exists := sm.sessions[accessToken]
	if !exists || time.Now().After(session.TokenExpiry) {
		return false
	}

	session.TokenExpiry = newExpiry
	session.LastActivity = time.Now()
	return true
}

// RevokeSession revokes a session.
func (sm *SessionManager) RevokeSession(accessToken string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, accessToken)
}

// RevokeAll revokes all sessions.
func (sm *SessionManager) RevokeAll() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions = make(map[string]*Session)
}

// IsSessionValid checks if a session exists and is not expired.
func (sm *SessionManager) IsSessionValid(accessToken string) bool {
	return sm.GetSession(accessToken) != nil
}

// SessionInfo returns limited session info for display.
type SessionInfo struct {
	LoginMethod LoginMethod `json:"login_method"`
	CreatedAt   string      `json:"created_at"`
	LastActivity string     `json:"last_activity"`
	PaperMode   bool        `json:"paper_mode"`
	ExpiresIn   int         `json:"expires_in"`
}

// GetSessionInfo returns limited session info.
func (sm *SessionManager) GetSessionInfo(accessToken string) *SessionInfo {
	session := sm.GetSession(accessToken)
	if session == nil {
		return nil
	}
	return &SessionInfo{
		LoginMethod: session.LoginMethod,
		CreatedAt:   session.CreatedAt.Format("2006-01-02 15:04:05"),
		LastActivity: session.LastActivity.Format("2006-01-02 15:04:05"),
		PaperMode:   session.AccessToken == "",
		ExpiresIn:   int(time.Until(session.TokenExpiry).Minutes()),
	}
}

// cleanupLoop periodically removes expired sessions.
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(sm.maxAge / 4)
	defer ticker.Stop()
	for range ticker.C {
		sm.RevokeAll()
	}
}

// ToSessionData converts a Session to a SessionData suitable for API responses.
func ToSessionData(s *Session) *SessionData {
	return &SessionData{
		SessionID:    s.AccessToken,
		AccessToken:  s.AccessToken,
		LoginMethod:  s.LoginMethod,
		PaperMode:    s.AccessToken == "",
		ExpiresIn:    0,
	}
}

// CleanupExpired removes sessions older than the given duration.
func (sm *SessionManager) CleanupExpired() {
	sm.cleanupMu.Lock()
	defer sm.cleanupMu.Unlock()
	sm.mu.Lock()
	now := time.Now()
	for key, session := range sm.sessions {
		if now.After(session.TokenExpiry) {
			delete(sm.sessions, key)
		}
	}
	sm.mu.Unlock()
}