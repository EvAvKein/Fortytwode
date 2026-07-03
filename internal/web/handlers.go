package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/api42"
	"github.com/EvAvKein/Fortytwode/internal/auth"
	"github.com/EvAvKein/Fortytwode/internal/config"
	"github.com/EvAvKein/Fortytwode/internal/fetch"
	"github.com/EvAvKein/Fortytwode/internal/routes"
	"github.com/EvAvKein/Fortytwode/internal/snapshot"
	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/EvAvKein/Fortytwode/internal/view"
	"github.com/EvAvKein/Fortytwode/internal/view/model"
	"github.com/EvAvKein/Fortytwode/internal/view/pages"
)

// syncCooldown is the minimum time between full data fetches for one 42 user.
const syncCooldown = 15 * time.Minute

// viewerLogin returns the current viewer's 42 login, or empty string if not logged in.
func (s *Server) viewerLogin(r *http.Request) string {
	if acc, ok := s.currentAccount(r); ok {
		return acc.FtLogin
	}
	return ""
}

// canResync reports whether the account is outside the 42 sync cooldown and is
// therefore allowed to start a new re-sync.
func (s *Server) canResync(ctx context.Context, acc store.Account) bool {
	_, active, _, err := s.store.SyncCooldown(ctx, acc.FtID, syncCooldown)
	return err == nil && !active
}

// handleHome shows the landing page.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	render(w, r, pages.Landing(s.viewerLogin(r), s.store.GetStats()))
}

// handleNotFound renders the styled 404 for any unmatched page route.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	renderStatus(w, r, http.StatusNotFound, pages.NotFound(s.viewerLogin(r)))
}

// handlePrivacy renders the privacy notice (linked from the footer).
func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	render(w, r, pages.Privacy(s.viewerLogin(r)))
}

// handleHealth backs the container healthcheck: 200 when the database is
// reachable, 503 otherwise.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintln(w, "ok")
}

// rejectCrossSite 403s a request the browser flags as a cross-site POST/navigation,
// reporting whether it handled (rejected) the request. It guards the endpoints that
// SameSite=Lax doesn't (sync needs no session; login/signup take no prior cookie), so
// another site can't drive them on a visitor's behalf. An absent header (old browser,
// curl, direct navigation) is allowed — credentials, cooldown, and the nginx/limiter
// caps still bound those.
func (s *Server) rejectCrossSite(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "cross-site request blocked. Use the form on the site", http.StatusForbidden)
		return true
	}
	return false
}

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
		fmt.Fprintf(os.Stderr, "warning: token exchange failed: %v\n", err)
		http.Error(w, "Could not complete 42 authorization, please try again. If this continues happening, please report it at https://github.com/EvAvKein/Fortytwode/issues", http.StatusBadGateway)
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

// startSync runs the pull in the background, pushing progress to the job. A
// logged-in re-sync writes straight to the account after an identity check. A
// logged-out sync whose 42 identity already has an account refreshes it too and
// flags the job, so the page can offer sign-in instead of sign-up.
func (s *Server) startSync(token string, j *job, claimAccountID, expectFtID int64) {
	go func() {
		ctx := context.Background() // independent of the request; bounded by api timeouts
		client := api42.New(token, s.limiter)

		// reservedID/ok drive the cooldown: a slot is claimed once /me reveals the
		// 42 user, and released if the sync then fails so the user can retry now.
		var reservedID int64
		ok := false
		defer func() {
			if reservedID != 0 && !ok {
				_ = s.store.ReleaseSync(ctx, reservedID)
			}
		}()

		res, err := fetch.Pull(ctx, client, j.setProgress, func(ftID int64) error {
			retryAfter, allowed, lastSync, err := s.store.ReserveSync(ctx, ftID, syncCooldown)
			if err != nil {
				return fmt.Errorf("could not check sync cooldown: %w", err)
			}
			if !allowed {
				return cooldownError(lastSync, retryAfter)
			}
			reservedID = ftID
			return nil
		})
		if err != nil {
			j.fail(err)
			return
		}
		if claimAccountID != 0 {
			if res.FtID != expectFtID {
				j.fail(fmt.Errorf("You authorized as a different 42 user than this account. Re-sync as the right user, or log out and sign up a new account"))
				return
			}
			if err := s.store.UpdateSnapshot(ctx, claimAccountID, res.Snapshot); err != nil {
				j.fail(err)
				return
			}
		} else if acc, err := s.store.AccountByFtID(ctx, res.FtID); err == nil {
			if err := s.store.UpdateSnapshot(ctx, acc.ID, res.Snapshot); err != nil {
				j.fail(err)
				return
			}
			j.linkAccount(acc.ID, acc.FtLogin)
		}
		ok = true // sync succeeded: keep the cooldown slot
		j.finish(res.Snapshot, res.FtID, res.FtLogin)
	}()
}

