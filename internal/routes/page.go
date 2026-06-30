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
	PageVerifyPending = "/verify-pending"
	PageVerifyEmail   = "/verify-email"
	PageConfirmDelete = "/confirm-delete"
)

// PageProfile returns the canonical profile page path for a 42 login.
func PageProfile(login string) string { return "/users/" + login }
