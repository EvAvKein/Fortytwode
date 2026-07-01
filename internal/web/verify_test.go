package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/config"
	"github.com/EvAvKein/Fortytwode/internal/routes"
	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/EvAvKein/Fortytwode/internal/storetest"
)

// fakeSender records transactional emails instead of sending them, so handler
// tests don't touch the network. The issue* helpers send in a goroutine, so the
// recorder is mutex-guarded; tests can inspect the recorded payloads via the
// last* accessors after calling waitForSent.
type fakeSender struct {
	mu       sync.Mutex
	sent     int
	lastTo   string
	lastSubj string
	lastLink string
}

func (f *fakeSender) record(to, subj, link string) {
	f.mu.Lock()
	f.sent++
	f.lastTo = to
	f.lastSubj = subj
	f.lastLink = link
	f.mu.Unlock()
}

func (f *fakeSender) SendVerification(_ context.Context, to, link string) error {
	f.record(to, "Verify your Fortytwode email", link)
	return nil
}

func (f *fakeSender) SendLogin(_ context.Context, to, link string) error {
	f.record(to, "Your Fortytwode login link", link)
	return nil
}

func (f *fakeSender) SendEmailChange(_ context.Context, to, link string) error {
	f.record(to, "Confirm your new Fortytwode email", link)
	return nil
}

func (f *fakeSender) SendEmailChangeNotice(_ context.Context, to, newEmail string) error {
	f.record(to, "Your Fortytwode email was changed", newEmail)
	return nil
}

func (f *fakeSender) SendDeletionConfirmation(_ context.Context, to, link string) error {
	f.record(to, "Confirm your Fortytwode account deletion", link)
	return nil
}

// waitForSent blocks until at least n emails have been recorded (with a
// timeout), giving the goroutine launched by issueVerification/issueDeletion
// time to run.
func (f *fakeSender) waitForSent(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		f.mu.Lock()
		count := f.sent
		f.mu.Unlock()
		if count >= n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d emails, only %d sent", n, count)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func (f *fakeSender) snapshot() (to, subj, link string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTo, f.lastSubj, f.lastLink
}

var testSeq atomic.Int64

func uniqueN() int64 { return time.Now().UnixNano() + testSeq.Add(1) }

func newTestServer(st *store.Store) (*Server, *fakeSender) {
	fs := &fakeSender{}
	s := &Server{
		store:               st,
		cfg:                 config.Config{RedirectURI: "http://localhost:8080/api/v1/auth/42/callback"},
		loginAttempts:       newAttemptLimiter[string](maxLoginAttempts, loginAttemptWindow),
		verifyResends:       newAttemptLimiter[int64](maxVerifyResends, verifyResendWindow),
		emailChangeRequests: newAttemptLimiter[int64](maxEmailChangeRequests, emailChangeRequestWindow),
		deleteRequests:      newAttemptLimiter[int64](maxDeleteRequests, deleteRequestWindow),
		email:               fs,
	}
	return s, fs
}

// newAccount creates a fresh (unverified) account and returns its id, email and 42
// login.
func newAccount(t *testing.T, st *store.Store) (int64, string, string) {
	t.Helper()
	ctx := context.Background()
	u := uniqueN()
	email := fmt.Sprintf("verify-%d@e.st", u)
	login := fmt.Sprintf("verifier%d", u)
	id, err := st.CreateAccount(ctx, email, u, login,
		map[string]json.RawMessage{"me": json.RawMessage(`{"login":"` + login + `"}`)})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteAccount(ctx, id) })
	return id, email, login
}

// markVerified flips an account to verified using the real store round-trip.
func markVerified(t *testing.T, st *store.Store, id int64) {
	t.Helper()
	ctx := context.Background()
	tok := randomToken()
	if err := st.SetVerifyToken(ctx, id, tokenHash(tok), time.Now()); err != nil {
		t.Fatalf("set verify token: %v", err)
	}
	if _, err := st.VerifyByToken(ctx, tokenHash(tok), verifyTokenTTL); err != nil {
		t.Fatalf("verify token: %v", err)
	}
}

func sessionCookieFor(t *testing.T, st *store.Store, id int64) *http.Cookie {
	t.Helper()
	ctx := context.Background()
	sid := randomToken()
	if err := st.CreateSession(ctx, sid, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSession(ctx, sid) })
	return &http.Cookie{Name: sessionCookie, Value: sid}
}