// cooldownError is the error shown when a 42 user re-syncs within the cooldown,
// reporting when they last synced and when they can retry.
func cooldownError(lastSync time.Time, retryAfter time.Duration) error {
	ago := view.Ago(lastSync)
	if ago == "" {
		return fmt.Errorf("You synced recently. Try again %s", view.In(retryAfter))
	}
	return fmt.Errorf("You synced %s. Try again %s", ago, view.In(retryAfter))
}

// handleSyncing renders the progress page (which opens the SSE stream).
func (s *Server) handleSyncing(w http.ResponseWriter, r *http.Request) {
	viewerLogin := s.viewerLogin(r)
	render(w, r, pages.Syncing(viewerLogin, viewerLogin))
}

// handleSyncSignin signs in a returning user whose logged-out sync was matched
// to an existing account (see startSync), then sends them to their profile. The
// account comes from the job, which was matched against the 42 identity they
// just authorized — so this can only ever log them into their own account.
func (s *Server) handleSyncSignin(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	jobID, j, ok := s.jobFor(r)
	if !ok {
		http.Redirect(w, r, routes.PageLogin, http.StatusFound)
		return
	}
	id, login := j.matched()
	if id == 0 {
		http.Redirect(w, r, routes.PageLogin, http.StatusFound)
		return
	}
	if err := s.startSession(w, r, id); err != nil {
		http.Error(w, "Could not start session", http.StatusInternalServerError)
		return
	}
	s.jobs.delete(jobID)
	s.clearCookie(w, jobCookie)
	// The just-set session cookie isn't on this request yet, so resolve the account
	// via a direct lookup rather than currentAccount; an unverified (e.g. legacy)
	// account signing in this way is still boxed into the pending page.
	dest := routes.PageProfile(login)
	if acc, err := s.store.AccountByLogin(r.Context(), login); err == nil {
		dest = destAfterAuth(acc)
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// handleStream streams the current job's progress as Server-Sent Events.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(jobCookie)
	if err != nil {
		http.Error(w, "No sync in progress", http.StatusNotFound)
		return
	}
	j, ok := s.jobs.get(c.Value)
	if !ok {
		http.Error(w, "No sync in progress", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.jobs.runningCount() >= slowTrafficThreshold {
			j.markSlow()
		}
		st := j.state()
		event := "progress"
		switch st.Status {
		case string(jobDone):
			event = "done"
		case string(jobError):
			event = "error"
		}
		b, _ := json.Marshal(st)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
		if st.Status != string(jobRunning) {
			return
		}
		select {
		case <-ticker.C:
		case <-r.Context().Done():
			return
		}
	}
}

// handleDownloadRaw serves the just-synced job's raw 42 data: the unmodified API
// snapshot, before curation. It exists only while the sync job is live (the raw
// copy is never persisted), so a logged-in user with no in-flight sync gets a
// prompt to re-sync. Used by the anonymous syncing page and right after a re-sync.
func (s *Server) handleDownloadRaw(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(jobCookie); err == nil {
		if j, ok := s.jobs.get(c.Value); ok {
			if snap, _, login, done := j.result(); done {
				go s.store.IncrementDownloads()
				writeJSONDownload(w, snap, login+"-raw")
				return
			}
		}
	}
	http.Error(w, "No raw data to download: Re-sync your 42 data", http.StatusNotFound)
}

// handleDownloadSaved serves the logged-in account's persisted snapshot — exactly
// the curated data we store, for transparency. Always available to the owner.
func (s *Server) handleDownloadSaved(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		renderStatus(w, r, http.StatusUnauthorized, pages.LoginForm("Please log in to continue", ""))
		return
	}
	if s.gateUnverified(w, r, acc) {
		return
	}
	go s.store.IncrementDownloads()
	writeJSONDownload(w, acc.Data, acc.FtLogin+"-saved")
}

// handleDownloadCurated serves the just-synced job's data after curation: the exact
// subset we would persist, derived on the fly from the live job. It needs no
// account, so the syncing page can offer it before sign-up — a preview of what
// storing the profile would keep (and drop).
func (s *Server) handleDownloadCurated(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(jobCookie); err == nil {
		if j, ok := s.jobs.get(c.Value); ok {
			if snap, _, login, done := j.result(); done {
				go s.store.IncrementDownloads()
				writeJSONDownload(w, snapshot.Curate(snap), login+"-curated")
				return
			}
		}
	}
	http.Error(w, "No data to download: Re-sync your 42 data", http.StatusNotFound)
}

// writeJSONDownload sends a snapshot map as a pretty-printed JSON file attachment.
func writeJSONDownload(w http.ResponseWriter, data map[string]json.RawMessage, name string) {
	blob, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		http.Error(w, "could not encode data", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fortytwode-%s.json"`, name))
	w.Write(blob)
}

// jobFor returns the request's sync job (and its id) while its data is still
// around (cookie set, not yet swept by TTL). A missing job means it expired.
func (s *Server) jobFor(r *http.Request) (string, *job, bool) {
	c, err := r.Cookie(jobCookie)
	if err != nil {
		return "", nil, false
	}
	j, ok := s.jobs.get(c.Value)
	return c.Value, j, ok
}

func (s *Server) handleSignupForm(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.jobFor(r); !ok {
		render(w, r, pages.SignupForm("Your fetch expired. Please fetch your 42 data again", true, ""))
		return
	}
	render(w, r, pages.SignupForm("", false, ""))
}

// handleSignup creates an account, attaching the just-synced job's data and 42
// identity, then logs the user in.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	email, ok := parseEmail(r.FormValue("email"))
	if !ok {
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.SignupForm("Enter a valid email address", false, ""))
		return
	}
	jobID, j, ok := s.jobFor(r)
	if !ok {
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.SignupForm("Your fetch expired. Please fetch your 42 data again", true, ""))
		return
	}
	snap, ftID, ftLogin, done := j.result()
	if !done {
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.SignupForm("Your fetch hasn't finished yet, wait for it to complete", false, ""))
		return
	}
	id, err := s.store.CreateAccount(r.Context(), email, ftID, ftLogin, snap)
	if errors.Is(err, store.ErrDuplicate) {
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.SignupForm("This email or 42 profile already has an account, try logging in", false, ""))
		return
	}
	if err != nil {
		http.Error(w, "Could not create account", http.StatusInternalServerError)
		return
	}
	go s.store.IncrementProfiles()
	s.jobs.delete(jobID)
	s.clearCookie(w, jobCookie)
	// Start a session so the pending page can identify the account (resend /
	// correct-email), but the verified-gate keeps it boxed there until the link is
	// clicked. The verification email is issued before the redirect.
	if err := s.startSession(w, r, id); err != nil {
		http.Error(w, "Could not start session", http.StatusInternalServerError)
		return
	}
	s.issueVerification(r.Context(), id, email, ftLogin)
	http.Redirect(w, r, routes.PageVerifyPending, http.StatusFound)
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	render(w, r, pages.LoginForm("", ""))
}

