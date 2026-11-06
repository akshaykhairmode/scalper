package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Device represents a registered WebAuthn / Passkey authenticator device.
type Device struct {
	ID         string    `json:"id"`
	DeviceName string    `json:"device_name"`
	PublicKey  string    `json:"public_key"`
	Algorithm  int64     `json:"algorithm"`
	SignCount  uint32    `json:"sign_count"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// SaveDevice persists a WebAuthn device in the database.
func (s *Store) SaveDevice(ctx context.Context, dev Device) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if strings.TrimSpace(dev.ID) == "" {
		return errors.New("store: device ID is required")
	}
	if strings.TrimSpace(dev.DeviceName) == "" {
		return errors.New("store: device name is required")
	}
	if strings.TrimSpace(dev.PublicKey) == "" {
		return errors.New("store: device public key is required")
	}

	createdAt := dev.CreatedAt
	if createdAt.IsZero() {
		if s.now != nil {
			createdAt = s.now()
		} else {
			createdAt = time.Now()
		}
	}
	lastUsedAt := dev.LastUsedAt
	if lastUsedAt.IsZero() {
		lastUsedAt = createdAt
	}

	query := `
		INSERT INTO devices (
			id, device_name, public_key, algorithm, sign_count, created_at, last_used_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			device_name  = excluded.device_name,
			public_key   = excluded.public_key,
			algorithm    = excluded.algorithm,
			sign_count   = excluded.sign_count,
			last_used_at = excluded.last_used_at
	`
	_, err := s.db.ExecContext(ctx, query,
		dev.ID,
		dev.DeviceName,
		dev.PublicKey,
		dev.Algorithm,
		dev.SignCount,
		createdAt.UTC().Format("2006-01-02 15:04:05"),
		lastUsedAt.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return fmt.Errorf("store: save device: %w", err)
	}
	return nil
}

// GetDevice retrieves a device by its ID.
func (s *Store) GetDevice(ctx context.Context, id string) (*Device, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}

	query := `
		SELECT id, device_name, public_key, algorithm, sign_count, created_at, last_used_at
		FROM devices
		WHERE id = ?
	`
	var (
		d            Device
		createdAtStr string
		lastUsedStr  string
	)
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&d.ID,
		&d.DeviceName,
		&d.PublicKey,
		&d.Algorithm,
		&d.SignCount,
		&createdAtStr,
		&lastUsedStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get device: %w", err)
	}

	d.CreatedAt = parseDBTime(createdAtStr)
	d.LastUsedAt = parseDBTime(lastUsedStr)
	return &d, nil
}

// ListDevices returns all registered devices ordered by last used and creation time descending.
func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}

	query := `
		SELECT id, device_name, public_key, algorithm, sign_count, created_at, last_used_at
		FROM devices
		ORDER BY last_used_at DESC, created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list devices: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var devices []Device
	for rows.Next() {
		var (
			d            Device
			createdAtStr string
			lastUsedStr  string
		)
		if err := rows.Scan(
			&d.ID,
			&d.DeviceName,
			&d.PublicKey,
			&d.Algorithm,
			&d.SignCount,
			&createdAtStr,
			&lastUsedStr,
		); err != nil {
			return nil, fmt.Errorf("store: scan device: %w", err)
		}
		d.CreatedAt = parseDBTime(createdAtStr)
		d.LastUsedAt = parseDBTime(lastUsedStr)
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate devices: %w", err)
	}
	if devices == nil {
		devices = []Device{}
	}
	return devices, nil
}

// UpdateDeviceUsage updates the sign count and last used timestamp of a device.
func (s *Store) UpdateDeviceUsage(ctx context.Context, id string, signCount uint32, lastUsedAt time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("store: device ID is required")
	}
	if lastUsedAt.IsZero() {
		if s.now != nil {
			lastUsedAt = s.now()
		} else {
			lastUsedAt = time.Now()
		}
	}

	query := `
		UPDATE devices
		SET sign_count = ?, last_used_at = ?
		WHERE id = ?
	`
	res, err := s.db.ExecContext(ctx, query,
		signCount,
		lastUsedAt.UTC().Format("2006-01-02 15:04:05"),
		id,
	)
	if err != nil {
		return fmt.Errorf("store: update device usage: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update device usage rows affected: %w", err)
	}
	if rows == 0 {
		return errors.New("store: device not found")
	}
	return nil
}

// DeleteDevice deletes a device by ID.
func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete device: %w", err)
	}
	return nil
}

// CountDevices returns the number of registered devices.
func (s *Store) CountDevices(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("store: database not open")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices`).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count devices: %w", err)
	}
	return count, nil
}
