package routes

// Page path constants are absolute routes for server-rendered pages.
const (
	PageHome     = "/"
	PageHealth   = "/healthcheck"
	PageSyncing  = "/syncing"
	PageSignup   = "/signup"
	PageLogin    = "/login"
	PageSettings = "/settings"
	PagePrivacy  = "/privacy"
)

// PageProfile returns the canonical profile page path for a 42 login.
func PageProfile(login string) string { return "/users/" + login }
