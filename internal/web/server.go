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
	"github.com/EvAvKein/Fortytwode/internal/routes"
	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/a-h/templ"
)

// Server holds the dependencies shared by all handlers.
type Server struct {
	store            *store.Store
	cfg              config.Config
	limiter          *api42.Limiter // shared across all syncs to respect 42's per-app caps
	jobs             *jobRegistry
	secure           bool // mark cookies Secure (set when the redirect URI is https)
	passwordAttempts *attemptLimiter[int64]
	loginAttempts    *attemptLimiter[string]
}

// Serve starts the web app, reading PORT (default 4242).
func Serve(cfg config.Config, st *store.Store) error {
	s := &Server{
		store:            st,
		cfg:              cfg,
		limiter:          api42.NewLimiter(),
		jobs:             newJobRegistry(),
		secure:           strings.HasPrefix(cfg.RedirectURI, "https://"),
		passwordAttempts: newAttemptLimiter[int64](maxPasswordAttempts, passwordAttemptWindow),
		loginAttempts:    newAttemptLimiter[string](maxLoginAttempts, loginAttemptWindow),
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
			s.loginAttempts.prune()
			s.passwordAttempts.prune()
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

// routes wires the request multiplexer: pages at the top level, API endpoints
// under config.AppAPIPrefix(). Keeping actions in their own mux lets the page
// mux use a catch-all 404 without shadowing the API mux's per-method 405s —
// e.g. GET /api/<version>/session still 405s, while GET /nope renders the styled 404.
func (server *Server) routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc(routes.APIAuth42Callback.Pattern(), server.handleCallback) // OAuth redirect URI (see .env.example)
	api.HandleFunc(routes.APIAuth42Login.Pattern(), server.handleLogin42)     // OAuth login-only flow (no sync)
	api.HandleFunc(routes.API42Sync.Pattern(), server.handleSync)
	api.HandleFunc(routes.APISyncStream.Pattern(), server.handleStream)
	api.HandleFunc(routes.APISyncDownloadRaw.Pattern(), server.handleDownloadRaw)
	api.HandleFunc(routes.APISyncDownloadCurated.Pattern(), server.handleDownloadCurated)
	api.HandleFunc(routes.APILogInSync.Pattern(), server.handleSyncSignin)
	api.HandleFunc(routes.APILogIn.Pattern(), server.handleLogin)
	api.HandleFunc(routes.APILogOut.Pattern(), server.handleLogout)
	api.HandleFunc(routes.APIAccountCreate.Pattern(), server.handleSignup)
	api.HandleFunc(routes.APIAccountDownload.Pattern(), server.handleDownloadSaved)
	api.HandleFunc(routes.APIAccountVisibility.Pattern(), server.handleSettings)
	api.HandleFunc(routes.APIAccountEmail.Pattern(), server.handleSettingsEmail)
	api.HandleFunc(routes.APIAccountPassword.Pattern(), server.handleSettingsPassword)
	api.HandleFunc(routes.APIAccountDelete.Pattern(), server.handleDeleteAccount)

	// Pages are top-level HTML GETs. The trailing method-less "/" is the
	// catch-all that renders the styled 404 for any unmatched path.
	pages := http.NewServeMux()
	pages.HandleFunc("GET "+routes.PageHealth, server.handleHealth)
	pages.HandleFunc("GET /{$}", server.handleHome)
	pages.HandleFunc("GET "+routes.PageSyncing, server.handleSyncing)
	pages.HandleFunc("GET "+routes.PageSignup, server.handleSignupForm)
	pages.HandleFunc("GET "+routes.PageLogin, server.handleLoginForm)
	pages.HandleFunc("GET "+routes.PageSettings, server.handleSettingsForm)
	pages.HandleFunc("GET "+routes.PagePrivacy, server.handlePrivacy)
	pages.HandleFunc("GET "+routes.PageProfile("{login}"), server.handleProfile)
	registerAssets(pages) // fingerprinted /static/* stylesheet + scripts
	pages.Handle(routes.APIPrefix()+"/", http.StripPrefix(routes.APIPrefix(), api))
	pages.HandleFunc("/", server.handleNotFound)
	return securityHeaders(pages)
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


