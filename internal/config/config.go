// Package config loads the 42 OAuth settings from the environment.
package config

import (
	"cmp"
	"fmt"
	"os"
	"strings"
)

// 42 API endpoints. These are fixed; only the credentials below vary per user.
const (
	APIBase      = "https://api.intra.42.fr"
	AuthorizeURL = APIBase + "/oauth/authorize"
	TokenURL     = APIBase + "/oauth/token"
	APIv2        = APIBase + "/v2"
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

// Config holds everything needed to authenticate against the 42 API.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scope        string
}

// Load reads the configuration from environment variables, applying defaults
// for the optional ones and erroring if a required one is missing.
func Load() (Config, error) {
	config := Config{
		ClientID:     env("FT_CLIENT_ID", ""),
		ClientSecret: env("FT_CLIENT_SECRET", ""),
		RedirectURI:  env("FT_REDIRECT_URI", "http://localhost:3000/callback"),
		Scope:        defaultScope,
	}

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
	return config, nil
}

// env returns the trimmed value of an environment variable, or fallback when it
// is unset or blank.
func env(key, fallback string) string {
	return cmp.Or(strings.TrimSpace(os.Getenv(key)), fallback)
}