// postForm builds a form-encoded request to route (carrying cookie, if any) and a
// recorder to serve it into.
func postForm(method, route string, cookie *http.Cookie, vals url.Values) (*httptest.ResponseRecorder, *http.Request) {
	r := httptest.NewRequest(method, route, strings.NewReader(vals.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return httptest.NewRecorder(), r
}

func TestMagicLinkLogin(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, fs := newTestServer(st)
	_, email, login := newAccount(t, st)

	// Existing address: 200 (never a redirect — no session yet) and a login link is mailed.
	rec, r := postForm(http.MethodPost, routes.APILogIn.URL(), nil, url.Values{"email": {email}})
	s.handleLogin(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("login request: status = %d, want 200", rec.Code)
	}
	fs.waitForSent(t, 1)
	to, subj, link := fs.snapshot()
	if to != email || subj != "Your Fortytwode login link" {
		t.Fatalf("login email: to=%q subj=%q, want to=%q subj=%q", to, subj, email, "Your Fortytwode login link")
	}

	// Opening the link renders the confirm interstitial and starts no session (the GET
	// peeks, it doesn't consume).
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	token := u.Query().Get("token")
	rec = httptest.NewRecorder()
	s.handleLoginCallback(rec, httptest.NewRequest(http.MethodGet, routes.PageLoginCallback+"?token="+token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("login callback: status = %d, want 200", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Error("login callback (GET) must not start a session")
		}
	}

	// Confirming (the interstitial's POST) starts a session, verifies the account,
	// and lands on the profile.
	rec, r = postForm(http.MethodPost, routes.APILogInConsume.URL(), nil, url.Values{"token": {token}})
	s.handleLoginConsume(rec, r)
	if rec.Code != http.StatusFound {
		t.Fatalf("login consume: status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != routes.PageProfile(login) {
		t.Errorf("login consume: Location = %q, want %q", got, routes.PageProfile(login))
	}
	var gotCookie bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			gotCookie = true
		}
	}
	if !gotCookie {
		t.Error("login consume should set a session cookie")
	}
	if acc, _ := st.AccountByLogin(context.Background(), login); !acc.EmailVerified {
		t.Error("confirming a login link should verify the account")
	}

	// Reusing the consumed token fails.
	rec, r = postForm(http.MethodPost, routes.APILogInConsume.URL(), nil, url.Values{"token": {token}})
	s.handleLoginConsume(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("consumed login token: status = %d, want 400", rec.Code)
	}
}

func TestMagicLinkLoginUnknownEmailIsSilent(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, fs := newTestServer(st)

	// No account for the address: same 200 page, but nothing is sent (no enumeration).
	rec, r := postForm(http.MethodPost, routes.APILogIn.URL(), nil, url.Values{"email": {fmt.Sprintf("nobody-%d@e.st", uniqueN())}})
	s.handleLogin(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown-email login: status = %d, want 200", rec.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if to, _, _ := fs.snapshot(); to != "" {
		t.Errorf("unknown email should send nothing, but sent to %q", to)
	}
}

func TestVerifyEmailConsumesToken(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	id, _, login := newAccount(t, st)

	token := randomToken()
	if err := st.SetVerifyToken(context.Background(), id, tokenHash(token), time.Now()); err != nil {
		t.Fatalf("set token: %v", err)
	}

	// A valid token (no session: e.g. opened in another browser) shows success.
	rec := httptest.NewRecorder()
	s.handleVerifyEmail(rec, httptest.NewRequest(http.MethodGet, routes.PageVerifyEmail+"?token="+token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "verified") {
		t.Errorf("valid token: body should confirm verification, got:\n%s", rec.Body.String())
	}
	if acc, _ := st.AccountByLogin(context.Background(), login); !acc.EmailVerified {
		t.Error("account not marked verified after consuming the token")
	}

	// Reusing the (now consumed) token fails.
	rec = httptest.NewRecorder()
	s.handleVerifyEmail(rec, httptest.NewRequest(http.MethodGet, routes.PageVerifyEmail+"?token="+token, nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("consumed token: status = %d, want 400", rec.Code)
	}
}

func TestVerifyEmailRejectsBadToken(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)

	for _, tok := range []string{"", "deadbeef"} {
		rec := httptest.NewRecorder()
		s.handleVerifyEmail(rec, httptest.NewRequest(http.MethodGet, routes.PageVerifyEmail+"?token="+tok, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("token %q: status = %d, want 400", tok, rec.Code)
		}
	}
}

func TestVerifyResendRateLimited(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, fs := newTestServer(st)
	id, email, _ := newAccount(t, st)
	cookie := sessionCookieFor(t, st, id)

	resend := func() int {
		rec, r := postForm(http.MethodPost, routes.APIVerifyResend.URL(), cookie, nil)
		s.handleVerifyResend(rec, r)
		return rec.Code
	}

	for i := 0; i < maxVerifyResends; i++ {
		if code := resend(); code != http.StatusFound {
			t.Fatalf("resend %d: status = %d, want 302", i+1, code)
		}
	}
	fs.waitForSent(t, maxVerifyResends)
	if gotTo, gotSubj, _ := fs.snapshot(); gotTo != email || gotSubj != "Verify your Fortytwode email" {
		t.Errorf("resend email: to=%q subj=%q, want to=%q subj=%q", gotTo, gotSubj, email, "Verify your Fortytwode email")
	}
	// Rate-limited: redirects to the pending page with an error param.
	if code := resend(); code != http.StatusFound {
		t.Errorf("resend past the cap: status = %d, want 302", code)
	}
}

func TestVerifyPendingEmailChange(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, fs := newTestServer(st)
	id, _, login := newAccount(t, st)
	cookie := sessionCookieFor(t, st, id)

	newEmail := fmt.Sprintf("corrected-%d@e.st", uniqueN())
	rec, r := postForm(http.MethodPatch, routes.APIVerifyEmailEdit.URL(), cookie, url.Values{"email": {newEmail}})
	s.handleVerifyEmailChange(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("email change: status = %d, want 302", rec.Code)
	}
	wantLoc := routes.PageVerifyPending + "?email=" + url.QueryEscape(newEmail) + "&updated=1"
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("email change: Location = %q, want %q", got, wantLoc)
	}
	acc, err := st.AccountByLogin(context.Background(), login)
	if err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if acc.Email != newEmail {
		t.Errorf("email = %q, want %q", acc.Email, newEmail)
	}
	if acc.EmailVerified {
		t.Error("account should remain unverified after an email change")
	}
	fs.waitForSent(t, 1)
	if gotTo, gotSubj, _ := fs.snapshot(); gotTo != newEmail || gotSubj != "Verify your Fortytwode email" {
		t.Errorf("verification email: to=%q subj=%q, want to=%q subj=%q", gotTo, gotSubj, newEmail, "Verify your Fortytwode email")
	}
}

func TestVerifyPendingEmailChangeRejectsDuplicate(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	idA, _, _ := newAccount(t, st)
	_, emailB, _ := newAccount(t, st)
	cookie := sessionCookieFor(t, st, idA)

	// collide with the other account
	rec, r := postForm(http.MethodPatch, routes.APIVerifyEmailEdit.URL(), cookie, url.Values{"email": {emailB}})
	s.handleVerifyEmailChange(rec, r)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate email: status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already in use") {
		t.Errorf("duplicate email: body should explain the conflict, got:\n%s", rec.Body.String())
	}
}

func TestVerifyEmailChangeRateLimited(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	id, _, _ := newAccount(t, st)
	cookie := sessionCookieFor(t, st, id)

	change := func() int {
		vals := url.Values{"email": {fmt.Sprintf("changed-%d@e.st", uniqueN())}}
		rec, r := postForm(http.MethodPatch, routes.APIVerifyEmailEdit.URL(), cookie, vals)
		s.handleVerifyEmailChange(rec, r)
		return rec.Code
	}

	// The change-and-resend path shares the resend budget, so it can't be looped to
	// spray verification mail at arbitrary addresses.
	for i := 0; i < maxVerifyResends; i++ {
		if code := change(); code != http.StatusFound {
			t.Fatalf("change %d: status = %d, want 302", i+1, code)
		}
	}
	// Rate-limited: redirects to the pending page with an error param.
	if code := change(); code != http.StatusFound {
		t.Errorf("change past the cap: status = %d, want 302", code)
	}
}

func TestSettingsEmailChangeConfirmFirst(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, fs := newTestServer(st)
	id, oldEmail, login := newAccount(t, st)
	markVerified(t, st, id)
	cookie := sessionCookieFor(t, st, id)

	newEmail := fmt.Sprintf("moved-%d@e.st", uniqueN())
	rec, r := postForm(http.MethodPatch, routes.APIAccountEmail.URL(), cookie, url.Values{"email": {newEmail}})
	s.handleSettingsEmail(rec, r)

	// The request only mails a confirmation link; the account email is untouched.
	// Post/Redirect/Get: it redirects to settings with the pending address so the
	// "sent" flash survives the reload the client JS does on success.
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("settings email change: status = %d, want 303", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), routes.PageSettings+"?email_pending="+url.QueryEscape(newEmail); got != want {
		t.Fatalf("settings email change: Location = %q, want %q", got, want)
	}
	acc, err := st.AccountByLogin(context.Background(), login)
	if err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if acc.Email != oldEmail {
		t.Errorf("email changed before confirmation: got %q, want %q", acc.Email, oldEmail)
	}
	fs.waitForSent(t, 1)
	to, subj, link := fs.snapshot()
	if to != newEmail || subj != "Confirm your new Fortytwode email" {
		t.Fatalf("confirmation email: to=%q subj=%q, want to=%q subj=%q", to, subj, newEmail, "Confirm your new Fortytwode email")
	}

	// Opening the confirmation link only peeks the token and renders the interstitial:
	// a prefetch (mail scanner) of this GET must not apply the change.
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	token := u.Query().Get("token")
	req := httptest.NewRequest(http.MethodGet, routes.PageConfirmEmail+"?token="+token, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.handleConfirmEmail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm-email GET (peek): status = %d, want 200", rec.Code)
	}
	// The interstitial names the account the change belongs to (by 42 login), since
	// the link may be opened while logged into a different account, or none.
	if want := "account of <strong>" + login; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("confirm-email interstitial does not name the account %q", login)
	}
	if acc, _ := st.AccountByLogin(context.Background(), login); acc.Email != oldEmail {
		t.Errorf("email changed by the peek GET: got %q, want %q (unchanged)", acc.Email, oldEmail)
	}

	// The interstitial's button POSTs the token, which promotes the pending address
	// (from the account's own session) and lands back on settings.
	rec, req = postForm(http.MethodPost, routes.APIAccountEmailConfirm.URL(), cookie, url.Values{"token": {token}})
	s.handleConfirmEmailConsume(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("confirm email: status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != routes.PageSettings {
		t.Errorf("confirm email: Location = %q, want %q", got, routes.PageSettings)
	}
	if acc, _ := st.AccountByLogin(context.Background(), login); acc.Email != newEmail {
		t.Errorf("email after confirmation = %q, want %q", acc.Email, newEmail)
	}

	// Confirming also notifies the previous address that the email was changed, so a
	// silent takeover leaves a trail. It's the second send (after the confirmation).
	fs.waitForSent(t, 2)
	if to, subj, changedTo := fs.snapshot(); to != oldEmail || subj != "Your Fortytwode email was changed" || changedTo != newEmail {
		t.Errorf("change notice: to=%q subj=%q changedTo=%q, want to=%q subj=%q changedTo=%q",
			to, subj, changedTo, oldEmail, "Your Fortytwode email was changed", newEmail)
	}
}

// TestEmailChangeConfirmElsewhereSignsOutSessions covers the "link opened in a
// different browser" path: confirming while not logged in as the account must
// still sign out all of that account's sessions, as the interstitial promises.
func TestEmailChangeConfirmElsewhereSignsOutSessions(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, fs := newTestServer(st)
	id, _, login := newAccount(t, st)
	markVerified(t, st, id)
	// Two live sessions on the account (e.g. two devices). One drives the change
	// request; both must be gone after a confirm from elsewhere.
	cookieA := sessionCookieFor(t, st, id)
	cookieB := sessionCookieFor(t, st, id)

	newEmail := fmt.Sprintf("moved-%d@e.st", uniqueN())
	rec, r := postForm(http.MethodPatch, routes.APIAccountEmail.URL(), cookieA, url.Values{"email": {newEmail}})
	s.handleSettingsEmail(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("request email change: status = %d, want 303", rec.Code)
	}
	fs.waitForSent(t, 1)
	_, _, link := fs.snapshot()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	token := u.Query().Get("token")

	// Confirm with NO session cookie — i.e. from a browser not logged into the account.
	rec, req := postForm(http.MethodPost, routes.APIAccountEmailConfirm.URL(), nil, url.Values{"token": {token}})
	s.handleConfirmEmailConsume(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm elsewhere: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Email verified") {
		t.Errorf("confirm elsewhere: expected the success result page")
	}
	if acc, _ := st.AccountByLogin(context.Background(), login); acc.Email != newEmail {
		t.Errorf("email after confirmation = %q, want %q", acc.Email, newEmail)
	}
	// Both prior sessions must be gone.
	ctx := context.Background()
	for name, sid := range map[string]string{"A": cookieA.Value, "B": cookieB.Value} {
		if _, err := st.SessionAccount(ctx, sid); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("session %s survived the email change: err = %v, want ErrNotFound", name, err)
		}
	}
}

// TestSettingsEmailChangeMultipart guards against the parsing regression where
// handleSettingsEmail called ParseForm but not ParseMultipartForm: the browser
// submits this form as multipart/form-data (fetch + FormData), so the email field
// came back empty and every valid address was rejected as invalid.
func TestSettingsEmailChangeMultipart(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, fs := newTestServer(st)
	id, _, _ := newAccount(t, st)
	markVerified(t, st, id)
	cookie := sessionCookieFor(t, st, id)

	newEmail := fmt.Sprintf("moved-%d@e.st", uniqueN())
	rec, r := postMultipart(http.MethodPatch, routes.APIAccountEmail.URL(), cookie, map[string]string{"email": newEmail})
	s.handleSettingsEmail(rec, r)

	// A parsed, valid address is accepted: the handler redirects to settings
	// carrying the pending address (not a 422 with "Enter a valid email address").
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("multipart email change: status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), routes.PageSettings+"?email_pending="+url.QueryEscape(newEmail); got != want {
		t.Fatalf("multipart email change: Location = %q, want %q", got, want)
	}
	fs.waitForSent(t, 1)
	if to, _, _ := fs.snapshot(); to != newEmail {
		t.Errorf("confirmation email to = %q, want %q", to, newEmail)
	}
}

// TestSettingsFlashEmailPending checks the GET side of the Post/Redirect/Get:
// /settings?email_pending=<addr> renders the "confirmation link sent" message.
func TestSettingsFlashEmailPending(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	id, _, _ := newAccount(t, st)
	markVerified(t, st, id)
	cookie := sessionCookieFor(t, st, id)

	pending := fmt.Sprintf("moved-%d@e.st", uniqueN())
	req := httptest.NewRequest(http.MethodGet, routes.PageSettings+"?email_pending="+url.QueryEscape(pending), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleSettingsForm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("settings flash: status = %d, want 200", rec.Code)
	}
	if want := "Confirmation link sent to " + pending; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("settings page missing pending-email flash %q", want)
	}
}

