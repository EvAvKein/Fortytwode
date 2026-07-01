package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/store"
)

// Cookie names and the session lifetime.
const (
	sessionCookie = "sid"
	jobCookie     = "job"
	stateCookie   = "ftstate"
	intentCookie  = "ftintent"
	sessionTTL    = 30 * 24 * time.Hour
)

// Per-email magic-link send rate limit (bounds how often a login link can be
// mailed to one address, whether or not an account exists for it).
const (
	maxLoginAttempts   = 2
	loginAttemptWindow = 30 * time.Minute
)

// Magic-link login and email-change confirmation link lifetimes. Login links are
// short-lived; the email-change link matches the verification TTL.
const (
	loginTokenTTL       = 1 * time.Hour
	emailChangeTokenTTL = 24 * time.Hour
)

// Per-account email-change request cap (bounds how often a session can spray
// confirmation mail at arbitrary new addresses).
const (
	maxEmailChangeRequests   = 2
	emailChangeRequestWindow = 30 * time.Minute
)

// Email-verification link lifetime and the per-account resend rate limit.
const (
	verifyTokenTTL       = 24 * time.Hour
	unverifiedAccountTTL = 7 * 24 * time.Hour // grace before a never-verified account is reaped
	maxVerifyResends     = 3
	verifyResendWindow   = 15 * time.Minute
)

// Account-deletion confirmation link lifetime and the per-account request rate
// limit (bounds how often a session can spray deletion mail at the address).
const (
	deleteTokenTTL      = 24 * time.Hour
	maxDeleteRequests   = 3
	deleteRequestWindow = 15 * time.Minute
)

// randomToken returns 32 bytes of crypto-random hex, used for session ids, job
// ids, the OAuth state, and the email login/verification tokens.
func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b) // crypto/rand.Read never returns an error (Go 1.24+)
	return hex.EncodeToString(b)
}

// tokenHash returns the sha256 hex of a token. Verification tokens are stored
// hashed (the plaintext only ever lives in the emailed link), so a database leak
// can't be used to verify anyone. sha256 is fine here — the token is already
// high-entropy, so no slow KDF is needed.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// clientKey identifies the client for the per-client sync cap: the session id
// when logged in, else the real client IP. sessionValid must report whether the
// session cookie resolved to a live session (the caller has already looked it
// up) — an unvalidated cookie would let a client mint a fresh key per request
// and bypass the cap. The IP comes from the X-Real-IP header our Nginx sets (the
// app always runs behind it), falling back to the connection's address for a
// direct/dev hit. Returns "" when the client can't be identified, so the cap
// fails open — Nginx's per-IP limit and the per-42-user cooldown still apply.
func (s *Server) clientKey(r *http.Request, sessionValid bool) string {
	if c, err := r.Cookie(sessionCookie); sessionValid && err == nil && c.Value != "" {
		return "sid:" + c.Value
	}
	ip := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr) // drop the port; "" if unparseable
	}
	if ip == "" {
		return ""
	}
	return "ip:" + ip
}

// currentAccount resolves the logged-in account from the session cookie, if any.
func (s *Server) currentAccount(r *http.Request) (store.Account, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return store.Account{}, false
	}
	acc, err := s.store.SessionAccount(r.Context(), c.Value)
	if err != nil {
		return store.Account{}, false
	}
	return acc, true
}

// currentSessionID returns the active session id from the cookie, if present.
func (s *Server) currentSessionID(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

// startSession creates a session row and sets the session cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, accountID int64) error {
	_, err := s.startSessionWithID(w, r, accountID)
	return err
}

// startSessionWithID creates a session row, sets the cookie, and returns the
// new session id. Callers that need the id (e.g. session rotation) use this.
func (s *Server) startSessionWithID(w http.ResponseWriter, r *http.Request, accountID int64) (string, error) {
	id := randomToken()
	if err := s.store.CreateSession(r.Context(), id, accountID, time.Now().Add(sessionTTL)); err != nil {
		return "", err
	}
	s.setCookie(w, sessionCookie, id, sessionTTL)
	return id, nil
}

// rotateSession creates a new session for the account, sets the new cookie, and
// deletes the old session row. It returns the new session id.
func (s *Server) rotateSession(w http.ResponseWriter, r *http.Request, accountID int64, oldSessionID string) (string, error) {
	newID, err := s.startSessionWithID(w, r, accountID)
	if err != nil {
		return "", err
	}
	_ = s.store.DeleteSession(r.Context(), oldSessionID)
	return newID, nil
}

// attemptLimiter tracks recent failed attempts keyed by an arbitrary comparable
// value (account ID, email, etc.). It is intentionally in-memory and
// non-persistent: a restart clears it. Periodic calls to prune() free memory
// from keys that are no longer actively failing.
type attemptLimiter[T comparable] struct {
	mu       sync.Mutex
	attempts map[T][]time.Time
	max      int
	window   time.Duration
	now      func() time.Time // overridable for deterministic tests; defaults to time.Now
}

func newAttemptLimiter[T comparable](max int, window time.Duration) *attemptLimiter[T] {
	return &attemptLimiter[T]{
		attempts: make(map[T][]time.Time),
		max:      max,
		window:   window,
		now:      time.Now,
	}
}

func (a *attemptLimiter[T]) allowed(key T) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.countRecentLocked(key) < a.max
}

func (a *attemptLimiter[T]) recordFailed(key T) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attempts[key] = append(a.attempts[key], a.now())
}

func (a *attemptLimiter[T]) clear(key T) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.attempts, key)
}

// prune removes all entries whose timestamps have fully expired. It is safe to
// call from a background goroutine; it does not interact with the rate-limit
// check path.
func (a *attemptLimiter[T]) prune() {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := a.now().Add(-a.window)
	for key, list := range a.attempts {
		kept := list[:0]
		for _, t := range list {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(a.attempts, key)
		} else {
			a.attempts[key] = kept
		}
	}
}

func (a *attemptLimiter[T]) countRecentLocked(key T) int {
	cutoff := a.now().Add(-a.window)
	list := a.attempts[key]
	kept := list[:0]
	for _, t := range list {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(a.attempts, key)
	} else {
		a.attempts[key] = kept
	}
	return len(kept)
}

// endSession deletes the session row (if any) and clears the cookie.
func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	s.clearCookie(w, sessionCookie)
}

// setCookie sets an HttpOnly, Lax cookie, marked Secure in production (s.secure,
// derived from an https redirect URI). SameSite=Lax still lets the cookie ride
// the top-level 42 OAuth redirect back to the callback.
func (s *Server) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	})
}

// clearCookie expires a cookie immediately.
func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: s.secure, MaxAge: -1})
}

// validEmail is a deliberately loose syntactic check: a non-empty local part, an
// "@", and a dotted domain. Actual deliverability is confirmed out-of-band by the
// email-verification flow (an account stays unverified until it clicks a link
// delivered to this address), so a strict format check here would add no real
// protection and only risk rejecting valid addresses.
func validEmail(email string) bool {
	at := strings.IndexByte(email, '@')
	return at > 0 && at < len(email)-1 && strings.Contains(email[at+1:], ".")
}
