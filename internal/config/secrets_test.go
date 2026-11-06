package config

import (
	"strings"
	"testing"
)

func TestRedactSecretsMasksConfiguredValues(t *testing.T) {
	cfg := Default()
	cfg.Kotak.MobileNumber = "9999999999"
	cfg.Kotak.UCC = "ABC123"
	cfg.Web.BasicAuth.Username = "admin"
	cfg.Web.BasicAuth.Password = "secret"

	redacted := RedactSecrets(cfg)
	if redacted.Kotak.MobileNumber != SecretMask || redacted.Kotak.UCC != SecretMask ||
		redacted.Web.BasicAuth.Password != SecretMask {
		t.Fatalf("secrets were not masked: %+v", redacted)
	}
	if redacted.Web.BasicAuth.Username != "admin" {
		t.Fatal("non-secret username should remain visible")
	}
	if cfg.Web.BasicAuth.Password != "secret" {
		t.Fatal("RedactSecrets must not mutate the input")
	}
}

func TestRedactAndRestoreYAMLSecrets(t *testing.T) {
	current := []byte(`# broker identity
kotak:
  mobile_number: "+919999999999"
  ucc: TESTUCC
web:
  basic_auth:
    username: admin
    password: "super-secret" # keep me
`)
	redacted, err := RedactYAMLSecrets(current)
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	text := string(redacted)
	for _, secret := range []string{"+919999999999", "TESTUCC", "super-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("redacted yaml still contains %q:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, SecretMask) || !strings.Contains(text, "# broker identity") || !strings.Contains(text, "# keep me") {
		t.Fatalf("redaction lost the mask or comments:\n%s", text)
	}

	// Simulate the settings UI changing a non-secret field but leaving the
	// masks untouched.
	edited := strings.Replace(text, `username: admin`, `username: trader`, 1)
	restored, err := RestoreYAMLSecrets([]byte(edited), current)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	cfg, err := LoadBytes(restored, "config.yaml")
	if err != nil {
		t.Fatalf("load restored yaml: %v", err)
	}
	if cfg.Web.BasicAuth.Password != "super-secret" || cfg.Kotak.MobileNumber != "+919999999999" || cfg.Kotak.UCC != "TESTUCC" {
		t.Fatalf("masked values were not restored: %+v", cfg)
	}
	if cfg.Web.BasicAuth.Username != "trader" {
		t.Fatalf("non-secret edit was lost: %q", cfg.Web.BasicAuth.Username)
	}
}

func TestRestoreYAMLSecretsKeepsUnchangedFormatting(t *testing.T) {
	current := []byte("web:\n  basic_auth:\n    username: admin\n    password: secret\n")
	edited := []byte("web:\n  basic_auth:\n    username: admin\n    password: new-secret\n")
	restored, err := RestoreYAMLSecrets(edited, current)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if string(restored) != string(edited) {
		t.Fatalf("unmasked edits should not be reformatted:\n%s", restored)
	}
}

func TestRestoreYAMLSecretsNeverPersistsTheMask(t *testing.T) {
	current := []byte("web:\n  basic_auth:\n    username: admin\n")
	incoming := []byte("web:\n  basic_auth:\n    username: admin\n    password: '" + SecretMask + "'\n")
	restored, err := RestoreYAMLSecrets(incoming, current)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	cfg, err := LoadBytes(restored, "config.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Web.BasicAuth.Password != "" {
		t.Fatalf("mask was persisted as a password: %q", cfg.Web.BasicAuth.Password)
	}
}