// handleLogin requests a magic-link login: it mails a one-time link if an account
// exists for the address, then renders the same LoginLinkSent page either way (see
// that template for why the response must not reveal whether the address is
// registered). The per-email cap bounds how often a link can be sprayed at one address.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	email, ok := parseEmail(r.FormValue("email"))
	if !ok {
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.LoginForm("Enter a valid email address.", ""))
		return
	}
	// The cap is keyed on the submitted address and checked in-memory before any DB
	// work, so telling the user they're over it reveals nothing about whether an account
	// exists — only that this address has requested too many links recently.
	if !s.loginAttempts.allowed(email) {
		renderStatus(w, r, http.StatusTooManyRequests,
			pages.LoginForm("Too many login link requests — wait a little before trying again.", ""))
		return
	}
	s.loginAttempts.recordFailed(email)
	// Respond before any DB work, then look up and mail on a background context, so the
	// response time doesn't depend on whether the address is registered (which would
	// otherwise reveal whether an account exists).
	render(w, r, pages.LoginLinkSent(email, s.viewerLogin(r)))
	go func() {
		if acc, err := s.store.AccountByEmail(context.Background(), email); err == nil {
			s.issueLoginLink(context.Background(), acc.ID, acc.Email)
		}
	}()
}

// handleLoginCallback is the GET the magic-link email points at. It only peeks the
// token and renders a confirm interstitial — consuming nothing, so a link prefetch
// (mail scanner, preview bot) can't spend the one-time token, and the session start
// stays off this cross-site-triggerable GET (that's what defeats login CSRF). The
// interstitial's button POSTs to handleLoginConsume, which is where login happens.
func (s *Server) handleLoginCallback(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAttemptAllowed(w, r) {
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		s.badLink(w, r)
		return
	}
	if _, err := s.store.PeekLoginToken(r.Context(), tokenHash(token), loginTokenTTL); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.recordBadToken(r)
			s.badLink(w, r)
			return
		}
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	render(w, r, pages.LoginConfirm(token, s.viewerLogin(r)))
}

