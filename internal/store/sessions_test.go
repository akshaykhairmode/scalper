package store

import (
	"context"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/auth"
)

func TestSaveAndGetLatestSession(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return baseTime }

	sess := auth.Session{
		AccessToken: "test-token-123",
		BrokerToken: "broker-tok-abc",
		BrokerSID:   "sid-xyz",
		BaseURL:     "https://tradeapi.kotaksecurities.com",
		FeedURL:     "wss://tradeapi.kotaksecurities.com/feed",
		LoginMethod: auth.LoginMethodMPIN,
		CreatedAt:   baseTime,
		TokenExpiry: baseTime.Add(3 * time.Hour),
	}

	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	got, err := s.GetLatestSession(ctx)
	if err != nil {
		t.Fatalf("GetLatestSession: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}

	if got.AccessToken != sess.AccessToken {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, sess.AccessToken)
	}
	if got.BrokerToken != sess.BrokerToken {
		t.Errorf("BrokerToken = %q, want %q", got.BrokerToken, sess.BrokerToken)
	}
	if got.BrokerSID != sess.BrokerSID {
		t.Errorf("BrokerSID = %q, want %q", got.BrokerSID, sess.BrokerSID)
	}
	if got.BaseURL != sess.BaseURL {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, sess.BaseURL)
	}
	if got.FeedURL != sess.FeedURL {
		t.Errorf("FeedURL = %q, want %q", got.FeedURL, sess.FeedURL)
	}
	if got.LoginMethod != sess.LoginMethod {
		t.Errorf("LoginMethod = %q, want %q", got.LoginMethod, sess.LoginMethod)
	}
	if !got.CreatedAt.Equal(sess.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, sess.CreatedAt)
	}
	if !got.TokenExpiry.Equal(sess.TokenExpiry) {
		t.Errorf("TokenExpiry = %v, want %v", got.TokenExpiry, sess.TokenExpiry)
	}
}

func TestGetLatestSessionExpired(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return baseTime }

	sess := auth.Session{
		AccessToken: "expired-token",
		BrokerToken: "tok",
		BrokerSID:   "sid",
		BaseURL:     "https://api.example.com",
		FeedURL:     "wss://api.example.com/feed",
		LoginMethod: auth.LoginMethodMPIN,
		CreatedAt:   baseTime.Add(-4 * time.Hour),
		TokenExpiry: baseTime.Add(-1 * time.Hour), // expired 1 hour ago
	}

	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	got, err := s.GetLatestSession(ctx)
	if err != nil {
		t.Fatalf("GetLatestSession: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for expired session, got: %+v", got)
	}
}

func TestGetLatestSessionMultiple(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	t1 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return t2 }

	sess1 := auth.Session{
		AccessToken: "token-1",
		BrokerToken: "b-tok-1",
		BrokerSID:   "sid-1",
		BaseURL:     "https://api1.example.com",
		FeedURL:     "wss://api1.example.com",
		LoginMethod: auth.LoginMethodTOTP,
		CreatedAt:   t1,
		TokenExpiry: t1.Add(3 * time.Hour),
	}
	sess2 := auth.Session{
		AccessToken: "token-2",
		BrokerToken: "b-tok-2",
		BrokerSID:   "sid-2",
		BaseURL:     "https://api2.example.com",
		FeedURL:     "wss://api2.example.com",
		LoginMethod: auth.LoginMethodMPIN,
		CreatedAt:   t2,
		TokenExpiry: t2.Add(3 * time.Hour),
	}

	if err := s.SaveSession(ctx, sess1); err != nil {
		t.Fatalf("SaveSession 1: %v", err)
	}
	if err := s.SaveSession(ctx, sess2); err != nil {
		t.Fatalf("SaveSession 2: %v", err)
	}

	got, err := s.GetLatestSession(ctx)
	if err != nil {
		t.Fatalf("GetLatestSession: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.AccessToken != "token-2" {
		t.Errorf("got token %q, want %q", got.AccessToken, "token-2")
	}
}

func TestDeleteAndRevokeSessions(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return baseTime }

	sess := auth.Session{
		AccessToken: "del-token",
		BrokerToken: "b-tok",
		BrokerSID:   "sid",
		BaseURL:     "https://api.example.com",
		FeedURL:     "wss://api.example.com",
		LoginMethod: auth.LoginMethodMPIN,
		CreatedAt:   baseTime,
		TokenExpiry: baseTime.Add(3 * time.Hour),
	}

	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// Delete by token
	if err := s.DeleteSession(ctx, "del-token"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	got, err := s.GetLatestSession(ctx)
	if err != nil {
		t.Fatalf("GetLatestSession: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}

	// Delete non-existent or empty token should not fail
	if err := s.DeleteSession(ctx, ""); err != nil {
		t.Errorf("DeleteSession empty: %v", err)
	}
	if err := s.DeleteSession(ctx, "nonexistent"); err != nil {
		t.Errorf("DeleteSession nonexistent: %v", err)
	}

	// Save another and test RevokeAllSessions
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := s.RevokeAllSessions(ctx); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	got2, err := s.GetLatestSession(ctx)
	if err != nil {
		t.Fatalf("GetLatestSession after revoke: %v", err)
	}
	if got2 != nil {
		t.Fatalf("expected nil after RevokeAllSessions, got %+v", got2)
	}
}

func TestSaveSessionValidation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// Empty AccessToken
	err := s.SaveSession(ctx, auth.Session{})
	if err == nil {
		t.Fatal("expected error for empty AccessToken")
	}

	// Default timestamps
	sess := auth.Session{
		AccessToken: "token-defaults",
		BrokerToken: "tok",
	}
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession with defaults: %v", err)
	}
	got, err := s.GetLatestSession(ctx)
	if err != nil {
		t.Fatalf("GetLatestSession: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if got.TokenExpiry.IsZero() {
		t.Error("expected non-zero TokenExpiry")
	}

	// Closed store
	_ = s.Close()
	if err := s.SaveSession(ctx, sess); err == nil {
		t.Error("expected error on closed store SaveSession")
	}
	if _, err := s.GetLatestSession(ctx); err == nil {
		t.Error("expected error on closed store GetLatestSession")
	}
	if err := s.DeleteSession(ctx, "token-defaults"); err == nil {
		t.Error("expected error on closed store DeleteSession")
	}
	if err := s.RevokeAllSessions(ctx); err == nil {
		t.Error("expected error on closed store RevokeAllSessions")
	}
}

func TestSaveSessionUpsert(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return baseTime }

	sess := auth.Session{
		AccessToken: "upsert-token",
		BrokerToken: "tok-v1",
		BrokerSID:   "sid-v1",
		BaseURL:     "https://v1.example.com",
		FeedURL:     "wss://v1.example.com",
		LoginMethod: auth.LoginMethodTOTP,
		CreatedAt:   baseTime,
		TokenExpiry: baseTime.Add(3 * time.Hour),
	}

	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession v1: %v", err)
	}

	// Update the same token
	sess.BrokerToken = "tok-v2"
	sess.BaseURL = "https://v2.example.com"
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession v2: %v", err)
	}

	got, err := s.GetLatestSession(ctx)
	if err != nil {
		t.Fatalf("GetLatestSession: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.BrokerToken != "tok-v2" {
		t.Errorf("BrokerToken = %q, want %q", got.BrokerToken, "tok-v2")
	}
	if got.BaseURL != "https://v2.example.com" {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, "https://v2.example.com")
	}
}
