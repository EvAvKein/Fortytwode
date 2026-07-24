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

// securityHeaders sets the CSP and non-TLS security headers on every response.
// It lives in the app (not just the prod Nginx config) so dev and the standalone
// binary are covered too, and so the policy stays next to the templates/assets
// whose shape it depends on. HSTS stays in the prod Nginx because it must only
// be sent over HTTPS.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// registerAssets wires one route per static asset (stylesheet, scripts, favicon),
// served from the embedded bytes. The URLs are content-fingerprinted, so the bytes can
// be cached forever — a change to a file changes its URL, not this response's headers.
func registerAssets(mux *http.ServeMux) {
	for _, a := range assets.All() {
		body, ctype := a.Body, a.ContentType
		mux.HandleFunc("GET "+a.Path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Write(body)
		})
	}

	// Crawlers and link-preview bots fetch /favicon.ico directly rather than reading
	// the HTML's <link rel="icon">, so serve the same bytes there too — otherwise those
	// requests fall through to the styled HTML 404. This path can't carry a fingerprint,
	// so it gets a bounded cache lifetime instead of the immutable one above.
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		w.Write(assets.FaviconICO)
	})
}
