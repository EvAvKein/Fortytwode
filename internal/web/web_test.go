package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/routes"
	"github.com/EvAvKein/Fortytwode/internal/storetest"
	"github.com/EvAvKein/Fortytwode/internal/view"
)

func TestClientKey(t *testing.T) {
	t.Parallel()
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, routes.API42Sync.URL(), nil)
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
	t.Parallel()
	s := &Server{}

	req := httptest.NewRequest(http.MethodGet, routes.API42Sync.URL(), nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	s.handleSync(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site sync: status = %d, want 403", rec.Code)
	}

	// Same-origin (and header-less) requests proceed to the OAuth redirect.
	for _, site := range []string{"same-origin", ""} {
		req := httptest.NewRequest(http.MethodGet, routes.API42Sync.URL(), nil)
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

func TestParseEmail(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"a@b.co": true, "user@school.42.fr": true, "first.last+tag@b.co": true,
		"nope": false, "no@at": false, "@b.co": false, "a@.co": false,
		"a b@c.d":                          false, // whitespace
		"Name <a@b.co>":                    false, // display-name form must not slip through
		"a@b.co\r\nBcc: x":                 false, // header-injection-shaped
		strings.Repeat("a", 250) + "@b.co": false, // over the SMTP length limit
	}
	for in, want := range cases {
		if _, got := parseEmail(in); got != want {
			t.Errorf("parseEmail(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTokenAttemptLimiter(t *testing.T) {
	t.Parallel()
	s := &Server{tokenAttempts: newAttemptLimiter[string](maxTokenAttempts, tokenAttemptWindow)}
	req := httptest.NewRequest(http.MethodGet, routes.PageVerifyEmail+"?token=bogus", nil)
	req.Header.Set("X-Real-IP", "1.2.3.4")

	for i := range maxTokenAttempts {
		if !s.tokenAttemptAllowed(httptest.NewRecorder(), req) {
			t.Fatalf("attempt %d should be allowed", i)
		}
		s.recordBadToken(req)
	}
	rec := httptest.NewRecorder()
	if s.tokenAttemptAllowed(rec, req) {
		t.Error("client over the cap should be blocked")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}

	// The cap is per-client: a different IP is unaffected.
	other := httptest.NewRequest(http.MethodGet, routes.PageVerifyEmail+"?token=bogus", nil)
	other.Header.Set("X-Real-IP", "5.6.7.8")
	if !s.tokenAttemptAllowed(httptest.NewRecorder(), other) {
		t.Error("a different client should still be allowed")
	}
}

func TestStreamEmitsDone(t *testing.T) {
	t.Parallel()
	s := &Server{jobs: newJobRegistry()}
	id, j, _ := s.jobs.create("")
	j.finish(map[string]json.RawMessage{"me": json.RawMessage(`{}`)}, 42, "tester")

	req := httptest.NewRequest(http.MethodGet, routes.APISyncStream.URL(), nil)
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
	t.Parallel()
	s := &Server{jobs: newJobRegistry()}
	id, j, _ := s.jobs.create("")
	j.linkAccount(7, "tester")
	j.finish(map[string]json.RawMessage{"me": json.RawMessage(`{}`)}, 42, "tester")

	req := httptest.NewRequest(http.MethodGet, routes.APISyncStream.URL(), nil)
	req.AddCookie(&http.Cookie{Name: jobCookie, Value: id})
	rec := httptest.NewRecorder()
	s.handleStream(rec, req)

	if body := rec.Body.String(); !strings.Contains(body, `"matched":true`) {
		t.Errorf("expected matched flag in the done payload, got:\n%s", body)
	}
}

func TestCreateRejectsConcurrentClient(t *testing.T) {
	t.Parallel()
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

func TestRunningCount(t *testing.T) {
	t.Parallel()
	r := newJobRegistry()

	if n := r.runningCount(); n != 0 {
		t.Errorf("empty registry: got %d running, want 0", n)
	}

	_, j1, _ := r.create("ip:1.2.3.4")
	_, j2, _ := r.create("ip:5.6.7.8")
	if n := r.runningCount(); n != 2 {
		t.Errorf("two live syncs: got %d running, want 2", n)
	}

	// Finished and failed jobs stay in the registry but no longer count.
	j1.finish(nil, 0, "")
	if n := r.runningCount(); n != 1 {
		t.Errorf("after one finished: got %d running, want 1", n)
	}
	j2.fail(fmt.Errorf("boom"))
	if n := r.runningCount(); n != 0 {
		t.Errorf("after the other failed: got %d running, want 0", n)
	}
}

func TestMarkSlowLatches(t *testing.T) {
	t.Parallel()
	r := newJobRegistry()
	_, j, _ := r.create("")

	if j.state().Slow {
		t.Error("a fresh job should not be marked slow")
	}

	j.markSlow()
	if !j.state().Slow {
		t.Error("state should report slow after markSlow")
	}

	// The flag is sticky: later progress (e.g. once traffic clears) must not clear it.
	j.setProgress(3, 5, "projects")
	if !j.state().Slow {
		t.Error("slow should stay latched after subsequent progress updates")
	}
}

func TestHasRunning(t *testing.T) {
	t.Parallel()
	r := newJobRegistry()

	if r.hasRunning("ip:1.2.3.4") {
		t.Error("no job started yet: hasRunning should be false")
	}

	_, j, ok := r.create("ip:1.2.3.4")
	if !ok {
		t.Fatal("create should be accepted")
	}
	if !r.hasRunning("ip:1.2.3.4") {
		t.Error("a running job for the client: hasRunning should be true")
	}
	if r.hasRunning("ip:5.6.7.8") {
		t.Error("a different client has no job: hasRunning should be false")
	}

	// A finished job no longer counts as running.
	j.finish(nil, 0, "")
	if r.hasRunning("ip:1.2.3.4") {
		t.Error("finished job: hasRunning should be false")
	}

	// A blank key is never tracked, so it always reports false.
	if _, _, ok := r.create(""); !ok {
		t.Fatal("blank-key create should be accepted")
	}
	if r.hasRunning("") {
		t.Error("blank client key should always report not-running")
	}
}

func TestSecureCookieFlag(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// fakeClock is a controllable time source for the rate-limiter tests, letting
// them advance past a limiter's window deterministically instead of sleeping.
// The limiter drives it from a single goroutine, so no locking is needed.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// testAttemptLimiterBehaviour exercises the full lifecycle of an attemptLimiter:
// it allows up to max attempts, blocks the next one, recovers after the window
// expires, resets on clear, and tracks keys independently. It is generic so the
// int64 (account ID) and string (email) limiters share one test body.
func testAttemptLimiterBehaviour[T comparable](t *testing.T, key, other T) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(0, 0)}
	limiter := newAttemptLimiter[T](3, 100*time.Millisecond)
	limiter.now = clk.now

	// The first three attempts are allowed.
	for i := 0; i < 3; i++ {
		if !limiter.allowed(key) {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		limiter.recordFailed(key)
	}

	// The fourth attempt is blocked.
	if limiter.allowed(key) {
		t.Error("fourth attempt should be blocked")
	}

	// After the window passes, attempts are allowed again.
	clk.advance(150 * time.Millisecond)
	if !limiter.allowed(key) {
		t.Error("attempts should be allowed after the window expires")
	}

	// A successful check clears the history.
	limiter.recordFailed(key)
	limiter.clear(key)
	if !limiter.allowed(key) {
		t.Error("clear should reset the limiter")
	}

	// Different keys are tracked independently.
	if !limiter.allowed(other) {
		t.Error("other keys should not be blocked")
	}
}

func TestAttemptLimiterBehaviour(t *testing.T) {
	t.Parallel()
	t.Run("int64", func(t *testing.T) { testAttemptLimiterBehaviour[int64](t, 1, 2) })
	t.Run("string", func(t *testing.T) {
		testAttemptLimiterBehaviour(t, "user@example.com", "other@example.com")
	})
}

func TestLoginAttemptLimiterPrunesKeys(t *testing.T) {
	t.Parallel()
	clk := &fakeClock{t: time.Unix(0, 0)}
	limiter := newAttemptLimiter[string](2, 50*time.Millisecond)
	limiter.now = clk.now
	email := "prune@example.com"

	limiter.recordFailed(email)
	limiter.recordFailed(email)
	if limiter.allowed(email) {
		t.Error("should be blocked after max attempts")
	}

	clk.advance(80 * time.Millisecond)
	if !limiter.allowed(email) {
		t.Error("should be allowed after window expires")
	}
	if len(limiter.attempts) != 0 {
		t.Errorf("stale map key not pruned: got %d entries, want 0", len(limiter.attempts))
	}
}

// testAttemptLimiterPrune verifies that prune() keeps entries still inside the
// window and removes them once they expire. Generic over the key type so the
// int64 and string limiters share one test body.
func testAttemptLimiterPrune[T comparable](t *testing.T, a, b T) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(0, 0)}
	limiter := newAttemptLimiter[T](5, 50*time.Millisecond)
	limiter.now = clk.now

	limiter.recordFailed(a)
	limiter.recordFailed(b)
	if len(limiter.attempts) != 2 {
		t.Fatalf("setup: got %d entries, want 2", len(limiter.attempts))
	}

	limiter.prune()
	if len(limiter.attempts) != 2 {
		t.Errorf("prune should not remove entries still in window: got %d, want 2", len(limiter.attempts))
	}

	clk.advance(80 * time.Millisecond)
	limiter.prune()
	if len(limiter.attempts) != 0 {
		t.Errorf("prune should remove expired entries: got %d, want 0", len(limiter.attempts))
	}
}

func TestAttemptLimiterPrune(t *testing.T) {
	t.Parallel()
	t.Run("int64", func(t *testing.T) { testAttemptLimiterPrune[int64](t, 1, 2) })
	t.Run("string", func(t *testing.T) {
		testAttemptLimiterPrune(t, "a@example.com", "b@example.com")
	})
}

func TestRoutes(t *testing.T) {
	t.Parallel()
	h := (&Server{}).routes()
	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/nope", http.StatusNotFound},                               // unmatched page → styled 404
		{"POST", "/users/anyone", http.StatusNotFound},                      // wrong method on a page route → friendly 404
		{"GET", routes.APILogIn.URL(), http.StatusMethodNotAllowed},         // session is POST/DELETE-only → 405
		{"POST", routes.API42Sync.URL(), http.StatusMethodNotAllowed},       // sync is GET-only → 405
		{"DELETE", routes.API42Sync.URL(), http.StatusMethodNotAllowed},     // sync is GET-only → 405
		{"GET", routes.APIAccountCreate.URL(), http.StatusMethodNotAllowed}, // account is POST/DELETE-only → 405
		{"GET", routes.APIPrefix() + "/nope", http.StatusNotFound},          // unknown API path → plain 404
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}

	// The preserved 405 should still advertise the allowed methods.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", routes.APILogIn.URL(), nil))
	allow := rec.Header().Get("Allow")
	if !strings.Contains(allow, "POST") || !strings.Contains(allow, "DELETE") {
		t.Errorf("GET %s Allow = %q, want POST and DELETE", routes.APILogIn.URL(), allow)
	}
}

func TestLogoutRedirectsWithSeeOther(t *testing.T) {
	t.Parallel()
	h := (&Server{}).routes()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, routes.APILogOut.URL(), nil))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("DELETE %s = %d, want %d", routes.APILogOut.URL(), rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != routes.PageHome {
		t.Errorf("DELETE %s Location = %q, want %q", routes.APILogOut.URL(), got, routes.PageHome)
	}
}

func TestSyncingPageHidesErrorActions(t *testing.T) {
	t.Parallel()
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleSyncing(rec, httptest.NewRequest(http.MethodGet, routes.PageSyncing, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="sync-error-actions" class="spaced-apart hidden"`) {
		t.Errorf("expected #sync-error-actions to start hidden, got:\n%s", body)
	}
}

func TestParseVisibilityFormURLEncoded(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPatch, routes.APIAccountVisibility.URL(), strings.NewReader("is_public=on&section_projects_users=on"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	isPublic, sections, err := parseVisibilityForm(req)
	if err != nil {
		t.Fatalf("parseVisibilityForm returned error: %v", err)
	}
	if !isPublic {
		t.Errorf("isPublic = %v, want true", isPublic)
	}
	if got, ok := sections["projects_users"]; !ok || !got {
		t.Errorf("projects_users toggle = %v (ok=%v), want true/ok", got, ok)
	}
	if got, ok := sections["achievements"]; !ok || got {
		t.Errorf("achievements toggle = %v (ok=%v), want false/ok", got, ok)
	}
	if got, ok := sections["skills"]; !ok || got {
		t.Errorf("skills toggle = %v (ok=%v), want false/ok", got, ok)
	}
	if len(sections) != len(view.ToggleableSections) {
		t.Errorf("toggle count = %d, want %d", len(sections), len(view.ToggleableSections))
	}
}

func TestParseVisibilityFormMultipart(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("section_locations", "on"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, routes.APIAccountVisibility.URL(), &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	isPublic, sections, err := parseVisibilityForm(req)
	if err != nil {
		t.Fatalf("parseVisibilityForm returned error: %v", err)
	}
	if isPublic {
		t.Errorf("isPublic = %v, want false", isPublic)
	}
	if got, ok := sections["locations"]; !ok || !got {
		t.Errorf("locations toggle = %v (ok=%v), want true/ok", got, ok)
	}
	if got, ok := sections["projects_users"]; !ok || got {
		t.Errorf("projects_users toggle = %v (ok=%v), want false/ok", got, ok)
	}
	if len(sections) != len(view.ToggleableSections) {
		t.Errorf("toggle count = %d, want %d", len(sections), len(view.ToggleableSections))
	}
}

func TestParseVisibilityFormRestoreDefaults(t *testing.T) {
	t.Parallel()
	// Simulate the restore defaults form: hidden inputs for every section that
	// is public by default, no is_public field.
	body := "section_projects_users=on" +
		"&section_scale_teams_as_corrected=on" +
		"&section_scale_teams_as_corrector=on" +
		"&section_quests_users=on" +
		"&section_titles_users=on" +
		"&section_achievements=on"
	req := httptest.NewRequest(http.MethodPatch, routes.APIAccountVisibility.URL(), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	isPublic, sections, err := parseVisibilityForm(req)
	if err != nil {
		t.Fatalf("parseVisibilityForm returned error: %v", err)
	}
	if isPublic {
		t.Errorf("isPublic = %v, want false", isPublic)
	}
	// Every default-public section should be true.
	for _, key := range []string{
		"projects_users", "scale_teams_as_corrected", "scale_teams_as_corrector",
		"quests_users", "titles_users", "achievements",
	} {
		if got, ok := sections[key]; !ok || !got {
			t.Errorf("default-public section %s = %v (ok=%v), want true/ok", key, got, ok)
		}
	}
	// Every default-private section should be false.
	for _, key := range []string{
		"coalitions", "locations", "skills", "contact", "points",
		"correction_point_historics", "events_users",
	} {
		if got, ok := sections[key]; !ok || got {
			t.Errorf("default-private section %s = %v (ok=%v), want false/ok", key, got, ok)
		}
	}
	if len(sections) != len(view.ToggleableSections) {
		t.Errorf("toggle count = %d, want %d", len(sections), len(view.ToggleableSections))
	}
}

func TestProfileHidesResyncDuringCooldown(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	ctx := context.Background()
	unique := time.Now().UnixNano()
	email := fmt.Sprintf("user-%d@e.st", unique)
	login := fmt.Sprintf("tester%d", unique)
	ftID := unique

	data := map[string]json.RawMessage{
		"me": json.RawMessage(`{"login":"` + login + `"}`),
	}
	id, err := st.CreateAccount(ctx, email, ftID, login, data)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteAccount(ctx, id) })
	markVerified(t, st, id) // owner pages are gated until the email is verified

	sid := randomToken()
	if err := st.CreateSession(ctx, sid, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSession(ctx, sid) })

	s := &Server{store: st}
	h := s.routes()
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, routes.PageProfile(login), nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
		return r
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req())
	if rec.Code != http.StatusOK {
		t.Fatalf("profile before cooldown: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Re-sync") {
		t.Errorf("profile before cooldown should contain Re-sync link")
	}

	if _, allowed, _, err := st.ReserveSync(ctx, ftID, syncCooldown); err != nil {
		t.Fatalf("reserve cooldown: %v", err)
	} else if !allowed {
		t.Fatal("expected cooldown slot to be free for a fresh account")
	}
	t.Cleanup(func() { _ = st.ReleaseSync(ctx, ftID) })

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req())
	if rec.Code != http.StatusOK {
		t.Fatalf("profile during cooldown: status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Re-sync") {
		t.Errorf("profile during cooldown should not contain Re-sync link")
	}
}

// postMultipart builds a multipart/form-data request, matching how the browser
// submits data-method forms via fetch(new FormData(form)) — as opposed to
// postForm's urlencoded body. Handlers that only ParseForm (not ParseMultipartForm)
// see empty fields for these, so the distinction matters in tests.
func postMultipart(method, route string, cookie *http.Cookie, fields map[string]string) (*httptest.ResponseRecorder, *http.Request) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = writer.WriteField(k, v)
	}
	_ = writer.Close()
	r := httptest.NewRequest(method, route, &buf)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return httptest.NewRecorder(), r
}
