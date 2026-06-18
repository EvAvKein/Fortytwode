package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/store"
	"golang.org/x/crypto/argon2"
)

// Cookie names and the session lifetime.
const (
	sessionCookie = "sid"
	jobCookie     = "job"
	stateCookie   = "ftstate"
	intentCookie  = "ftintent"
	sessionTTL    = 30 * 24 * time.Hour
)

// argon2id parameters (OWASP-recommended baseline). Encoded into each hash, so
// they can be tuned later without breaking existing hashes.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// dummyHash is verified against when a login's email matches no account, so the
// not-found path costs the same argon2 derivation as a real one — otherwise the
// response time would reveal which emails have accounts.
var dummyHash, _ = hashPassword("dummy-timing-equalizer")

// hashPassword returns a PHC-format argon2id string ($argon2id$v=..$m=..,t=..,p=..$salt$hash).
func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword reports whether password matches a PHC-format argon2id hash,
// re-deriving with the parameters embedded in the hash.
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// randomToken returns 32 bytes of crypto-random hex, used for session ids, job
// ids and the OAuth state.
func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b) // crypto/rand.Read never returns an error (Go 1.24+)
	return hex.EncodeToString(b)
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

// startSession creates a session row and sets the session cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, accountID int64) error {
	id := randomToken()
	if err := s.store.CreateSession(r.Context(), id, accountID, time.Now().Add(sessionTTL)); err != nil {
		return err
	}
	s.setCookie(w, sessionCookie, id, sessionTTL)
	return nil
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

// validEmail is a deliberately loose check: a non-empty local part, an "@", and a
// dotted domain. Real deliverability isn't verified (no email flow in v1).
func validEmail(email string) bool {
	at := strings.IndexByte(email, '@')
	return at > 0 && at < len(email)-1 && strings.Contains(email[at+1:], ".")
}
