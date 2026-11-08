package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/auth"
)

func encryptGCM(key []byte, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if len(key) != 32 {
		return "", errors.New("store: invalid encryption key: 32 bytes required")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "enc:v1:" + hex.EncodeToString(sealed), nil
}

func decryptGCM(key []byte, ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, "enc:v1:") {
		return ciphertext, nil
	}
	if len(key) != 32 {
		return "", errors.New("invalid encryption key")
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(ciphertext, "enc:v1:"))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce, cipherBytes := raw[:nonceSize], raw[nonceSize:]
	plain, err := gcm.Open(nil, nonce, cipherBytes, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// SaveSession persists a Kotak broker session in the database.
func (s *Store) SaveSession(ctx context.Context, session auth.Session) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if strings.TrimSpace(session.AccessToken) == "" {
		return errors.New("store: session token ID is required")
	}

	createdAt := session.CreatedAt
	if createdAt.IsZero() {
		if s.now != nil {
			createdAt = s.now()
		} else {
			createdAt = time.Now()
		}
	}
	expiresAt := session.TokenExpiry
	if expiresAt.IsZero() {
		expiresAt = createdAt.Add(3 * time.Hour)
	}

	encToken, err := encryptGCM(s.key, session.BrokerToken)
	if err != nil {
		return fmt.Errorf("store: encrypt broker token: %w", err)
	}
	encSID, err := encryptGCM(s.key, session.BrokerSID)
	if err != nil {
		return fmt.Errorf("store: encrypt broker sid: %w", err)
	}

	query := `
		INSERT INTO sessions (
			token_id, broker_token, broker_sid, base_url, feed_url, login_method, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(token_id) DO UPDATE SET
			broker_token = excluded.broker_token,
			broker_sid   = excluded.broker_sid,
			base_url     = excluded.base_url,
			feed_url     = excluded.feed_url,
			login_method = excluded.login_method,
			created_at   = excluded.created_at,
			expires_at   = excluded.expires_at
	`
	_, err = s.db.ExecContext(ctx, query,
		session.AccessToken,
		encToken,
		encSID,
		session.BaseURL,
		session.FeedURL,
		string(session.LoginMethod),
		createdAt.UTC().Format("2006-01-02 15:04:05"),
		expiresAt.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return fmt.Errorf("store: save session: %w", err)
	}
	return nil
}

// GetLatestSession returns the most recently created session if it has not expired,
// or nil if there is no session or if the latest session has expired.
func (s *Store) GetLatestSession(ctx context.Context) (*auth.Session, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}

	query := `
		SELECT token_id, broker_token, broker_sid, base_url, feed_url, login_method, created_at, expires_at
		FROM sessions
		ORDER BY created_at DESC, rowid DESC
		LIMIT 1
	`
	var (
		tokenID      string
		brokerToken  string
		brokerSID    string
		baseURL      string
		feedURL      string
		loginMethod  string
		createdAtStr string
		expiresAtStr string
	)
	err := s.db.QueryRowContext(ctx, query).Scan(
		&tokenID,
		&brokerToken,
		&brokerSID,
		&baseURL,
		&feedURL,
		&loginMethod,
		&createdAtStr,
		&expiresAtStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get latest session: %w", err)
	}

	decToken, err := decryptGCM(s.key, brokerToken)
	if err != nil {
		return nil, fmt.Errorf("store: decrypt broker token: %w", err)
	}
	decSID, err := decryptGCM(s.key, brokerSID)
	if err != nil {
		return nil, fmt.Errorf("store: decrypt broker sid: %w", err)
	}

	createdAt := parseDBTime(createdAtStr)
	expiresAt := parseDBTime(expiresAtStr)

	now := time.Now()
	if s.now != nil {
		now = s.now()
	}

	if !expiresAt.IsZero() && (now.After(expiresAt) || now.Equal(expiresAt)) {
		return nil, nil
	}

	sess := &auth.Session{
		AccessToken:  tokenID,
		TokenExpiry:  expiresAt,
		LoginMethod:  auth.LoginMethod(loginMethod),
		BrokerToken:  decToken,
		BrokerSID:    decSID,
		BaseURL:      baseURL,
		FeedURL:      feedURL,
		CreatedAt:    createdAt,
		LastActivity: createdAt,
	}
	return sess, nil
}

// DeleteSession removes a session by its token ID.
func (s *Store) DeleteSession(ctx context.Context, tokenID string) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if strings.TrimSpace(tokenID) == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_id = ?`, tokenID); err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// RevokeAllSessions deletes all persistent sessions from the database.
func (s *Store) RevokeAllSessions(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return fmt.Errorf("store: revoke all sessions: %w", err)
	}
	return nil
}
