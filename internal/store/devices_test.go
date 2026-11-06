package store

import (
	"context"
	"testing"
	"time"
)

func TestSaveAndGetDevice(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return baseTime }

	dev := Device{
		ID:         "cred-id-12345",
		DeviceName: "MacBook Pro Touch ID",
		PublicKey:  "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEtestpublickey",
		Algorithm:  -7, // ES256
		SignCount:  1,
		CreatedAt:  baseTime,
		LastUsedAt: baseTime,
	}

	if err := s.SaveDevice(ctx, dev); err != nil {
		t.Fatalf("SaveDevice: %v", err)
	}

	got, err := s.GetDevice(ctx, dev.ID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got == nil {
		t.Fatal("expected device, got nil")
	}

	if got.ID != dev.ID {
		t.Errorf("ID = %q, want %q", got.ID, dev.ID)
	}
	if got.DeviceName != dev.DeviceName {
		t.Errorf("DeviceName = %q, want %q", got.DeviceName, dev.DeviceName)
	}
	if got.PublicKey != dev.PublicKey {
		t.Errorf("PublicKey = %q, want %q", got.PublicKey, dev.PublicKey)
	}
	if got.Algorithm != dev.Algorithm {
		t.Errorf("Algorithm = %d, want %d", got.Algorithm, dev.Algorithm)
	}
	if got.SignCount != dev.SignCount {
		t.Errorf("SignCount = %d, want %d", got.SignCount, dev.SignCount)
	}
	if !got.CreatedAt.Equal(dev.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, dev.CreatedAt)
	}
	if !got.LastUsedAt.Equal(dev.LastUsedAt) {
		t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, dev.LastUsedAt)
	}
}

func TestDeviceUpsert(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	t1 := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 7, 2, 0, 0, 0, time.UTC)

	dev := Device{
		ID:         "cred-upsert-1",
		DeviceName: "Old Name",
		PublicKey:  "old-pub-key",
		Algorithm:  -7,
		SignCount:  10,
		CreatedAt:  t1,
		LastUsedAt: t1,
	}
	if err := s.SaveDevice(ctx, dev); err != nil {
		t.Fatalf("SaveDevice: %v", err)
	}

	// Update device with new name and updated sign count
	dev.DeviceName = "New Name"
	dev.SignCount = 15
	dev.LastUsedAt = t2
	if err := s.SaveDevice(ctx, dev); err != nil {
		t.Fatalf("SaveDevice update: %v", err)
	}

	got, err := s.GetDevice(ctx, dev.ID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got.DeviceName != "New Name" {
		t.Errorf("DeviceName = %q, want %q", got.DeviceName, "New Name")
	}
	if got.SignCount != 15 {
		t.Errorf("SignCount = %d, want 15", got.SignCount)
	}
}

func TestListDevices(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// Initial empty check
	list, err := s.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices empty: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 devices, got %d", len(list))
	}

	count, err := s.CountDevices(ctx)
	if err != nil {
		t.Fatalf("CountDevices empty: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}

	d1 := Device{
		ID:         "dev-1",
		DeviceName: "Device 1",
		PublicKey:  "pub1",
		Algorithm:  -7,
		SignCount:  5,
		CreatedAt:  time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC),
		LastUsedAt: time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC),
	}
	d2 := Device{
		ID:         "dev-2",
		DeviceName: "Device 2",
		PublicKey:  "pub2",
		Algorithm:  -7,
		SignCount:  20,
		CreatedAt:  time.Date(2026, 11, 7, 2, 0, 0, 0, time.UTC),
		LastUsedAt: time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC),
	}

	if err := s.SaveDevice(ctx, d1); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDevice(ctx, d2); err != nil {
		t.Fatal(err)
	}

	count, err = s.CountDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("CountDevices = %d, want 2", count)
	}

	list, err = s.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	// Ordered by last_used_at DESC, so dev-2 is first
	if list[0].ID != "dev-2" {
		t.Errorf("first device = %q, want dev-2", list[0].ID)
	}
	if list[1].ID != "dev-1" {
		t.Errorf("second device = %q, want dev-1", list[1].ID)
	}
}

func TestUpdateDeviceUsage(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	t0 := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	dev := Device{
		ID:         "dev-usage-1",
		DeviceName: "Phone",
		PublicKey:  "pubkey",
		Algorithm:  -7,
		SignCount:  1,
		CreatedAt:  t0,
		LastUsedAt: t0,
	}
	if err := s.SaveDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}

	tNew := time.Date(2026, 11, 7, 5, 0, 0, 0, time.UTC)
	if err := s.UpdateDeviceUsage(ctx, dev.ID, 42, tNew); err != nil {
		t.Fatalf("UpdateDeviceUsage: %v", err)
	}

	got, err := s.GetDevice(ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SignCount != 42 {
		t.Errorf("SignCount = %d, want 42", got.SignCount)
	}
	if !got.LastUsedAt.Equal(tNew) {
		t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, tNew)
	}

	// Update non-existent device
	err = s.UpdateDeviceUsage(ctx, "nonexistent", 1, tNew)
	if err == nil {
		t.Error("expected error for nonexistent device, got nil")
	}
}

func TestDeleteDevice(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	dev := Device{
		ID:         "dev-del-1",
		DeviceName: "To Delete",
		PublicKey:  "pubkey",
		Algorithm:  -7,
		SignCount:  1,
	}
	if err := s.SaveDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteDevice(ctx, dev.ID); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	got, err := s.GetDevice(ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %v", got)
	}

	// Deleting non-existent or empty ID should not error
	if err := s.DeleteDevice(ctx, "nonexistent"); err != nil {
		t.Errorf("DeleteDevice nonexistent: %v", err)
	}
	if err := s.DeleteDevice(ctx, ""); err != nil {
		t.Errorf("DeleteDevice empty: %v", err)
	}
}

func TestDeviceValidation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.SaveDevice(ctx, Device{}); err == nil {
		t.Error("expected error for empty ID")
	}
	if err := s.SaveDevice(ctx, Device{ID: "id"}); err == nil {
		t.Error("expected error for empty DeviceName")
	}
	if err := s.SaveDevice(ctx, Device{ID: "id", DeviceName: "name"}); err == nil {
		t.Error("expected error for empty PublicKey")
	}

	got, err := s.GetDevice(ctx, "")
	if err != nil || got != nil {
		t.Errorf("GetDevice empty ID: got %v, err %v", got, err)
	}

	// Nil store checks
	var nilStore *Store
	if err := nilStore.SaveDevice(ctx, Device{}); err == nil {
		t.Error("expected error on nil store")
	}
	if _, err := nilStore.GetDevice(ctx, "id"); err == nil {
		t.Error("expected error on nil store")
	}
	if _, err := nilStore.ListDevices(ctx); err == nil {
		t.Error("expected error on nil store")
	}
	if err := nilStore.UpdateDeviceUsage(ctx, "id", 1, time.Now()); err == nil {
		t.Error("expected error on nil store")
	}
	if err := nilStore.DeleteDevice(ctx, "id"); err == nil {
		t.Error("expected error on nil store")
	}
	if _, err := nilStore.CountDevices(ctx); err == nil {
		t.Error("expected error on nil store")
	}
}
