package web

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/auth"
	"github.com/EvAvKein/Fortytwode/internal/config"
	"github.com/EvAvKein/Fortytwode/internal/routes"
	"github.com/EvAvKein/Fortytwode/internal/view/pages"
)

// handleSync starts the 42 OAuth authorization-code flow. Cross-site navigations
// are rejected (sync needs no session, so SameSite cookies don't gate it): another
// site could otherwise send visitors here and spend their sync cooldown/budget.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	state := randomToken()
	s.setCookie(w, stateCookie, state, 10*time.Minute)
	authURL := config.AuthorizeURL + "?" + url.Values{
		"client_id":     {s.cfg.ClientID},
		"redirect_uri":  {s.cfg.RedirectURI},
		"response_type": {"code"},
		"scope":         {s.cfg.Scope},
		"state":         {state},
	}.Encode()
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleLogin42 starts a 42 OAuth flow for authentication only (no data sync).
// It sets an intent cookie so the callback knows to look up an existing account
// by 42 identity rather than starting a full sync.
func (s *Server) handleLogin42(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	state := randomToken()
	s.setCookie(w, stateCookie, state, 10*time.Minute)
	s.setCookie(w, intentCookie, "login", 10*time.Minute)
	authURL := config.AuthorizeURL + "?" + url.Values{
		"client_id":     {s.cfg.ClientID},
		"redirect_uri":  {s.cfg.RedirectURI},
		"response_type": {"code"},
		"scope":         {s.cfg.Scope},
		"state":         {state},
	}.Encode()
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleCallback validates the OAuth redirect, exchanges the code for a token,
// and kicks off a background sync job. A logged-in user's job updates their
// account (if the 42 identity matches); an anonymous job awaits sign-up,
// download, or — if its 42 identity turns out to be registered — sign-in.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Error(w, "Authorization denied: "+e, http.StatusBadRequest)
		return
	}
	stateC, err := r.Cookie(stateCookie)
	if err != nil || q.Get("state") == "" || q.Get("state") != stateC.Value {
		http.Error(w, "OAuth state mismatch, please try syncing again", http.StatusBadRequest)
		return
	}
	s.clearCookie(w, stateCookie)
	intent := ""
	if c, err := r.Cookie(intentCookie); err == nil {
		intent = c.Value
	}
	s.clearCookie(w, intentCookie)
	code := q.Get("code")
	if code == "" {
		http.Error(w, "no authorization code in callback", http.StatusBadRequest)
		return
	}

	token, err := auth.ExchangeCode(s.cfg, code)
	if err != nil {
		s.renderTokenExchangeFailure(w, r, err)
		return
	}

	if intent == "login" {
		s.handleLoginFlow(w, r, token)
		return
	}

	var claimAccountID, expectFtID int64
	acc, loggedIn := s.currentAccount(r)
	if loggedIn {
		claimAccountID, expectFtID = acc.ID, acc.FtID
	}

	// One running sync per client: a second concurrent attempt would spend more
	// 42 API budget against the shared limiter before the per-42-user cooldown
	// (enforced once /me reveals the user) can apply. The in-flight job's cookie
	// is still set, so the progress page just picks it back up.
	clientKey := s.clientKey(r, loggedIn)
	jobID, j, ok := s.jobs.create(clientKey)
	if !ok {
		// create refuses in two cases: this client already has a running job
		// (pick it back up on the syncing page), or the registry is full of
		// other clients' in-flight syncs (transient: show a styled 503).
		if s.jobs.hasRunning(clientKey) {
			http.Redirect(w, r, routes.PageSyncing, http.StatusFound)
			return
		}
		renderStatus(w, r, http.StatusServiceUnavailable, pages.ServerBusy(s.viewerLogin(r)))
		return
	}
	s.setCookie(w, jobCookie, jobID, s.jobs.ttl)

	// A logged-in user's 42 id is known up front, so reject a too-soon re-sync
	// here — before spending even the /me request. The job is failed immediately
	// so the cooldown message surfaces over the same SSE error path. Anonymous
	// syncs are still gated authoritatively in startSync, once /me reveals who
	// they are. A pre-check error is ignored; the authoritative reserve will run.
	if claimAccountID != 0 {
		if retryAfter, active, lastSync, err := s.store.SyncCooldown(r.Context(), expectFtID, syncCooldown); err == nil && active {
			j.fail(cooldownError(lastSync, retryAfter))
			http.Redirect(w, r, routes.PageSyncing, http.StatusFound)
			return
		}
	}

	s.startSync(token, j, claimAccountID, expectFtID)
	http.Redirect(w, r, routes.PageSyncing, http.StatusFound)
}

// renderTokenExchangeFailure responds to a failed 42 callback token exchange,
// alerting the maintainer when the failure is a rejected client secret.
func (s *Server) renderTokenExchangeFailure(w http.ResponseWriter, r *http.Request, err error) {
	fmt.Fprintf(os.Stderr, "warning: token exchange failed: %v\n", err)
	if auth.IsInvalidGrant(err) {
		renderStatus(w, r, http.StatusBadRequest,
			pages.AuthFailed("This 42 authorization expired or was already used, please try again.", s.viewerLogin(r)))
		return
	}
	if auth.IsClientSecretRejected(err) {
		go s.sendMaintainerAlert(secretRejectedAlert(err))
	}
	// 503, not 502/504: Cloudflare (fronting the origin) intercepts those
	// codes with its own branded interstitial instead of showing this response.
	renderStatus(w, r, http.StatusServiceUnavailable,
		pages.AuthFailed("We couldn't reach or complete sign-in with 42. Please try again in a few minutes.", s.viewerLogin(r)))
}
