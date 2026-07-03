package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/routes"
	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/EvAvKein/Fortytwode/internal/view/pages"
)

// originBase returns the public origin (scheme+host) derived from the configured
// OAuth redirect URI, so emailed links resolve to the right host in dev and prod
// automatically. It is empty when the redirect URI is missing or relative.
func (s *Server) originBase() string {
	if u, err := url.Parse(s.cfg.RedirectURI); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return ""
}

// actionLink builds the absolute URL an emailed link (verify, login, email-change)
// points at: the public origin, the page path, and the ?token= query.
func (s *Server) actionLink(page, token string) string {
	return s.originBase() + page + "?token=" + url.QueryEscape(token)
}

// issueVerification mints a fresh token, persists its hash and the send time, and
// fires the email asynchronously. A dropped send isn't fatal — the pending page's
// resend button covers it — so a send error is logged, not surfaced.
func (s *Server) issueVerification(ctx context.Context, accountID int64, email, login string) {
	token := randomToken()
	if err := s.store.SetVerifyToken(ctx, accountID, tokenHash(token), time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: set verify token for account %d: %v\n", accountID, err)
		return
	}
	link := s.actionLink(routes.PageVerifyEmail, token)
	go func() {
		if err := s.email.SendVerification(context.Background(), email, login, link); err != nil {
			fmt.Fprintf(os.Stderr, "warning: send verification email for account %d: %v\n", accountID, err)
		}
	}()
}

// issueLoginLink mints and mails a magic-link login token (mirrors issueVerification;
// a dropped send is logged, not surfaced — the login form can be re-submitted).
func (s *Server) issueLoginLink(ctx context.Context, accountID int64, email string) {
	token := randomToken()
	if err := s.store.SetLoginToken(ctx, accountID, tokenHash(token), time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: set login token for account %d: %v\n", accountID, err)
		return
	}
	link := s.actionLink(routes.PageLoginCallback, token)
	go func() {
		if err := s.email.SendLogin(context.Background(), email, link); err != nil {
			fmt.Fprintf(os.Stderr, "warning: send login email for account %d: %v\n", accountID, err)
		}
	}()
}

// issueEmailChange parks the requested new address with a fresh confirmation token and
// mails the link there; the account's real email switches only once it's consumed.
func (s *Server) issueEmailChange(ctx context.Context, accountID int64, newEmail string) {
	token := randomToken()
	if err := s.store.SetEmailChange(ctx, accountID, newEmail, tokenHash(token), time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: set email-change token for account %d: %v\n", accountID, err)
		return
	}
	link := s.actionLink(routes.PageConfirmEmail, token)
	go func() {
		if err := s.email.SendEmailChange(context.Background(), newEmail, link); err != nil {
			fmt.Fprintf(os.Stderr, "warning: send email-change email for account %d: %v\n", accountID, err)
		}
	}()
}

// badLink renders the shared "this link is invalid or expired" failure page (400),
// used by every emailed-link handler (verify, login, email-change).
func (s *Server) badLink(w http.ResponseWriter, r *http.Request) {
	renderStatus(w, r, http.StatusBadRequest, pages.VerifyResult(false, s.viewerLogin(r)))
}

// gateUnverified redirects an authenticated-but-unverified account to the pending
// page, reporting whether it handled the request. Account-action handlers call it
// right after resolving currentAccount so an unverified session can't act.
func (s *Server) gateUnverified(w http.ResponseWriter, r *http.Request, acc store.Account) bool {
	if !acc.EmailVerified {
		http.Redirect(w, r, routes.PageVerifyPending, http.StatusFound)
		return true
	}
	return false
}

// destAfterAuth is where a freshly-authenticated account lands: the pending page
// when unverified, otherwise its profile.
func destAfterAuth(acc store.Account) string {
	if !acc.EmailVerified {
		return routes.PageVerifyPending
	}
	return routes.PageProfile(acc.FtLogin)
}

// requireUnverified resolves the current account for the resend/change handlers:
// it rejects cross-site posts, requires a session, and bounces an already-verified
// account to its profile. Returns false (after responding) when the caller should stop.
func (s *Server) requireUnverified(w http.ResponseWriter, r *http.Request) (store.Account, bool) {
	if s.rejectCrossSite(w, r) {
		return store.Account{}, false
	}
	acc, ok := s.currentAccount(r)
	if !ok {
		renderStatus(w, r, http.StatusUnauthorized, pages.LoginForm("Please log in to continue", ""))
		return store.Account{}, false
	}
	if acc.EmailVerified {
		http.Redirect(w, r, routes.PageProfile(acc.FtLogin), http.StatusFound)
		return store.Account{}, false
	}
	return acc, true
}

