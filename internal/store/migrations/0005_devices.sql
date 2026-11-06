-- 0005_devices.sql — WebAuthn / Passkey registered devices.

CREATE TABLE devices (
  id           TEXT PRIMARY KEY,
  device_name  TEXT NOT NULL,
  public_key   TEXT NOT NULL,
  algorithm    INTEGER NOT NULL,
  sign_count   INTEGER NOT NULL DEFAULT 0,
  created_at   DATETIME NOT NULL,
  last_used_at DATETIME NOT NULL
);
