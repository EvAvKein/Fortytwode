package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHashVerify(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("correct horse battery staple", hash) {
		t.Error("correct password rejected")
	}
	if verifyPassword("wrong", hash) {
		t.Error("wrong password accepted")
	}
	if verifyPassword("correct horse battery staple", hash+"x") {
		t.Error("tampered hash accepted")
	}
}

func TestDummyHashUsable(t *testing.T) {
	// handleLogin verifies against dummyHash when an email matches no account; a
	// blank value (hashPassword failing at init) would skip the argon2 work and
	// reopen the timing oracle.
	if dummyHash == "" {
		t.Fatal("dummyHash is empty")
	}
	if verifyPassword("anything", dummyHash) {
		t.Error("dummyHash should not verify arbitrary passwords")
	}
}

func TestClientKey(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/sync", nil)
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "forged"})

	// An unvalidated session cookie must not become the key — a client could mint
	// a fresh one per request and bypass the per-client cap.
	if got := s.clientKey(req, false); got != "ip:1.2.3.4" {
		t.Errorf("invalid session: key = %q, want ip:1.2.3.4", got)
	}
	if got := s.clientKey(req, true); got != "sid:forged" {
		t.Errorf("valid session: key = %q, want sid:forged", got)
	}
}

func TestSyncRejectsCrossSite(t *testing.T) {
	s := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/api/sync", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	s.handleSync(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site sync: status = %d, want 403", rec.Code)
	}

	// Same-origin (and header-less) requests proceed to the OAuth redirect.
	for _, site := range []string{"same-origin", ""} {
		req := httptest.NewRequest(http.MethodGet, "/api/sync", nil)
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		rec := httptest.NewRecorder()
		s.handleSync(rec, req)
		if rec.Code != http.StatusFound {
			t.Errorf("Sec-Fetch-Site=%q: status = %d, want 302", site, rec.Code)
		}
	}
}

func TestValidEmail(t *testing.T) {
	cases := map[string]bool{
		"a@b.co": true, "user@school.42.fr": true,
		"nope": false, "no@at": false, "@b.co": false, "a@.co": true, // loose by design
	}
	for in, want := range cases {
		if got := validEmail(in); got != want {
			t.Errorf("validEmail(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStreamEmitsDone(t *testing.T) {
	s := &Server{jobs: newJobRegistry()}
	id, j, _ := s.jobs.create("")
	j.finish(map[string]json.RawMessage{"me": json.RawMessage(`{}`)}, 42, "tester")

	req := httptest.NewRequest(http.MethodGet, "/api/fetch/stream", nil)
	req.AddCookie(&http.Cookie{Name: jobCookie, Value: id})
	rec := httptest.NewRecorder()
	s.handleStream(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "event: done") {
		t.Errorf("expected a done event, got:\n%s", body)
	}
	if !strings.Contains(body, `"status":"done"`) {
		t.Errorf("expected done status payload, got:\n%s", body)
	}
	if strings.Contains(body, "matched") {
		t.Errorf("unmatched job should omit the matched flag, got:\n%s", body)
	}
}

func TestStreamSignalsMatch(t *testing.T) {
	s := &Server{jobs: newJobRegistry()}
	id, j, _ := s.jobs.create("")
	j.linkAccount(7, "tester")
	j.finish(map[string]json.RawMessage{"me": json.RawMessage(`{}`)}, 42, "tester")

	req := httptest.NewRequest(http.MethodGet, "/api/fetch/stream", nil)
	req.AddCookie(&http.Cookie{Name: jobCookie, Value: id})
	rec := httptest.NewRecorder()
	s.handleStream(rec, req)

	if body := rec.Body.String(); !strings.Contains(body, `"matched":true`) {
		t.Errorf("expected matched flag in the done payload, got:\n%s", body)
	}
}

func TestCreateRejectsConcurrentClient(t *testing.T) {
	r := newJobRegistry()

	_, j1, ok := r.create("ip:1.2.3.4")
	if !ok || j1 == nil {
		t.Fatal("first create for a client should be accepted")
	}
	if _, _, ok := r.create("ip:1.2.3.4"); ok {
		t.Error("a second concurrent create for the same client should be refused")
	}
	if _, _, ok := r.create("ip:5.6.7.8"); !ok {
		t.Error("a different client should be accepted")
	}

	// Once the first job finishes, the client may sync again.
	j1.finish(nil, 0, "")
	if _, _, ok := r.create("ip:1.2.3.4"); !ok {
		t.Error("client should be accepted again after its job finished")
	}

	// A blank key (unidentifiable client) skips the cap entirely.
	if _, _, ok := r.create(""); !ok {
		t.Error("blank client key should be accepted")
	}
	if _, _, ok := r.create(""); !ok {
		t.Error("blank client key should always be accepted (cap skipped)")
	}
}

func TestSecureCookieFlag(t *testing.T) {
	for _, secure := range []bool{false, true} {
		s := &Server{secure: secure}
		rec := httptest.NewRecorder()
		s.setCookie(rec, "x", "v", time.Hour)
		c := rec.Result().Cookies()[0]
		if c.Secure != secure {
			t.Errorf("secure=%v: cookie.Secure=%v", secure, c.Secure)
		}
		if !c.HttpOnly {
			t.Error("cookie should always be HttpOnly")
		}
	}
}

func TestNotFoundHandler(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleNotFound(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "doesn't exist") {
		t.Errorf("expected the 404 body, got:\n%s", rec.Body.String())
	}
}

func TestRoutes(t *testing.T) {
	h := (&Server{}).routes()
	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/nope", http.StatusNotFound},               // unmatched page → styled 404
		{"POST", "/users/anyone", http.StatusNotFound},      // wrong method on a page route → friendly 404
		{"GET", "/api/logout", http.StatusMethodNotAllowed}, // logout is POST-only → 405 preserved
		{"POST", "/api/sync", http.StatusMethodNotAllowed},  // sync is GET-only → 405
		{"GET", "/api/nope", http.StatusNotFound},           // unknown API path → plain 404
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}

	// The preserved 405 should still advertise the allowed method.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/logout", nil))
	if got := rec.Header().Get("Allow"); got != "POST" {
		t.Errorf(`GET /api/logout Allow = %q, want "POST"`, got)
	}
}
