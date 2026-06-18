// Package web serves the multi-user dashboard over HTTP: anonymous 42 sync + JSON
// download, email/password accounts, and per-section public profiles. It owns
// routing, sessions, and the background sync jobs; the stored snapshot is curated
// into view models and rendered by the internal/view package.
package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/api42"
	"github.com/EvAvKein/Fortytwode/internal/config"
	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/a-h/templ"
)

// Server holds the dependencies shared by all handlers.
type Server struct {
	store   *store.Store
	cfg     config.Config
	limiter *api42.Limiter // shared across all syncs to respect 42's per-app caps
	jobs    *jobRegistry
	secure  bool // mark cookies Secure (set when the redirect URI is https)
}

// Serve starts the web app, reading PORT (default 4242).
func Serve(cfg config.Config, st *store.Store) error {
	s := &Server{
		store:   st,
		cfg:     cfg,
		limiter: api42.NewLimiter(),
		jobs:    newJobRegistry(),
		secure:  strings.HasPrefix(cfg.RedirectURI, "https://"),
	}

	// Periodically purge stale sync-cooldown rows and expired sessions
	// (data-minimisation retention): cooldowns only matter inside their window
	// (this also clears rows from anonymous syncs that never became accounts),
	// and expired sessions are already invisible to lookups.
	go func() {
		for range time.Tick(30 * time.Minute) {
			if _, err := st.PurgeStaleCooldowns(context.Background(), syncCooldown); err != nil {
				fmt.Fprintf(os.Stderr, "warning: purge cooldowns: %v\n", err)
			}
			if _, err := st.PurgeExpiredSessions(context.Background()); err != nil {
				fmt.Fprintf(os.Stderr, "warning: purge sessions: %v\n", err)
			}
		}
	}()

	port := cmp.Or(os.Getenv("PORT"), "4242")

	// The server binds :PORT inside its container, but users reach it through the
	// Nginx front door, whose origin is the redirect URI's scheme+host. Announce
	// that public URL so the printed address is one you can actually open (printing
	// :PORT would point at the unpublished internal port). Fall back to the bind
	// port if the redirect URI is missing/relative (e.g. running the binary direct).
	dashboard := "http://localhost:" + port + "/"
	if u, err := url.Parse(cfg.RedirectURI); err == nil && u.Scheme != "" && u.Host != "" {
		dashboard = u.Scheme + "://" + u.Host + "/"
	}
	fmt.Printf("Serving the dashboard at %s\n", dashboard)
	fmt.Println("Press Ctrl+C to stop.")
	return http.ListenAndServe(":"+port, s.routes())
}

// routes wires the request multiplexer: pages at the top level, action endpoints
// under /api/. Keeping actions in their own mux lets the page mux use a catch-all
// 404 without shadowing the action mux's per-method 405s — e.g. GET /api/logout
// still 405s, while GET /nope renders the styled 404.
func (s *Server) routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /sync", s.handleSync)
	api.HandleFunc("GET /auth/42/callback", s.handleCallback) // OAuth redirect URI (see .env.example)
	api.HandleFunc("GET /auth/42/login", s.handleLogin42)     // OAuth login-only flow (no sync)
	api.HandleFunc("GET /syncing/signin", s.handleSyncSignin)
	api.HandleFunc("GET /fetch/stream", s.handleStream)
	api.HandleFunc("GET /download", s.handleDownloadRaw)
	api.HandleFunc("GET /download/curated", s.handleDownloadCurated)
	api.HandleFunc("GET /download/saved", s.handleDownloadSaved)
	api.HandleFunc("POST /signup", s.handleSignup)
	api.HandleFunc("POST /login", s.handleLogin)
	api.HandleFunc("POST /logout", s.handleLogout)
	api.HandleFunc("POST /settings", s.handleSettings)
	api.HandleFunc("POST /account/delete", s.handleDeleteAccount)

	// Pages are top-level HTML GETs. The trailing method-less "/" is the
	// catch-all that renders the styled 404 for any unmatched path.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthcheck", s.handleHealth)
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /syncing", s.handleSyncing)
	mux.HandleFunc("GET /signup", s.handleSignupForm)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("GET /settings", s.handleSettingsForm)
	mux.HandleFunc("GET /privacy", s.handlePrivacy)
	mux.HandleFunc("GET /u/{login}", s.handleProfile)
	registerAssets(mux) // fingerprinted /static/* stylesheet + scripts
	mux.Handle("/api/", http.StripPrefix("/api", api))
	mux.HandleFunc("/", s.handleNotFound)
	return securityHeaders(mux)
}

// render writes a templ component as a 200 HTML response.
func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	renderStatus(w, r, http.StatusOK, c)
}

// renderStatus writes a templ component as an HTML response with the given
// status. The status must be written after the headers and before the body.
func renderStatus(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		fmt.Fprintf(os.Stderr, "warning: render failed: %v\n", err)
	}
}
