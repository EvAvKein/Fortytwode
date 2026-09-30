package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvAvKein/Fortytwode/internal/config"
)

// tokenServer starts a fake 42 token endpoint and points the package's tokenURL
// seam at it for the duration of the test.
func tokenServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	prev := tokenURL
	tokenURL = srv.URL
	t.Cleanup(func() { tokenURL = prev })
	useNextSecret.Store(false)
	t.Cleanup(func() { useNextSecret.Store(false) })
}

func oauthError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func TestRequestTokenFallsBackToNextSecret(t *testing.T) {
	tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.FormValue("client_secret") {
		case "current":
			oauthError(w, http.StatusUnauthorized, "invalid_client")
		case "next":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "bearer", "expires_in": 3600})
		default:
			t.Errorf("unexpected client_secret %q", r.FormValue("client_secret"))
			http.Error(w, "unexpected client_secret", http.StatusInternalServerError)
		}
	})

	cfg := config.Config{ClientSecret: "current", ClientSecretNext: "next"}
	token, err := ExchangeCode(cfg, "code")
	if err != nil {
		t.Fatalf("ExchangeCode() error = %v, want nil", err)
	}
	if token != "tok" {
		t.Errorf("ExchangeCode() = %q, want %q", token, "tok")
	}
}

func TestRequestTokenSkipsCurrentSecretAfterFallback(t *testing.T) {
	var secrets []string
	tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		secrets = append(secrets, r.FormValue("client_secret"))
		if r.FormValue("client_secret") == "current" {
			oauthError(w, http.StatusUnauthorized, "invalid_client")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "bearer", "expires_in": 3600})
	})

	cfg := config.Config{ClientSecret: "current", ClientSecretNext: "next"}
	for range 2 {
		if _, err := ExchangeCode(cfg, "code"); err != nil {
			t.Fatalf("ExchangeCode() error = %v, want nil", err)
		}
	}
	if got, want := strings.Join(secrets, ","), "current,next,next"; got != want {
		t.Errorf("secrets tried = %s, want %s", got, want)
	}
}

func TestRequestTokenNoFallbackWithoutNextConfigured(t *testing.T) {
	calls := 0
	tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		oauthError(w, http.StatusUnauthorized, "invalid_client")
	})

	cfg := config.Config{ClientSecret: "current"} // ClientSecretNext unset
	_, err := ExchangeCode(cfg, "code")
	if err == nil {
		t.Fatal("ExchangeCode() error = nil, want non-nil")
	}
	if !IsClientSecretRejected(err) {
		t.Errorf("ExchangeCode() error = %v, want an invalid_client tokenError", err)
	}
	if calls != 1 {
		t.Errorf("token endpoint called %d time(s), want 1 (no fallback attempt)", calls)
	}
}

func TestRequestTokenNoFallbackOnNonSecretError(t *testing.T) {
	calls := 0
	tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		oauthError(w, http.StatusBadRequest, "invalid_grant")
	})

	cfg := config.Config{ClientSecret: "current", ClientSecretNext: "next"}
	_, err := ExchangeCode(cfg, "bad-code")
	if err == nil {
		t.Fatal("ExchangeCode() error = nil, want non-nil")
	}
	if IsClientSecretRejected(err) {
		t.Errorf("ExchangeCode() error = %v, unexpectedly classified as invalid_client", err)
	}
	if !IsInvalidGrant(err) {
		t.Errorf("IsInvalidGrant(%v) = false, want true", err)
	}
	if calls != 1 {
		t.Errorf("token endpoint called %d time(s), want 1 (invalid_grant shouldn't trigger fallback)", calls)
	}
}

func TestRequestTokenBothSecretsRejected(t *testing.T) {
	tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
	})

	cfg := config.Config{ClientSecret: "current", ClientSecretNext: "next"}
	_, err := ExchangeCode(cfg, "code")
	if err == nil {
		t.Fatal("ExchangeCode() error = nil, want non-nil")
	}
	if !IsClientSecretRejected(err) {
		t.Errorf("IsClientSecretRejected(%v) = false, want true", err)
	}
}

func TestIsClientSecretRejected(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"invalid_client", &tokenError{OAuthError: "invalid_client"}, true},
		{"invalid_grant", &tokenError{OAuthError: "invalid_grant"}, false},
		{"unparsed body", &tokenError{OAuthError: ""}, false},
		{"non-tokenError", errors.New("network error"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsClientSecretRejected(tt.err); got != tt.want {
				t.Errorf("IsClientSecretRejected(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestTokenErrorTruncatesBody(t *testing.T) {
	err := newTokenError(http.StatusBadGateway, []byte(strings.Repeat("x", 10*maxTokenErrorBody)))
	if want := strings.Repeat("x", maxTokenErrorBody) + "…(truncated)"; err.Body != want {
		t.Errorf("Body has length %d, want %d", len(err.Body), len(want))
	}
}
