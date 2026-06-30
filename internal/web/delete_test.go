package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/routes"
	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/EvAvKein/Fortytwode/internal/storetest"
)

// requestDelete drives handleRequestDelete for a verified, logged-in account and
// returns the response recorder.
func requestDelete(s *Server, cookie *http.Cookie) *httptest.ResponseRecorder {
	rec, r := postForm(http.MethodDelete, routes.APIAccountDelete.URL(), cookie, nil)
	s.handleRequestDelete(rec, r)
	return rec
}

func TestRequestDeleteEmailsLinkWithoutDeleting(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, fs := newTestServer(st)
	id, email, login := newAccount(t, st)
	markVerified(t, st, id)
	cookie := sessionCookieFor(t, st, id)

	rec := requestDelete(s, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("request delete: status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != routes.PageSettings+"?deletion=requested" {
		t.Errorf("request delete: Location = %q, want %q", got, routes.PageSettings+"?deletion=requested")
	}
	// The account must still exist — only a confirmation link was issued.
	if _, err := st.AccountByLogin(context.Background(), login); err != nil {
		t.Fatalf("account should survive a deletion request: %v", err)
	}
	fs.waitForSent(t, 1)
	if gotTo, gotSubj, gotLink := fs.snapshot(); gotTo != email || gotSubj != "Confirm your Fortytwode account deletion" || gotLink == "" {
		t.Errorf("deletion email: to=%q subj=%q linkEmpty=%v, want to=%q subj=%q non-empty link", gotTo, gotSubj, gotLink == "", email, "Confirm your Fortytwode account deletion")
	}
}

func TestRequestDeleteGatesUnverified(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	id, _, _ := newAccount(t, st) // left unverified
	cookie := sessionCookieFor(t, st, id)

	rec := requestDelete(s, cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("unverified request delete: status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != routes.PageVerifyPending {
		t.Errorf("unverified request delete: Location = %q, want %q", got, routes.PageVerifyPending)
	}
}

func TestRequestDeleteRateLimited(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	id, _, _ := newAccount(t, st)
	markVerified(t, st, id)
	cookie := sessionCookieFor(t, st, id)

	for i := 0; i < maxDeleteRequests; i++ {
		if code := requestDelete(s, cookie).Code; code != http.StatusSeeOther {
			t.Fatalf("request %d: status = %d, want 303", i+1, code)
		}
	}
	if code := requestDelete(s, cookie).Code; code != http.StatusTooManyRequests {
		t.Errorf("request past the cap: status = %d, want 429", code)
	}
}

func TestConfirmDeleteErasesAccountAndSession(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)
	id, _, login := newAccount(t, st)
	markVerified(t, st, id)
	cookie := sessionCookieFor(t, st, id)
	ctx := context.Background()

	// Issue a deletion token via the real store round-trip.
	token := randomToken()
	if err := st.SetDeleteToken(ctx, id, tokenHash(token), time.Now()); err != nil {
		t.Fatalf("set delete token: %v", err)
	}

	// The confirmation page renders for a live token (read-only — nothing deleted).
	rec := httptest.NewRecorder()
	s.handleDeletePending(rec, httptest.NewRequest(http.MethodGet, routes.PageConfirmDelete+"?token="+token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm page: status = %d, want 200", rec.Code)
	}
	if _, err := st.AccountByLogin(ctx, login); err != nil {
		t.Fatalf("confirm page must not delete the account: %v", err)
	}

	// Confirming erases the account and cascades the session away.
	rec, r := postForm(http.MethodPost, routes.APIAccountDeleteConfirm.URL(), cookie, url.Values{"token": {token}})
	s.handleConfirmDelete(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm delete: status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != routes.PageHome {
		t.Errorf("confirm delete: Location = %q, want %q", got, routes.PageHome)
	}
	if _, err := st.AccountByLogin(ctx, login); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("account after confirm: got %v, want ErrNotFound", err)
	}
	if _, err := st.SessionAccount(ctx, cookie.Value); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("session should cascade away: got %v, want ErrNotFound", err)
	}

	// Replaying the now-consumed token shows the failure page.
	rec = httptest.NewRecorder()
	s.handleDeletePending(rec, httptest.NewRequest(http.MethodGet, routes.PageConfirmDelete+"?token="+token, nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("replayed token: status = %d, want 400", rec.Code)
	}
}

func TestConfirmDeleteRejectsBadToken(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	s, _ := newTestServer(st)

	for _, tok := range []string{"", "deadbeef"} {
		rec, r := postForm(http.MethodPost, routes.APIAccountDeleteConfirm.URL(), nil, url.Values{"token": {tok}})
		s.handleConfirmDelete(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("token %q: status = %d, want 400", tok, rec.Code)
		}
	}
}
