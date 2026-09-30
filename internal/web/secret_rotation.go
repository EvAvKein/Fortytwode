package web

import (
	"math"
	"strconv"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/config"
)

// secretExpiryLeadWindow is how close to Config.ClientSecretExpiry the rotation
// reminder starts firing — matches how far ahead 42 generates its "Next Secret"
// on the dashboard, so the reminder lines up with when there's actually
// something to copy.
const secretExpiryLeadWindow = 7 * 24 * time.Hour

const (
	addNextSecretSteps = "Steps:\n" +
		"- Open https://profile.intra.42.fr/oauth/applications\n" +
		"- Set FT_CLIENT_SECRET_NEXT to the app's \"Next Secret\"\n" +
		"- Set FT_CLIENT_SECRET_EXPIRES to the \"Secret\"'s \"Valid until\" date\n" +
		"- Redeploy: make update-prod"
	switchToNextSecretSteps = "Steps:\n" +
		"- Move FT_CLIENT_SECRET_NEXT's value into FT_CLIENT_SECRET\n" +
		"- Clear FT_CLIENT_SECRET_NEXT\n" +
		"- Set FT_CLIENT_SECRET_EXPIRES to the new secret's \"Valid until\" date (https://profile.intra.42.fr/oauth/applications)\n" +
		"- Redeploy: make update-prod"
)

// secretExpiryAlert returns the 42 app-secret rotation reminder due at now, if
// any: from secretExpiryLeadWindow before cfg.ClientSecretExpiry onward while
// ClientSecretNext is unset, and after that expiry while a staged
// ClientSecretNext still hasn't been promoted (otherwise the next cycle's
// reminder could never fire). No reminder when the expiry isn't configured.
func secretExpiryAlert(cfg config.Config, now time.Time) (maintainerAlert, bool) {
	if cfg.ClientSecretExpiry.IsZero() {
		return maintainerAlert{}, false
	}
	// "Valid until" is a date; treat the secret as valid through that whole day.
	left := cfg.ClientSecretExpiry.AddDate(0, 0, 1).Sub(now)
	expiry := cfg.ClientSecretExpiry.Format(time.DateOnly)

	switch {
	case cfg.ClientSecretNext != "" && left <= 0:
		return maintainerAlert{
			key:     "secret-promote-reminder",
			subject: "Fortytwode: Switch to the next 42 app secret",
			body: "FT_CLIENT_SECRET expired on " + expiry + ". Sign-in now uses FT_CLIENT_SECRET_NEXT. " +
				"Future expiry reminders won't fire until you switch.\n\n" + switchToNextSecretSteps,
		}, true
	case cfg.ClientSecretNext == "" && left <= 0:
		// Keeps firing past expiry: with no sign-ins, the rejected-secret alert never would.
		return secretBrokenAlert("FT_CLIENT_SECRET expired on " + expiry + "."), true
	case cfg.ClientSecretNext == "" && left <= secretExpiryLeadWindow:
		days := strconv.Itoa(int(math.Ceil(left.Hours() / 24)))
		return maintainerAlert{
			key:     "secret-expiry-reminder",
			subject: "Fortytwode: 42 app secret needs replacing",
			body:    "FT_CLIENT_SECRET expires on " + expiry + " (" + days + " day(s) left).\n\n" + addNextSecretSteps,
		}, true
	}
	return maintainerAlert{}, false
}

// secretBrokenAlert reports that 42 sign-in and sync are down for lack of a
// valid app secret. Expiry and live rejection share its key, so one outage
// yields one alert per window whichever path notices it first.
func secretBrokenAlert(cause string) maintainerAlert {
	return maintainerAlert{
		key:     "secret-broken",
		subject: "Fortytwode: 42 sign-in is broken (app secret)",
		body:    cause + " 42 sign-in and sync are broken.\n\n" + addNextSecretSteps,
	}
}

func secretRejectedAlert(err error) maintainerAlert {
	return secretBrokenAlert("42 rejected the app secret (" + err.Error() + ").")
}

func (s *Server) checkSecretExpiry() {
	if a, ok := secretExpiryAlert(s.cfg, time.Now()); ok {
		s.sendMaintainerAlert(a)
	}
}
