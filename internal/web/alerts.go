package web

import (
	"context"
	"fmt"
	"os"
	"time"
)

// maintainerAlertDedupWindow bounds each maintainer alert to once per day, so a
// ticker firing every 30 minutes (or a burst of failed requests) doesn't spam
// the inbox.
const maintainerAlertDedupWindow = 24 * time.Hour

// maintainerAlert is one alert to Config.MaintainerEmail; key identifies it for dedup.
type maintainerAlert struct {
	key, subject, body string
}

// sendMaintainerAlert logs a to stderr and, when a maintainer inbox is
// configured, emails it there, at most once per maintainerAlertDedupWindow per
// key; a failed email is cleared so the next call retries it.
func (s *Server) sendMaintainerAlert(a maintainerAlert) {
	if !s.maintainerAlerts.take(a.key) {
		return
	}
	fmt.Fprintf(os.Stderr, "maintainer alert: %s\n%s\n", a.subject, a.body)
	if s.cfg.MaintainerEmail == "" {
		return
	}
	if err := s.email.SendMaintainerAlert(context.Background(), s.cfg.MaintainerEmail, a.subject, a.body); err != nil {
		fmt.Fprintf(os.Stderr, "warning: send %s alert: %v\n", a.key, err)
		s.maintainerAlerts.clear(a.key)
	}
}
