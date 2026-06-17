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
	"github.com/EvAvKein/Fortytwode/internal/snapshot"
	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/EvAvKein/Fortytwode/internal/view"
	"github.com/EvAvKein/Fortytwode/internal/view/model"
	"github.com/EvAvKein/Fortytwode/internal/view/pages"
)

// syncCooldown is the minimum time between full data fetches for one 42 user.
const syncCooldown = 15 * time.Minute

// handleHome shows the landing page, or redirects a logged-in user to their profile.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if acc, ok := s.currentAccount(r); ok {
		http.Redirect(w, r, "/u/"+acc.FtLogin, http.StatusFound)
		return
	}
	render(w, r, pages.Landing())
}

// handleNotFound renders the styled 404 for any unmatched page route.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	_, loggedIn := s.currentAccount(r)
	renderStatus(w, r, http.StatusNotFound, pages.NotFound(loggedIn))
}

// handlePrivacy renders the privacy notice (linked from the footer).
func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	_, loggedIn := s.currentAccount(r)
	render(w, r, pages.Privacy(loggedIn))
}

// handleHealth backs the container healthcheck: 200 when the database is
// reachable, 503 otherwise.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
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
		http.Error(w, "cross-site request blocked — use the form on the site", http.StatusForbidden)
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

// handleCallback validates the OAuth redirect, exchanges the code for a token,
// and kicks off a background sync job. A logged-in user's job updates their
// account (if the 42 identity matches); an anonymous job awaits sign-up,
// download, or — if its 42 identity turns out to be registered — sign-in.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Error(w, "authorization denied: "+e, http.StatusBadRequest)
		return
	}
	stateC, err := r.Cookie(stateCookie)
	if err != nil || q.Get("state") == "" || q.Get("state") != stateC.Value {
		http.Error(w, "OAuth state mismatch — please try syncing again", http.StatusBadRequest)
		return
	}
	s.clearCookie(w, stateCookie)
	code := q.Get("code")
	if code == "" {
		http.Error(w, "no authorization code in callback", http.StatusBadRequest)
		return
	}

	token, err := auth.ExchangeCode(s.cfg, code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: token exchange failed: %v\n", err)
		http.Error(w, "could not complete 42 authorization — please try syncing again", http.StatusBadGateway)
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
	jobID, j, ok := s.jobs.create(s.clientKey(r, loggedIn))
	if !ok {
		http.Redirect(w, r, "/syncing", http.StatusFound)
		return
	}
	s.setCookie(w, jobCookie, jobID, s.jobs.ttl)

	// A logged-in user's 42 id is known up front, so reject a too-soon re-sync
	// here — before spending even the /me request. The job is failed immediately
	// so the cooldown message surfaces over the same SSE error path. Anonymous
	// syncs are still gated authoritatively in startSync, once /me reveals who
	// they are. A pre-check error is ignored; the authoritative reserve will run.
	if claimAccountID != 0 {
		if retryAfter, active, err := s.store.SyncCooldown(r.Context(), expectFtID, syncCooldown); err == nil && active {
			j.fail(cooldownError(retryAfter))
			http.Redirect(w, r, "/syncing", http.StatusFound)
			return
		}
	}

	s.startSync(token, j, claimAccountID, expectFtID)
	http.Redirect(w, r, "/syncing", http.StatusFound)
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
			retryAfter, allowed, err := s.store.ReserveSync(ctx, ftID, syncCooldown)
			if err != nil {
				return fmt.Errorf("could not check sync cooldown: %w", err)
			}
			if !allowed {
				return cooldownError(retryAfter)
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
				j.fail(fmt.Errorf("you authorized as a different 42 user than this account; re-sync as the right user, or log out and sign up a new account"))
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
// reporting the remaining wait rounded up to whole minutes.
func cooldownError(retryAfter time.Duration) error {
	mins := max(1, int((retryAfter+time.Minute-time.Nanosecond)/time.Minute))
	return fmt.Errorf("you synced recently — try again in about %d minute(s)", mins)
}

// handleSyncing renders the progress page (which opens the SSE stream).
func (s *Server) handleSyncing(w http.ResponseWriter, r *http.Request) {
	acc, loggedIn := s.currentAccount(r)
	login := ""
	if loggedIn {
		login = acc.FtLogin
	}
	render(w, r, pages.Syncing(loggedIn, login))
}

// handleSyncSignin signs in a returning user whose logged-out sync was matched
// to an existing account (see startSync), then sends them to their profile. The
// account comes from the job, which was matched against the 42 identity they
// just authorized — so this can only ever log them into their own account.
func (s *Server) handleSyncSignin(w http.ResponseWriter, r *http.Request) {
	jobID, j, ok := s.jobFor(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	id, login := j.matched()
	if id == 0 {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if err := s.startSession(w, r, id); err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	s.jobs.delete(jobID)
	s.clearCookie(w, jobCookie)
	http.Redirect(w, r, "/u/"+login, http.StatusFound)
}

// handleStream streams the current job's progress as Server-Sent Events.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(jobCookie)
	if err != nil {
		http.Error(w, "no sync in progress", http.StatusNotFound)
		return
	}
	j, ok := s.jobs.get(c.Value)
	if !ok {
		http.Error(w, "no sync in progress", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
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
				writeJSONDownload(w, snap, login+"-raw")
				return
			}
		}
	}
	http.Error(w, "no raw data to download — re-sync your 42 data (the raw copy isn't stored)", http.StatusNotFound)
}

// handleDownloadSaved serves the logged-in account's persisted snapshot — exactly
// the curated data we store, for transparency. Always available to the owner.
func (s *Server) handleDownloadSaved(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
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
				writeJSONDownload(w, snapshot.Curate(snap), login+"-curated")
				return
			}
		}
	}
	http.Error(w, "no sync to download — sync your 42 data first", http.StatusNotFound)
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
		render(w, r, pages.SignupForm("Your sync expired. Please sync your 42 data again.", true))
		return
	}
	render(w, r, pages.SignupForm("", false))
}

