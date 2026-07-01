package routes

// Page path constants are absolute routes for server-rendered pages.
const (
	PageHome          = "/"
	PageHealth        = "/healthcheck"
	PageSyncing       = "/syncing"
	PageSignup        = "/signup"
	PageLogin         = "/login"
	PageSettings      = "/settings"
	PagePrivacy       = "/privacy"
	PageVerifyPending = "/verify-pending"
	PageVerifyEmail   = "/verify-email"
	PageConfirmDelete = "/confirm-delete"
	// PageLoginCallback is the target of the magic-link login email; it consumes the
	// ?token= and starts a session. PageConfirmEmail is the target of the email-change
	// confirmation link, which promotes the pending address to the account's email.
	PageLoginCallback = "/login/callback"
	PageConfirmEmail  = "/confirm-email"
)

// PageProfile returns the canonical profile page path for a 42 login.
func PageProfile(login string) string { return "/users/" + login }
