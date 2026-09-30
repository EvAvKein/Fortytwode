package config

import (
	"testing"
	"time"
)

// TestLoadMailFrom checks that the From header is assembled from the code-side
// display name and the (space-free) MAIL_FROM_ADDRESS env override.
func TestLoadMailFrom(t *testing.T) {
	t.Setenv("FT_CLIENT_ID", "id")         // required, else Load errors
	t.Setenv("FT_CLIENT_SECRET", "secret") // required, else Load errors

	t.Run("default", func(t *testing.T) {
		t.Setenv("MAIL_FROM_ADDRESS", "") // blank -> code default address
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if want := "Fortytwode <no-reply@mail.fortytwode.net>"; cfg.MailFrom != want {
			t.Errorf("MailFrom = %q, want %q", cfg.MailFrom, want)
		}
	})

	t.Run("override", func(t *testing.T) {
		t.Setenv("MAIL_FROM_ADDRESS", "no-reply@mail.example.com")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if want := "Fortytwode <no-reply@mail.example.com>"; cfg.MailFrom != want {
			t.Errorf("MailFrom = %q, want %q", cfg.MailFrom, want)
		}
	})
}

// TestLoadClientSecretExpiry checks FT_CLIENT_SECRET_EXPIRES parsing: blank leaves
// it zero (optional), an invalid value fails Load, and a valid YYYY-MM-DD parses.
func TestLoadClientSecretExpiry(t *testing.T) {
	t.Setenv("FT_CLIENT_ID", "id")
	t.Setenv("FT_CLIENT_SECRET", "secret")

	t.Run("blank", func(t *testing.T) {
		t.Setenv("FT_CLIENT_SECRET_EXPIRES", "")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.ClientSecretExpiry.IsZero() {
			t.Errorf("ClientSecretExpiry = %v, want zero", cfg.ClientSecretExpiry)
		}
	})

	t.Run("valid", func(t *testing.T) {
		t.Setenv("FT_CLIENT_SECRET_EXPIRES", "2026-10-15")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
		if !cfg.ClientSecretExpiry.Equal(want) {
			t.Errorf("ClientSecretExpiry = %v, want %v", cfg.ClientSecretExpiry, want)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Setenv("FT_CLIENT_SECRET_EXPIRES", "15/10/2026")
		if _, err := Load(); err == nil {
			t.Error("Load() error = nil, want an invalid-date error")
		}
	})
}