// handleSignup creates an account, attaching the just-synced job's data and 42
// identity, then logs the user in.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	if !validEmail(email) || len(password) < 8 {
		render(w, r, pages.SignupForm("Enter a valid email and a password of at least 8 characters.", false))
		return
	}
	jobID, j, ok := s.jobFor(r)
	if !ok {
		render(w, r, pages.SignupForm("Your sync expired. Please sync your 42 data again.", true))
		return
	}
	snap, ftID, ftLogin, done := j.result()
	if !done {
		render(w, r, pages.SignupForm("Your sync hasn't finished yet — wait for it to complete.", false))
		return
	}
	hash, err := hashPassword(password)
	if err != nil {
		http.Error(w, "could not hash password", http.StatusInternalServerError)
		return
	}
	id, err := s.store.CreateAccount(r.Context(), email, hash, ftID, ftLogin, snap)
	if errors.Is(err, store.ErrDuplicate) {
		render(w, r, pages.SignupForm("That email or 42 profile already has an account — try logging in.", false))
		return
	}
	if err != nil {
		http.Error(w, "could not create account", http.StatusInternalServerError)
		return
	}
	s.jobs.delete(jobID)
	s.clearCookie(w, jobCookie)
	if err := s.startSession(w, r, id); err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/u/"+ftLogin, http.StatusFound)
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	render(w, r, pages.LoginForm(""))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	acc, hash, err := s.store.AccountByEmail(r.Context(), email)
	if err != nil {
		hash = dummyHash // burn the same argon2 time so a missing email isn't a timing oracle
	}
	if err != nil || !verifyPassword(password, hash) {
		render(w, r, pages.LoginForm("Invalid email or password."))
		return
	}
	if err := s.startSession(w, r, acc.ID); err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/u/"+acc.FtLogin, http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.endSession(w, r)
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleDeleteAccount erases the logged-in account and everything tied to it (GDPR
// Art. 17), ends the session, and returns home.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if err := s.store.DeleteAccount(r.Context(), acc.ID); err != nil {
		http.Error(w, "could not delete account", http.StatusInternalServerError)
		return
	}
	s.endSession(w, r)
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleProfile renders a profile, enforcing the visibility tier: owner sees all;
// a logged-in viewer sees public sections; anonymous sees them only if the owner
// opted the profile public.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	viewer, loggedIn := s.currentAccount(r)
	acc, err := s.store.AccountByLogin(r.Context(), login)
	if errors.Is(err, store.ErrNotFound) {
		renderStatus(w, r, http.StatusNotFound, pages.ProfileNotFound(login, loggedIn))
		return
	}
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	owner := loggedIn && viewer.ID == acc.ID
	if !owner && !loggedIn && !acc.IsPublic {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	d := view.Build(acc.Data, owner, acc.Visibility)
	d.Owner = owner
	d.Login = acc.FtLogin
	render(w, r, pages.Page(d))
}

func (s *Server) handleSettingsForm(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	render(w, r, pages.Settings(s.settingsData(acc, false)))
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	isPublic := r.FormValue("is_public") == "on"
	sections := map[string]bool{}
	for _, t := range view.ToggleableSections {
		sections[t.Key] = r.FormValue("section_"+t.Key) == "on"
	}
	if err := s.store.UpdateVisibility(r.Context(), acc.ID, isPublic, sections); err != nil {
		http.Error(w, "could not save settings", http.StatusInternalServerError)
		return
	}
	acc.IsPublic, acc.Visibility = isPublic, sections
	render(w, r, pages.Settings(s.settingsData(acc, true)))
}

// settingsData renders the current visibility state into the template's shape.
func (s *Server) settingsData(acc store.Account, saved bool) model.SettingsData {
	d := model.SettingsData{IsPublic: acc.IsPublic, Login: acc.FtLogin, Saved: saved}
	for _, t := range view.ToggleableSections {
		d.Toggles = append(d.Toggles, model.SettingsToggle{
			Key:     t.Key,
			Label:   t.Label,
			Public:  view.SectionPublic(acc.Visibility, t.Key),
			HasData: hasData(acc.Data[t.Key]),
		})
	}
	return d
}

// hasData reports whether a snapshot resource holds something worth showing.
func hasData(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "[]" && s != "null"
}
