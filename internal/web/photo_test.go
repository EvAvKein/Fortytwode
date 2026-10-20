package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/api42"
	"github.com/EvAvKein/Fortytwode/internal/routes"
	"github.com/EvAvKein/Fortytwode/internal/storetest"
)

func TestPhotoVisibilityAndVariants(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	ctx := context.Background()
	unique := time.Now().UnixNano()
	login := fmt.Sprintf("tester%d", unique)
	id, err := st.CreateAccount(ctx, fmt.Sprintf("user-%d@e.st", unique), unique, login,
		map[string]json.RawMessage{"me": json.RawMessage(`{"login":"` + login + `"}`)})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteAccount(ctx, id) })

	h := (&Server{store: st}).routes()
	get := func(path, sid string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if sid != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	photo := routes.PagePhoto(login)

	if rec := get(photo, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("no photo stored: status = %d, want 404", rec.Code)
	}
	original := &api42.Photo{ContentType: "image/jpeg", Bytes: []byte("original")}
	small := &api42.Photo{ContentType: "image/png", Bytes: []byte("small")}
	if err := st.SavePhotos(ctx, id, original, small); err != nil {
		t.Fatalf("save photos: %v", err)
	}

	if rec := get(photo, ""); rec.Code != http.StatusNotFound {
		t.Errorf("private profile, anonymous: status = %d, want 404", rec.Code)
	}

	markVerified(t, st, id)
	sid := randomToken()
	if err := st.CreateSession(ctx, sid, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSession(ctx, sid) })
	rec := get(photo, sid)
	if rec.Code != http.StatusOK || rec.Body.String() != "original" || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("logged in: got %d %q (%s), want 200 original image/jpeg", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}

	if err := st.UpdateVisibility(ctx, id, true, nil); err != nil {
		t.Fatalf("make public: %v", err)
	}
	if rec := get(photo+"?size=small", ""); rec.Code != http.StatusOK || rec.Body.String() != "small" {
		t.Errorf("public profile, small: got %d %q, want 200 small", rec.Code, rec.Body.String())
	}

	// A re-sync whose small download failed keeps the previous small copy.
	if err := st.SavePhotos(ctx, id, &api42.Photo{ContentType: "image/jpeg", Bytes: []byte("newer")}, nil); err != nil {
		t.Fatalf("re-save photos: %v", err)
	}
	if rec := get(photo, ""); rec.Body.String() != "newer" {
		t.Errorf("original after re-save = %q, want newer", rec.Body.String())
	}
	if rec := get(photo+"?size=small", ""); rec.Body.String() != "small" {
		t.Errorf("small after re-save without one = %q, want small", rec.Body.String())
	}

	// The header shows the small copy, linked to the original.
	body := get(routes.PageProfile(login), "").Body.String()
	if !strings.Contains(body, `src="`+photo+"?size=small&amp;synced=") || !strings.Contains(body, `href="`+photo+"?synced=") {
		t.Errorf("profile header lacks small img linked to original:\n%s", body)
	}
}
