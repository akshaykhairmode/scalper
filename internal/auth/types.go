package auth

import (
	"crypto/rand"
	"encoding/hex"
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
	AccessToken  string      `json:"-"`
	TokenExpiry  time.Time   `json:"-"`
	TOTP         string      `json:"-"`
	MPINRedacted string      `json:"-"`
	LoginMethod  LoginMethod `json:"-"`
	BrokerToken  string      `json:"-"`
	BrokerSID    string      `json:"-"`
	BaseURL      string      `json:"-"`
	FeedURL      string      `json:"-"`
	CreatedAt    time.Time   `json:"created_at"`
	LastActivity time.Time   `json:"last_activity"`
}

// SessionData is the data returned to the client upon login.
type SessionData struct {
	LoginMethod LoginMethod `json:"login_method"`
	ExpiresIn   int         `json:"expires_in"`
}

// SessionManager manages authenticated sessions in memory.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	maxAge   time.Duration
	stop     chan struct{}
	stopOnce sync.Once
}

// NewSessionManager creates a new session manager with the given max age.
func NewSessionManager(maxAge time.Duration) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		maxAge:   maxAge,
		stop:     make(chan struct{}),
	}
	go sm.cleanupLoop()
	return sm
}

// Close stops the background session expiration cleanup loop.
func (sm *SessionManager) Close() {
	sm.stopOnce.Do(func() {
		close(sm.stop)
	})
}

// NewBrokerSession stores the broker credentials in a new local session.
func (sm *SessionManager) NewBrokerSession(broker TradeSession, loginMethod LoginMethod) (*Session, error) {
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	session := &Session{
		AccessToken:  hex.EncodeToString(id[:]),
		TokenExpiry:  time.Now().Add(sm.maxAge),
		LoginMethod:  loginMethod,
		BrokerToken:  broker.Token,
		BrokerSID:    broker.SID,
		BaseURL:      broker.BaseURL,
		FeedURL:      broker.FeedURL,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
	}
	sm.mu.Lock()
	sm.sessions[session.AccessToken] = session
	sm.mu.Unlock()
	return session, nil
}

// RevokeAll revokes all sessions.
func (sm *SessionManager) RevokeAll() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions = make(map[string]*Session)
}

// cleanupLoop periodically removes expired sessions.
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(sm.maxAge / 4)
	defer ticker.Stop()
	for {
		select {
		case <-sm.stop:
			return
		case <-ticker.C:
			sm.CleanupExpired()
		}
	}
}

// ToSessionData converts a Session to a SessionData suitable for API responses.
func ToSessionData(s *Session) *SessionData {
	return &SessionData{
		LoginMethod: s.LoginMethod,
		ExpiresIn:   int(time.Until(s.TokenExpiry).Minutes()),
	}
}

// CleanupExpired removes sessions older than the given duration.
func (sm *SessionManager) CleanupExpired() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	now := time.Now()
	for key, session := range sm.sessions {
		if now.After(session.TokenExpiry) {
			delete(sm.sessions, key)
		}
	}
}
