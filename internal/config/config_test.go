package config

import "testing"

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
