// Package auth obtains 42 user access tokens. The CLI flow (AccessToken) caches
// to .token.json, trying in order: a cached non-expired token, a refresh of an
// expired one, then a fresh interactive browser login. The web flow
// (ExchangeCode) runs the callback's authorization-code exchange and does not cache.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/config"
)

const (
	tokenFile  = ".token.json"
	expirySkew = 60 * time.Second // refresh a little before the real expiry
)

// storedToken is the OAuth token response plus our own bookkeeping. JSON tags
// match both the 42 API fields and our on-disk format.
type storedToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"` // seconds the access token is valid
	CreatedAt    int64  `json:"created_at"` // unix seconds the token was issued
}

func (t storedToken) expired() bool {
	expiresAt := time.Unix(t.CreatedAt+int64(t.ExpiresIn), 0)
	return time.Now().Add(expirySkew).After(expiresAt)
}

// AccessToken returns a valid bearer token, obtaining or refreshing one as
// needed and persisting the result to .token.json.
func AccessToken(cfg config.Config) (string, error) {
	stored, _ := readStored() // a missing/garbled file just means "no token yet"

	if stored != nil && !stored.expired() {
		return stored.AccessToken, nil
	}

	if stored != nil && stored.RefreshToken != "" {
		refreshed, err := requestToken(cfg, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {stored.RefreshToken},
		})
		if err == nil {
			if err := writeStored(refreshed); err != nil {
				return "", err
			}
			fmt.Println("Refreshed access token.")
			return refreshed.AccessToken, nil
		}
		fmt.Printf("Token refresh failed (%v); falling back to interactive login.\n", err)
	}

	fresh, err := authorizeInteractively(cfg)
	if err != nil {
		return "", err
	}
	if err := writeStored(fresh); err != nil {
		return "", err
	}
	fmt.Println("Obtained new access token.")
	return fresh.AccessToken, nil
}

func readStored() (*storedToken, error) {
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, err
	}
	var t storedToken
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func writeStored(t storedToken) error {
	raw, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(tokenFile, append(raw, '\n'), 0o600)
}

// ExchangeCode runs the authorization-code grant for the web server's callback
// (the redirect URI must match cfg.RedirectURI) and returns the access token. The
// token is not cached — web syncs hold it only for the duration of the fetch.
func ExchangeCode(cfg config.Config, code string) (string, error) {
	t, err := requestToken(cfg, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {cfg.RedirectURI},
	})
	if err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

// tokenClient bounds the token-exchange round trip (matching the api package's
// client timeout) so a hung 42 endpoint can't hang the caller indefinitely.
var tokenClient = &http.Client{Timeout: 30 * time.Second}

// requestToken exchanges the given grant for a token at the OAuth token URL.
func requestToken(cfg config.Config, params url.Values) (storedToken, error) {
	params.Set("client_id", cfg.ClientID)
	params.Set("client_secret", cfg.ClientSecret)

	res, err := tokenClient.PostForm(config.TokenURL, params)
	if err != nil {
		return storedToken{}, err
	}
	defer res.Body.Close()

	var t storedToken
	if err := json.NewDecoder(res.Body).Decode(&t); err != nil {
		return storedToken{}, err
	}
	if res.StatusCode != http.StatusOK {
		return storedToken{}, fmt.Errorf("token request failed (%d)", res.StatusCode)
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().Unix()
	}
	return t, nil
}

// authorizeInteractively runs the authorization-code flow: it spins up a local
// server on the redirect URI, opens the browser, waits for the redirect, then
// exchanges the returned code for a token.
func authorizeInteractively(cfg config.Config) (storedToken, error) {
	redirect, err := url.Parse(cfg.RedirectURI)
	if err != nil {
		return storedToken{}, err
	}
	port := redirect.Port()
	if port == "" {
		port = "80"
	}
	state := randomState()

	authURL := config.AuthorizeURL + "?" + url.Values{
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {cfg.RedirectURI},
		"response_type": {"code"},
		"scope":         {cfg.Scope},
		"state":         {state},
	}.Encode()

	code, err := waitForCode(port, redirect.Path, state, func() {
		fmt.Printf("\nOpen this URL in your browser to authorize:\n\n  %s\n\n", authURL)
		fmt.Printf("Waiting for the redirect to %s ...\n", cfg.RedirectURI)
		openBrowser(authURL)
	})
	if err != nil {
		return storedToken{}, err
	}

	return requestToken(cfg, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {cfg.RedirectURI},
	})
}

// waitForCode serves a one-shot HTTP listener on port, runs onReady once it is
// listening, and resolves with the authorization code from the OAuth redirect.
func waitForCode(port, path, state string, onReady func()) (string, error) {
	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code, err := validateCallback(q, state)

		msg := "Authorized! ✅"
		status := http.StatusOK
		if err != nil {
			msg, status = err.Error(), http.StatusBadRequest
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>42 auth</title>`+
			`<body style="font-family:sans-serif;padding:2rem"><h1>%s</h1>`+
			`<p>You can close this tab and return to the terminal.</p></body>`, msg)

		results <- result{code: code, err: err}
	})

	// Loopback only: the browser redirect always comes from this machine, and an
	// all-interfaces listener would let other hosts on the network hit the callback.
	listener, err := net.Listen("tcp", "localhost:"+port)
	if err != nil {
		return "", err
	}
	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Shutdown(context.Background())

	onReady()
	r := <-results
	return r.code, r.err
}

// validateCallback extracts the authorization code, rejecting error responses,
// CSRF state mismatches, and missing codes.
func validateCallback(q url.Values, state string) (string, error) {
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("authorization denied: %s", e)
	}
	if q.Get("state") != state {
		return "", errors.New("OAuth state mismatch — possible CSRF, aborting")
	}
	code := q.Get("code")
	if code == "" {
		return "", errors.New("no authorization code in callback")
	}
	return code, nil
}

// openBrowser makes a best-effort attempt to open url; the URL is also printed
// for manual use, so failures are ignored.
func openBrowser(rawURL string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "cmd", []string{"/c", "start", ""} // empty title arg so URLs with & aren't misparsed
	default:
		cmd = "xdg-open"
	}
	_ = exec.Command(cmd, append(args, rawURL)...).Start()
}

// randomState returns a random hex string used to defend the OAuth flow
// against CSRF (the value must come back unchanged in the redirect).
func randomState() string {
	b := make([]byte, 16)
	rand.Read(b) // crypto/rand.Read never returns an error (Go 1.24+)
	return hex.EncodeToString(b)
}