// handleLoginConsume redeems the token from the confirm interstitial's POST (see
// handleLoginCallback for why login is a POST, not the GET). A valid, unexpired token
// starts a session and lands on the profile; anything else renders the failure page.
func (s *Server) handleLoginConsume(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	if !s.tokenAttemptAllowed(w, r) {
		return
	}
	token := r.FormValue("token")
	if token == "" {
		s.badLink(w, r)
		return
	}
	acc, err := s.store.ConsumeLoginToken(r.Context(), tokenHash(token), loginTokenTTL)
	if errors.Is(err, store.ErrNotFound) {
		s.recordBadToken(r)
		s.badLink(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := s.startSession(w, r, acc.ID); err != nil {
		http.Error(w, "Could not start session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, destAfterAuth(acc), http.StatusFound)
}

// handleLoginFlow completes a 42 OAuth login: it calls /v2/me to identify the
// user, looks up their account by 42 id, and starts a session if found. If no
// account exists for that 42 identity, the login form is shown with an error.
func (s *Server) handleLoginFlow(w http.ResponseWriter, r *http.Request, token string) {
	client := api42.New(token, s.limiter)
	rawMe, err := client.GetOne(r.Context(), "me")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: /v2/me failed during 42 login: %v\n", err)
		render(w, r, pages.LoginForm("Could not verify your 42 identity, please try again", ""))
		return
	}
	var me struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rawMe, &me); err != nil || me.ID == 0 {
		render(w, r, pages.LoginForm("Could not read your 42 identity, please try again", ""))
		return
	}
	acc, err := s.store.AccountByFtID(r.Context(), me.ID)
	if errors.Is(err, store.ErrNotFound) {
		render(w, r, pages.LoginForm("No account found for this 42 identity. To create an account, load your 42 data", ""))
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := s.startSession(w, r, acc.ID); err != nil {
		http.Error(w, "Could not start session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, destAfterAuth(acc), http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	s.endSession(w, r)
	http.Redirect(w, r, routes.PageHome, http.StatusSeeOther)
}

// handleProfile renders a profile, enforcing the visibility tier: owner sees all;
// a logged-in viewer sees public sections; anonymous sees them only if the owner
// opted the profile public.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	viewer, loggedIn := s.currentAccount(r)
	acc, err := s.store.AccountByLogin(r.Context(), login)
	if errors.Is(err, store.ErrNotFound) {
		renderStatus(w, r, http.StatusNotFound, pages.ProfileUnavailable(s.viewerLogin(r)))
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	owner := loggedIn && viewer.ID == acc.ID
	if owner && !viewer.EmailVerified {
		http.Redirect(w, r, routes.PageVerifyPending, http.StatusFound)
		return
	}
	if !owner && !loggedIn && !acc.IsPublic {
		renderStatus(w, r, http.StatusNotFound, pages.ProfileUnavailable(s.viewerLogin(r)))
		return
	}

	d := view.Build(acc.Data, owner, acc.Visibility)
	d.Owner = owner
	d.Login = acc.FtLogin
	if owner {
		d.SyncedAtISO = acc.FetchedAt.UTC().Format(time.RFC3339)
		d.CanResync = s.canResync(r.Context(), acc)
	}
	render(w, r, pages.Profile(d, s.viewerLogin(r)))
}

func (s *Server) handleSettingsForm(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		http.Redirect(w, r, routes.PageLogin, http.StatusFound)
		return
	}
	if s.gateUnverified(w, r, acc) {
		return
	}
	d := s.settingsData(r.Context(), acc, false)
	d.DeletionRequested = r.URL.Query().Get("deletion") == "requested"
	d.EmailPending = r.URL.Query().Get("email_pending")
	render(w, r, pages.Settings(d, acc.FtLogin))
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		renderStatus(w, r, http.StatusUnauthorized, pages.LoginForm("Please log in to continue", ""))
		return
	}
	if s.gateUnverified(w, r, acc) {
		return
	}
	if s.rejectCrossSite(w, r) {
		return
	}
	isPublic, sections, err := parseVisibilityForm(r)
	if err != nil {
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.Settings(s.settingsData(r.Context(), acc, false), acc.FtLogin))
		return
	}
	if err := s.store.UpdateVisibility(r.Context(), acc.ID, isPublic, sections); err != nil {
		http.Error(w, "Could not save settings", http.StatusInternalServerError)
		return
	}
	acc.IsPublic, acc.Visibility = isPublic, sections
	render(w, r, pages.Settings(s.settingsData(r.Context(), acc, true), acc.FtLogin))
}

func parseVisibilityForm(r *http.Request) (bool, map[string]bool, error) {
	if err := r.ParseForm(); err != nil {
		return false, nil, err
	}
	ctype := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(ctype, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return false, nil, err
		}
	}
	isPublic := r.Form.Has("is_public")
	sections := make(map[string]bool, len(view.ToggleableSections))
	for _, t := range view.ToggleableSections {
		sections[t.Key] = r.Form.Has("section_" + t.Key)
	}
	return isPublic, sections, nil
}

// handleSettingsEmail starts a confirm-first email change: it mails a confirmation
// link to the new address and leaves the real email untouched until that link is
// consumed (handleConfirmEmail), so a mistyped address can't lock the owner out. The
// session proves ownership; the per-account cap bounds mail sprayed at new addresses.
func (s *Server) handleSettingsEmail(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		renderStatus(w, r, http.StatusUnauthorized, pages.LoginForm("Please log in to continue", ""))
		return
	}
	if s.gateUnverified(w, r, acc) {
		return
	}
	if s.rejectCrossSite(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.Settings(s.settingsData(r.Context(), acc, false), acc.FtLogin))
		return
	}
	// ParseForm does not read a multipart body (the JS submits the form as
	// multipart/form-data), and having already populated r.Form above defeats
	// FormValue's own lazy multipart parse — so parse it explicitly, as
	// parseVisibilityForm does, or the email field comes back empty.
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			renderStatus(w, r, http.StatusUnprocessableEntity, pages.Settings(s.settingsData(r.Context(), acc, false), acc.FtLogin))
			return
		}
	}

	newEmail, ok := parseEmail(r.FormValue("email"))
	d := s.settingsData(r.Context(), acc, false)

	if !ok {
		d.Email = newEmail
		d.EmailError = "Enter a valid email address"
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.Settings(d, acc.FtLogin))
		return
	}
	if newEmail == acc.Email {
		d.EmailError = "That is already your email address"
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.Settings(d, acc.FtLogin))
		return
	}
	// Spend the per-account budget before the "already in use" probe below. Otherwise
	// someone could repeatedly submit different addresses and read that reply to learn
	// which emails have accounts; charging the probe against the cap limits how many
	// addresses they can test.
	if !s.emailChangeRequests.allowed(acc.ID) {
		d.EmailError = "Too many requests, wait a little before trying again"
		renderStatus(w, r, http.StatusTooManyRequests, pages.Settings(d, acc.FtLogin))
		return
	}
	s.emailChangeRequests.recordFailed(acc.ID)
	// Reject an address already taken by another account up front, rather than only
	// at confirmation time; the unique constraint still backstops a race (ConsumeEmailChange).
	if _, err := s.store.AccountByEmail(r.Context(), newEmail); err == nil {
		d.Email = newEmail
		d.EmailError = "That email is already in use"
		renderStatus(w, r, http.StatusUnprocessableEntity, pages.Settings(d, acc.FtLogin))
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	s.issueEmailChange(r.Context(), acc.ID, newEmail)
	// Post/Redirect/Get: app.js reloads the page on any 2xx and drops inline HTML,
	// so surface the "sent" message via a GET flash, as the deletion flow does.
	// The real email stays acc.Email until the confirmation link is clicked.
	http.Redirect(w, r, routes.PageSettings+"?email_pending="+url.QueryEscape(newEmail), http.StatusSeeOther)
}

