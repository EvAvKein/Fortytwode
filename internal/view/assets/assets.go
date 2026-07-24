// Package assets holds the static front-end files (stylesheet, scripts, favicon)
// served as their own cacheable resources rather than inlined into every HTML page.
//
// Each file is embedded into the binary (so it stays self-contained) and exposed
// at a content-fingerprinted URL: the path carries a short hash of the bytes, so
// editing a file changes its URL and busts the cache automatically. That lets the
// served files be cached immutably and indefinitely while the (no-store) HTML that
// references them always points at the current version. Templates read the *Href
// URLs; the web package serves the bytes via All().
package assets

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed style.css
var StyleCSS []byte

//go:embed app.js
var AppJS []byte

//go:embed syncing.js
var SyncingJS []byte

//go:embed favicon.ico
var FaviconICO []byte

// fingerprint returns a short hex digest of the bytes, used to version asset URLs.
func fingerprint(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// Fingerprinted public URLs for each asset, the single source of truth shared by
// the templates (which link them) and All (which serves them).
var (
	StyleHref   = "/static/style." + fingerprint(StyleCSS) + ".css"
	AppSrc      = "/static/app." + fingerprint(AppJS) + ".js"
	SyncingSrc  = "/static/syncing." + fingerprint(SyncingJS) + ".js"
	FaviconHref = "/static/favicon." + fingerprint(FaviconICO) + ".ico"
)

// Asset is one servable static file: its fingerprinted path, MIME type, and bytes.
type Asset struct {
	Path        string
	ContentType string
	Body        []byte
}

// All lists every static asset so the web layer can register one route each.
func All() []Asset {
	return []Asset{
		{StyleHref, "text/css; charset=utf-8", StyleCSS},
		{AppSrc, "application/javascript; charset=utf-8", AppJS},
		{SyncingSrc, "application/javascript; charset=utf-8", SyncingJS},
		{FaviconHref, "image/x-icon", FaviconICO},
	}
}
