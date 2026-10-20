// Package config loads the 42 OAuth settings from the environment.
package config

import (
	"cmp"
	"fmt"
	"os"
	"strings"
	"time"
)

// 42 API endpoints. These are fixed; only the credentials below vary per user.
const (
	APIBase      = "https://api.intra.42.fr"
	AuthorizeURL = APIBase + "/oauth/authorize"
	TokenURL     = APIBase + "/oauth/token"
	APIv2        = APIBase + "/v2"
	PhotoBase    = "https://cdn.intra.42.fr"
)

// AppAPIVersion is the version prefix for Fortytwode's own REST API.
const AppAPIVersion = "v1"

// AppAPIPrefix returns the mount prefix for the web API, e.g. "/api/v1".
func AppAPIPrefix() string { return "/api/" + AppAPIVersion }

// defaultScope is the space-separated OAuth scope requested at login. "public"
// is enough for /v2/me; widen it (e.g. "public profile projects") here and
// rebuild if some fields come back empty. It lives in code rather than the env
// because it is the only setting that can contain spaces, and quoting it
// behaves differently between the Makefile's `include .env` and a shell `source`.
const defaultScope = "public"

// The verification email's From header is assembled from a code-side display name
// and an env-configurable address. The name lives in code because the full
// "Name <addr>" header has a space and `<` that the documented shell-source path
// (`set -a; . ./.env`) would mangle; only the bare address is exposed via
// MAIL_FROM_ADDRESS. It sends from the dedicated mail subdomain (its own DKIM/SPF),
// clear of Cloudflare Email Routing, which owns the root domain's inbound MX/SPF.
const (
	defaultMailFromName    = "Fortytwode"
	defaultMailFromAddress = "no-reply@mail.fortytwode.net"
)

// Config holds everything needed to authenticate against the 42 API and to send
// the transactional verification email.
type Config struct {
	ClientID           string
	ClientSecret       string
	ClientSecretNext   string
	ClientSecretExpiry time.Time // zero when unset: no rotation reminders
	RedirectURI        string
	Scope              string

	// Transactional email (Resend). ResendAPIKey is required to actually send;
	// when empty, the email layer logs and skips so local dev still works. MailFrom
	// is the assembled "Name <addr>" sender header; MailReplyTo (optional, e.g. a
	// Cloudflare-routed inbox) lets replies reach a human. MaintainerEmail receives
	// operational alerts; it's the same inbox, empty when unset (alerts only log).
	ResendAPIKey    string
	MailFrom        string
	MailReplyTo     string
	MaintainerEmail string
}

// Load reads the configuration from environment variables, applying defaults
// for the optional ones and erroring if a required one is missing.
func Load() (Config, error) {
	config := Config{
		ClientID:         env("FT_CLIENT_ID", ""),
		ClientSecret:     env("FT_CLIENT_SECRET", ""),
		ClientSecretNext: env("FT_CLIENT_SECRET_NEXT", ""),
		RedirectURI:      env("FT_REDIRECT_URI", "http://localhost:3000/callback"),
		Scope:            defaultScope,
		ResendAPIKey:     env("RESEND_API_KEY", ""),
		MailFrom:         defaultMailFromName + " <" + env("MAIL_FROM_ADDRESS", defaultMailFromAddress) + ">",
		MailReplyTo:      env("MAIL_REPLY_TO", ""),
	}
	config.MaintainerEmail = config.MailReplyTo

	var missing []string
	if config.ClientID == "" {
		missing = append(missing, "FT_CLIENT_ID")
	}
	if config.ClientSecret == "" {
		missing = append(missing, "FT_CLIENT_SECRET")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf(
			"missing required environment variable(s): %s\n"+
				"see .env.example, then export them (e.g. `set -a; . ./.env; set +a`)",
			strings.Join(missing, ", "))
	}

	if rawExpiry := env("FT_CLIENT_SECRET_EXPIRES", ""); rawExpiry != "" {
		var err error
		if config.ClientSecretExpiry, err = time.Parse(time.DateOnly, rawExpiry); err != nil {
			return Config{}, fmt.Errorf("invalid FT_CLIENT_SECRET_EXPIRES %q, want YYYY-MM-DD", rawExpiry)
		}
	}
	return config, nil
}

// env returns the trimmed value of an environment variable, or fallback when it
// is unset or blank.
func env(key, fallback string) string {
	return cmp.Or(strings.TrimSpace(os.Getenv(key)), fallback)
}
