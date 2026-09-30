package web

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/config"
)

func TestSecretExpiryAlert(t *testing.T) {
	expiry := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		next    string
		now     time.Time
		wantKey string // "" = no alert
	}{
		{"outside lead window", "", expiry.AddDate(0, 0, -30), ""},
		{"within lead window", "", expiry.AddDate(0, 0, -3), "secret-expiry-reminder"},
		{"on expiry day", "", expiry.Add(12 * time.Hour), "secret-expiry-reminder"},
		{"day after expiry", "", expiry.AddDate(0, 0, 1), "secret-broken"},
		{"expired without next", "", expiry.AddDate(0, 0, 3), "secret-broken"},
		{"next already staged", "staged", expiry.AddDate(0, 0, -3), ""},
		{"staged next not promoted", "staged", expiry.AddDate(0, 0, 3), "secret-promote-reminder"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, ok := secretExpiryAlert(config.Config{ClientSecretExpiry: expiry, ClientSecretNext: tt.next}, tt.now)
			if ok != (tt.wantKey != "") || a.key != tt.wantKey {
				t.Errorf("secretExpiryAlert() = %q, %v; want %q", a.key, ok, tt.wantKey)
			}
		})
	}
}

func TestSecretExpiryAlertUnsetExpiry(t *testing.T) {
	if a, ok := secretExpiryAlert(config.Config{}, time.Now()); ok {
		t.Errorf("secretExpiryAlert() = %q, true; want no alert when expiry is unset", a.key)
	}
}

func newAlertsTestServer(sender *fakeSender) *Server {
	return &Server{
		cfg:              config.Config{MaintainerEmail: "maintainer@example.com"},
		email:            sender,
		maintainerAlerts: newAttemptLimiter[string](1, maintainerAlertDedupWindow),
	}
}

func TestSendMaintainerAlertDedupes(t *testing.T) {
	fs := &fakeSender{}
	s := newAlertsTestServer(fs)
	alert := secretRejectedAlert(errors.New("both client secrets rejected"))

	s.sendMaintainerAlert(alert)
	s.sendMaintainerAlert(alert)

	if fs.sent != 1 {
		t.Errorf("emails sent = %d, want 1 (second call should be deduped)", fs.sent)
	}
}

func TestSendMaintainerAlertConcurrentBurstSendsOnce(t *testing.T) {
	fs := &fakeSender{}
	s := newAlertsTestServer(fs)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { s.sendMaintainerAlert(secretRejectedAlert(errors.New("invalid_client"))) })
	}
	wg.Wait()

	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.sent != 1 {
		t.Errorf("emails sent = %d, want 1", fs.sent)
	}
}

func TestSendMaintainerAlertSkipsEmailWithoutInbox(t *testing.T) {
	fs := &fakeSender{}
	s := newAlertsTestServer(fs)
	s.cfg.MaintainerEmail = ""

	s.sendMaintainerAlert(secretRejectedAlert(errors.New("invalid_client")))

	if fs.sent != 0 {
		t.Errorf("emails sent = %d, want 0 with no maintainer inbox", fs.sent)
	}
}