// handleConfirmEmail is the GET the email-change link points at. It only peeks the
// token and renders a confirm interstitial — consuming nothing, so a link prefetch
// (mail scanner, preview bot) can't apply the change (and silently drop the owner's
// other sessions). The interstitial's button POSTs to handleConfirmEmailConsume,
// which is where the address actually changes. A bad/expired link shows the failure page.
func (s *Server) handleConfirmEmail(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAttemptAllowed(w, r) {
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		s.badLink(w, r)
		return
	}
	acc, pendingEmail, err := s.store.PeekEmailChange(r.Context(), tokenHash(token), emailChangeTokenTTL)
	if errors.Is(err, store.ErrNotFound) {
		s.recordBadToken(r)
		s.badLink(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	render(w, r, pages.ConfirmEmail(token, pendingEmail, acc.FtLogin, s.viewerLogin(r)))
}

// handleConfirmEmailConsume redeems the token from the confirm interstitial's POST,
// promoting the pending address to the account's email (see handleConfirmEmail for why
// this is a POST, not the GET). Confirming from the account's own browser rotates the
// session, drops the others, and lands on settings; a link opened elsewhere signs out
// all of the account's sessions and renders a result page. A stale/expired token, or an
// address taken since, shows the failure page.
func (s *Server) handleConfirmEmailConsume(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	if !s.tokenAttemptAllowed(w, r) {
		return
	}
	token := r.FormValue("token")
	if token == "" {
		s.badLink(w, r)
		return
	}
	acc, oldEmail, err := s.store.ConsumeEmailChange(r.Context(), tokenHash(token), emailChangeTokenTTL)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrDuplicate) {
		s.recordBadToken(r)
		s.badLink(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	// Tell the previous address the change happened, so a silent takeover leaves a
	// trail. Fire-and-forget on a background context — the change is already applied,
	// so a dropped notice is logged, not surfaced (mirrors issueEmailChange).
	go func() {
		if err := s.email.SendEmailChangeNotice(context.Background(), oldEmail, acc.Email); err != nil {
			fmt.Fprintf(os.Stderr, "warning: send email-change notice for account %d: %v\n", acc.ID, err)
		}
	}()
	if cur, ok := s.currentAccount(r); ok && cur.ID == acc.ID {
		// Confirmed from the account's own browser: keep this session alive by
		// rotating it, then drop every other session.
		if oldSID, ok := s.currentSessionID(r); ok {
			newSID, err := s.rotateSession(w, r, acc.ID, oldSID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not rotate session after email change for account %d: %v\n", acc.ID, err)
			} else if err := s.store.DeleteOtherSessions(r.Context(), acc.ID, newSID); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not invalidate other sessions after email change for account %d: %v\n", acc.ID, err)
			}
		}
		http.Redirect(w, r, routes.PageSettings, http.StatusFound)
		return
	}
	// Confirmed from a browser not logged into this account (logged out, or a
	// different account): no session to keep, so sign out all of the account's
	// existing sessions — the interstitial promises this unconditionally. Passing
	// "" keeps nothing, since session ids are never empty.
	if err := s.store.DeleteOtherSessions(r.Context(), acc.ID, ""); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not invalidate sessions after email change for account %d: %v\n", acc.ID, err)
	}
	render(w, r, pages.VerifyResult(true, s.viewerLogin(r)))
}

// settingsData renders the current account/visibility state into the template's shape.
func (s *Server) settingsData(ctx context.Context, acc store.Account, saved bool) model.SettingsData {
	d := model.SettingsData{IsPublic: acc.IsPublic, Login: acc.FtLogin, Saved: saved, Email: acc.Email, CanResync: s.canResync(ctx, acc)}
	d.SyncedAtISO = acc.FetchedAt.UTC().Format(time.RFC3339)
	for _, t := range view.ToggleableSections {
		d.Toggles = append(d.Toggles, model.SettingsToggle{
			Key:     t.Key,
			Label:   t.Label,
			Public:  view.SectionPublic(acc.Visibility, t.Key),
			Default: view.SectionPublic(nil, t.Key),
			HasData: settingsHasData(acc.Data, t.Key),
		})
	}
	return d
}

// settingsHasData reports whether a toggleable section has data. Most sections
// are top-level snapshot resources; skills, contact, and points are derived from
// the curated /me profile.
func settingsHasData(data map[string]json.RawMessage, key string) bool {
	switch key {
	case "skills":
		return profileHasSkills(data["me"])
	case "contact":
		return profileHasEmail(data["me"])
	case "points":
		return profileHasPoints(data["me"])
	case "achievements":
		return profileHasAchievements(data["me"])
	case "coalitions":
		return hasData(data["coalitions"])
	default:
		return hasData(data[key])
	}
}

func profileHasSkills(raw json.RawMessage) bool {
	var p struct {
		Cursus []struct {
			Skills []struct{ Name string } `json:"skills"`
		} `json:"cursus"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return false
	}
	for _, c := range p.Cursus {
		if len(c.Skills) > 0 {
			return true
		}
	}
	return false
}

func profileHasEmail(raw json.RawMessage) bool {
	var p struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return false
	}
	return p.Email != ""
}

func profileHasPoints(raw json.RawMessage) bool {
	var p struct {
		CorrectionPoint int `json:"correction_point"`
		Wallet          int `json:"wallet"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return false
	}
	return p.CorrectionPoint != 0 || p.Wallet != 0
}

func profileHasAchievements(raw json.RawMessage) bool {
	var p struct {
		Achievements []struct{ Name string } `json:"achievements"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return false
	}
	return len(p.Achievements) > 0
}

// hasData reports whether a snapshot resource holds something worth showing.
func hasData(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "[]" && s != "null"
}
