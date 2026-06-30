package web

import (
	"context"
	"encoding/json"
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

func (f *fakeSender) SendVerification(_ context.Context, to, link string) error {
	f.mu.Lock()
	f.sent++
	f.lastTo = to
	f.lastSubj = "Verify your Fortytwode email"
	f.lastLink = link
	f.mu.Unlock()
	return nil
}

func (f *fakeSender) SendDeletionConfirmation(_ context.Context, to, link string) error {
	f.mu.Lock()
	f.sent++
	f.lastTo = to
	f.lastSubj = "Confirm your Fortytwode account deletion"
	f.lastLink = link
	f.mu.Unlock()
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
		store:            st,
		cfg:              config.Config{RedirectURI: "http://localhost:8080/api/v1/auth/42/callback"},
		loginAttempts:    newAttemptLimiter[string](maxLoginAttempts, loginAttemptWindow),
		passwordAttempts: newAttemptLimiter[int64](maxPasswordAttempts, passwordAttemptWindow),
		verifyResends:    newAttemptLimiter[int64](maxVerifyResends, verifyResendWindow),
		deleteRequests:   newAttemptLimiter[int64](maxDeleteRequests, deleteRequestWindow),
		email:            fs,
	}
	return s, fs
}

// newAccount creates a fresh (unverified) account with the password "password123"
// and returns its id, email and 42 login.
func newAccount(t *testing.T, st *store.Store) (int64, string, string) {
	t.Helper()
	ctx := context.Background()
	u := uniqueN()
	email := fmt.Sprintf("verify-%d@e.st", u)
	login := fmt.Sprintf("verifier%d", u)
	hash, err := hashPassword("password123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	id, err := st.CreateAccount(ctx, email, hash, u, login,
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

func TestLoginGatesUnverifiedAccount(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	id, email, login := newAccount(t, st)

	creds := url.Values{"email": {email}, "password": {"password123"}}

	// Unverified: login starts a session but redirects to the pending page.
	rec, r := postForm(http.MethodPost, routes.APILogIn.URL(), nil, creds)
	s.handleLogin(rec, r)
	if rec.Code != http.StatusFound {
		t.Fatalf("unverified login: status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != routes.PageVerifyPending {
		t.Errorf("unverified login: Location = %q, want %q", got, routes.PageVerifyPending)
	}

	// Verified: login lands on the profile.
	markVerified(t, st, id)
	rec, r = postForm(http.MethodPost, routes.APILogIn.URL(), nil, creds)
	s.handleLogin(rec, r)
	if rec.Code != http.StatusFound {
		t.Fatalf("verified login: status = %d, want 302", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), routes.PageProfile(login); got != want {
		t.Errorf("verified login: Location = %q, want %q", got, want)
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

func TestSettingsEmailChangeReverifies(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, fs := newTestServer(st)
	id, _, login := newAccount(t, st)
	markVerified(t, st, id)
	cookie := sessionCookieFor(t, st, id)

	newEmail := fmt.Sprintf("moved-%d@e.st", uniqueN())
	vals := url.Values{"current_password": {"password123"}, "email": {newEmail}}
	rec, r := postForm(http.MethodPatch, routes.APIAccountEmail.URL(), cookie, vals)
	s.handleSettingsEmail(rec, r)

	// Changing a verified account's email must drop it back to the pending flow.
	if rec.Code != http.StatusFound {
		t.Fatalf("settings email change: status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != routes.PageVerifyPending {
		t.Errorf("settings email change: Location = %q, want %q", got, routes.PageVerifyPending)
	}
	acc, err := st.AccountByLogin(context.Background(), login)
	if err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if acc.Email != newEmail {
		t.Errorf("email = %q, want %q", acc.Email, newEmail)
	}
	if acc.EmailVerified {
		t.Error("account should be unverified again after changing its email")
	}
	fs.waitForSent(t, 1)
	if gotTo, gotSubj, _ := fs.snapshot(); gotTo != newEmail || gotSubj != "Verify your Fortytwode email" {
		t.Errorf("verification email: to=%q subj=%q, want to=%q subj=%q", gotTo, gotSubj, newEmail, "Verify your Fortytwode email")
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
