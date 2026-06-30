package routes

import (
	"strings"

	"github.com/EvAvKein/Fortytwode/internal/config"
)

// Route pairs an HTTP method with a path. It is the canonical definition of an
// API endpoint: both the router and the templates derive their strings from it.
type Route struct {
	Method string
	Path   string
}

// Pattern returns the method+path form used by http.ServeMux, e.g. "GET /me".
func (route Route) Pattern() string { return route.Method + " " + route.Path }

// URL returns the full URL under the current API prefix, e.g. "/api/v1/me".
func (route Route) URL() string { return APIPrefix() + route.Path }

// MethodLower returns the HTTP method in lowercase, suitable for HTML form
// method/data-method attributes.
func (route Route) MethodLower() string { return strings.ToLower(route.Method) }

// APIPrefix returns the mount prefix for the versioned API.
func APIPrefix() string { return config.AppAPIPrefix() }

// API routes. NOTE: because Nginx, Docker Compose, .env.example, and README.md
// cannot import Go packages, changing any of these values also requires updating
// the exact paths in those files. The OAuth redirect URI registered on the 42
// app must match APIAuth42Callback as well.
var (
	APIAuth42Login          = Route{Method: "GET", Path: "/auth/42/login"}
	APIAuth42Callback       = Route{Method: "GET", Path: "/auth/42/callback"}
	API42Sync               = Route{Method: "GET", Path: "/sync"}
	APISyncStream           = Route{Method: "GET", Path: "/sync/stream"}
	APISyncDownloadRaw      = Route{Method: "GET", Path: "/sync/download/raw"}
	APISyncDownloadCurated  = Route{Method: "GET", Path: "/sync/download/curated"}
	APILogInSync            = Route{Method: "POST", Path: "/session/sync"}
	APILogIn                = Route{Method: "POST", Path: "/session"}
	APILogOut               = Route{Method: "DELETE", Path: "/session"}
	APIAccountCreate        = Route{Method: "POST", Path: "/account"}
	APIAccountDownload      = Route{Method: "GET", Path: "/account/download"}
	APIAccountVisibility    = Route{Method: "PATCH", Path: "/account/visibility"}
	APIAccountEmail         = Route{Method: "PATCH", Path: "/account/email"}
	APIAccountPassword      = Route{Method: "PATCH", Path: "/account/password"}
	APIAccountDelete        = Route{Method: "DELETE", Path: "/account"}
	APIAccountDeleteConfirm = Route{Method: "POST", Path: "/account/delete/confirm"}
	APIVerifyResend         = Route{Method: "POST", Path: "/account/verify/resend"}
	APIVerifyEmailEdit      = Route{Method: "PATCH", Path: "/account/verify/email"}
)