// handleVerifyPending shows the "check your email" page to an authenticated but
// unverified account, with resend and email-correction. Anonymous visitors go to
// login; already-verified ones go to their profile. Query parameters notice,
// error, and email carry state from redirecting action handlers.
func (s *Server) handleVerifyPending(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		http.Redirect(w, r, routes.PageLogin, http.StatusFound)
		return
	}
	if acc.EmailVerified {
		http.Redirect(w, r, routes.PageProfile(acc.FtLogin), http.StatusFound)
		return
	}
	notice, errMsg := "", ""
	switch r.URL.Query().Get("resent") {
	case "1":
		notice = "Verification email sent, check your inbox."
	}
	switch r.URL.Query().Get("updated") {
	case "1":
		notice = "Email updated, we sent a new verification link."
	}
	switch r.URL.Query().Get("error") {
	case "rate-limited":
		errMsg = "Too many request, wait a little before resending."
	}
	render(w, r, pages.VerifyPending(acc.Email, errMsg, notice))
}

// handleVerifyEmail consumes the token from the verification link. A valid,
// unexpired token marks the account verified; anything else renders the failure
// page (with a path back to request a new link).
func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAttemptAllowed(w, r) {
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		s.badLink(w, r)
		return
	}
	acc, err := s.store.VerifyByToken(r.Context(), tokenHash(token), verifyTokenTTL)
	if errors.Is(err, store.ErrNotFound) {
		s.recordBadToken(r)
		s.badLink(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	// If the same account is logged in (the common case: they signed up in this
	// browser), drop them straight onto their now-unblocked profile.
	if cur, ok := s.currentAccount(r); ok && cur.ID == acc.ID {
		http.Redirect(w, r, routes.PageProfile(acc.FtLogin), http.StatusFound)
		return
	}
	render(w, r, pages.VerifyResult(true, s.viewerLogin(r)))
}

// verifyResendAllowed enforces the per-account send cap shared by the resend and
// change-and-resend paths. It redirects to the pending page with an error query
// param and returns false at the limit; otherwise it records the send and returns
// true. One shared budget keeps the email-correction form from being looped to
// spray mail at arbitrary addresses.
func (s *Server) verifyResendAllowed(w http.ResponseWriter, r *http.Request, acc store.Account) bool {
	if !s.verifyResends.allowed(acc.ID) {
		http.Redirect(w, r, routes.PageVerifyPending+"?error=rate-limited", http.StatusFound)
		return false
	}
	s.verifyResends.recordFailed(acc.ID)
	return true
}

// handleVerifyResend re-sends the verification email, rate-limited per account.
func (s *Server) handleVerifyResend(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.requireUnverified(w, r)
	if !ok {
		return
	}
	if !s.verifyResendAllowed(w, r, acc) {
		return
	}
	s.issueVerification(r.Context(), acc.ID, acc.Email, acc.FtLogin)
	http.Redirect(w, r, routes.PageVerifyPending+"?resent=1", http.StatusFound)
}

// handleVerifyEmailChange lets an unverified account correct its email (e.g. a
// typo, or a legacy placeholder), then issues a fresh link to the new address. The
// session already proves ownership, so no password is re-checked here; mail spray
// is bounded by the shared per-account resend cap (verifyResendAllowed), checked
// before the address is touched so an over-cap request changes nothing.
func (s *Server) handleVerifyEmailChange(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.requireUnverified(w, r)
	if !ok {
		return
	}
	newEmail, ok := parseEmail(r.FormValue("email"))
	if !ok {
		renderStatus(w, r, http.StatusUnprocessableEntity,
			pages.VerifyPending(acc.Email, "Enter a valid email address.", ""))
		return
	}
	if !s.verifyResendAllowed(w, r, acc) {
		return
	}
	if err := s.store.UpdateEmail(r.Context(), acc.ID, newEmail); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			renderStatus(w, r, http.StatusUnprocessableEntity,
				pages.VerifyPending(acc.Email, "That email is already in use.", ""))
			return
		}
		http.Error(w, "Could not update email", http.StatusInternalServerError)
		return
	}
	s.issueVerification(r.Context(), acc.ID, newEmail, acc.FtLogin)
	http.Redirect(w, r, routes.PageVerifyPending+"?email="+url.QueryEscape(newEmail)+"&updated=1", http.StatusFound)
}
