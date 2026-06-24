package web

import (
	"net/http"

	"github.com/EvAvKein/Fortytwode/internal/view/assets"
)

// contentSecurityPolicy locks the page down to its own origin. Styles and scripts
// are now external same-origin files, so script-src/style-src can be 'self' with no
// inline allowance — the high-value protection against injected scripts. Exceptions:
//   - style-src-attr 'unsafe-inline' keeps the few dynamic inline style="" attributes
//     (skill-bar widths, coalition colour); they're server-computed and can't run code.
//   - img-src allows https: so 42's CDN avatars load without pinning a host 42 may change.
//   - connect-src 'self' permits the /api/<version>/sync/stream EventSource (same-origin SSE).
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; style-src-attr 'unsafe-inline'; " +
	"img-src 'self' https: data:; " +
	"connect-src 'self'; " +
	"form-action 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'"

// securityHeaders sets the CSP on every response. It lives in the app (not just the
// prod Nginx config) so dev and the standalone binary are covered too, and so the
// policy stays next to the templates/assets whose shape it depends on.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// registerAssets wires one route per static asset (stylesheet + scripts), served
// from the embedded bytes. The URLs are content-fingerprinted, so the bytes can be
// cached forever — a change to a file changes its URL, not this response's headers.
func registerAssets(mux *http.ServeMux) {
	for _, a := range assets.All() {
		body, ctype := a.Body, a.ContentType
		mux.HandleFunc("GET "+a.Path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Write(body)
		})
	}
}