// TestSettingsEmailChangeProbeRateLimited checks that the "already in use" response
// spends the per-account budget, so it can't be repeated to learn which emails have
// accounts: after the cap the endpoint 429s instead of answering.
func TestSettingsEmailChangeProbeRateLimited(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	id, _, _ := newAccount(t, st)
	markVerified(t, st, id)
	cookie := sessionCookieFor(t, st, id)

	// Another account whose (taken) address the actor will probe repeatedly.
	_, victimEmail, _ := newAccount(t, st)

	// The first maxEmailChangeRequests probes reveal "already in use"; the next is
	// rate-limited (429) rather than continuing to answer the existence question.
	for i := 0; i < maxEmailChangeRequests; i++ {
		rec, r := postForm(http.MethodPatch, routes.APIAccountEmail.URL(), cookie, url.Values{"email": {victimEmail}})
		s.handleSettingsEmail(rec, r)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("probe %d: status = %d, want 422", i, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "already in use") {
			t.Fatalf("probe %d: body missing 'already in use'", i)
		}
	}
	rec, r := postForm(http.MethodPatch, routes.APIAccountEmail.URL(), cookie, url.Values{"email": {victimEmail}})
	s.handleSettingsEmail(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-cap probe: status = %d, want 429", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "already in use") {
		t.Errorf("over-cap probe still leaked existence via 'already in use'")
	}
}

func TestUnverifiedOwnerProfileRedirects(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	id, _, login := newAccount(t, st)
	cookie := sessionCookieFor(t, st, id)

	h := s.routes()
	r := httptest.NewRequest(http.MethodGet, routes.PageProfile(login), nil)
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("unverified owner profile: status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != routes.PageVerifyPending {
		t.Errorf("unverified owner profile: Location = %q, want %q", got, routes.PageVerifyPending)
	}
}
