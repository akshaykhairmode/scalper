package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// SessionTTL defines the strict Kotak authentication session lifetime.
const SessionTTL = 3 * time.Hour

// SessionStore persists broker sessions across server restarts.
type SessionStore interface {
	SaveSession(ctx context.Context, s Session) error
	GetLatestSession(ctx context.Context) (*Session, error)
	DeleteSession(ctx context.Context, tokenID string) error
	RevokeAllSessions(ctx context.Context) error
}

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
	LoginMethod      LoginMethod `json:"login_method"`
	ExpiresIn        int         `json:"expires_in"`
	ExpiresInSeconds int         `json:"expires_in_seconds,omitempty"`
	ExpiresAt        string      `json:"expires_at,omitempty"`
}

// SessionStatusResponse is returned by SessionStatus.
type SessionStatusResponse struct {
	Authenticated    bool   `json:"authenticated"`
	LoginMethod      string `json:"login_method,omitempty"`
	ExpiresIn        int    `json:"expires_in,omitempty"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
}

// SessionManager manages authenticated sessions in memory and optionally persists them.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	maxAge   time.Duration
	stop     chan struct{}
	stopOnce sync.Once
	store    SessionStore
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

// SetStore sets or updates the persistent session store.
func (sm *SessionManager) SetStore(store SessionStore) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.store = store
}

// Close stops the background session expiration cleanup loop.
func (sm *SessionManager) Close() {
	sm.stopOnce.Do(func() {
		close(sm.stop)
	})
}

// RestoreSession stores an existing active session without modifying timestamps.
func (sm *SessionManager) RestoreSession(s *Session) {
	if s == nil || s.AccessToken == "" {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[s.AccessToken] = s
}

// NewBrokerSession stores the broker credentials in a new local session.
func (sm *SessionManager) NewBrokerSession(broker TradeSession, loginMethod LoginMethod) (*Session, error) {
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	now := time.Now()
	session := &Session{
		AccessToken:  hex.EncodeToString(id[:]),
		TokenExpiry:  now.Add(sm.maxAge),
		LoginMethod:  loginMethod,
		BrokerToken:  broker.Token,
		BrokerSID:    broker.SID,
		BaseURL:      broker.BaseURL,
		FeedURL:      broker.FeedURL,
		CreatedAt:    now,
		LastActivity: now,
	}
	sm.mu.Lock()
	sm.sessions[session.AccessToken] = session
	store := sm.store
	sm.mu.Unlock()

	if store != nil {
		_ = store.SaveSession(context.Background(), *session)
	}
	return session, nil
}

// RevokeAll revokes all sessions.
func (sm *SessionManager) RevokeAll() {
	sm.mu.Lock()
	sm.sessions = make(map[string]*Session)
	store := sm.store
	sm.mu.Unlock()

	if store != nil {
		_ = store.RevokeAllSessions(context.Background())
	}
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
	if s == nil {
		return nil
	}
	remSecs := int(time.Until(s.TokenExpiry).Seconds())
	if remSecs < 0 {
		remSecs = 0
	}
	return &SessionData{
		LoginMethod:      s.LoginMethod,
		ExpiresIn:        int(time.Until(s.TokenExpiry).Minutes()),
		ExpiresInSeconds: remSecs,
		ExpiresAt:        s.TokenExpiry.UTC().Format(time.RFC3339),
	}
}

// CleanupExpired removes sessions older than the given duration.
func (sm *SessionManager) CleanupExpired() {
	sm.mu.Lock()
	now := time.Now()
	var expiredIDs []string
	for key, session := range sm.sessions {
		if now.After(session.TokenExpiry) {
			delete(sm.sessions, key)
			expiredIDs = append(expiredIDs, key)
		}
	}
	store := sm.store
	sm.mu.Unlock()

	if store != nil {
		for _, id := range expiredIDs {
			_ = store.DeleteSession(context.Background(), id)
		}
	}
}
