// Package email sends Fortytwode's transactional mail (the sign-up verification
// link and the account-deletion confirmation link) through Resend's HTTP API. It
// mirrors the internal/api42
// style — a small net/http client with a Bearer token — rather than pulling in an
// SDK, keeping the dependency set lean.
package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/config"
)

// resendEndpoint is Resend's send-email endpoint.
const resendEndpoint = "https://api.resend.com/emails"

// Sender sends Fortytwode's transactional emails. It is an interface so handlers
// can be tested with a fake recorder and so a missing API key degrades to a no-op.
type Sender interface {
	SendVerification(ctx context.Context, to, link string) error
	SendDeletionConfirmation(ctx context.Context, to, link string) error
}

// New returns a Sender from config. With no RESEND_API_KEY it returns a logging
// no-op sender (so local dev and tests don't need a real key — the link is printed
// to stderr); otherwise it returns a live Resend client.
func New(cfg config.Config) Sender {
	if cfg.ResendAPIKey == "" {
		fmt.Fprintln(os.Stderr, "warning: RESEND_API_KEY unset; verification emails will be logged, not sent")
		return logSender{}
	}
	return &resendSender{
		apiKey:  cfg.ResendAPIKey,
		from:    cfg.MailFrom,
		replyTo: cfg.MailReplyTo,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// logSender stands in when no API key is configured: it prints the link so a
// developer can complete verification locally, and never errors.
type logSender struct{}

func (logSender) SendVerification(_ context.Context, to, link string) error {
	fmt.Fprintf(os.Stderr, "email (not sent, no API key): verification for %s -> %s\n", to, link)
	return nil
}

func (logSender) SendDeletionConfirmation(_ context.Context, to, link string) error {
	fmt.Fprintf(os.Stderr, "email (not sent, no API key): deletion confirmation for %s -> %s\n", to, link)
	return nil
}

// resendSender posts to Resend's API.
type resendSender struct {
	apiKey  string
	from    string
	replyTo string
	http    *http.Client
}

func (s *resendSender) SendVerification(ctx context.Context, to, link string) error {
	return s.send(ctx, to, "Verify your Fortytwode email", verificationText(link), verificationHTML(link))
}

func (s *resendSender) SendDeletionConfirmation(ctx context.Context, to, link string) error {
	return s.send(ctx, to, "Confirm your Fortytwode account deletion", deletionText(link), deletionHTML(link))
}

// send posts one transactional email to Resend's API.
func (s *resendSender) send(ctx context.Context, to, subject, text, html string) error {
	payload := map[string]any{
		"from":    s.from,
		"to":      []string{to},
		"subject": subject,
		"text":    text,
		"html":    html,
	}
	if s.replyTo != "" {
		payload["reply_to"] = s.replyTo
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendEndpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("resend: unexpected status %d", res.StatusCode)
	}
	return nil
}

func verificationText(link string) string {
	return "Welcome to Fortytwode!\n\n" +
		"Confirm your email address by opening this link:\n" + link + "\n\n" +
		"The link is valid for 24 hours. If you didn't create an account, you can ignore this email."
}

func verificationHTML(link string) string {
	return `<p>Welcome to Fortytwode!</p>` +
		`<p>Confirm your email address by clicking the link below:</p>` +
		`<p><a href="` + link + `">Verify my email</a></p>` +
		`<p>The link is valid for 24 hours. If you didn't create an account, you can ignore this email.</p>`
}

func deletionText(link string) string {
	return "We received a request to delete your Fortytwode account.\n\n" +
		"Confirm the deletion by opening this link:\n" + link + "\n\n" +
		"The link is valid for 24 hours. If you didn't request this, you can ignore this email and nothing will be deleted."
}

func deletionHTML(link string) string {
	return `<p>We received a request to delete your Fortytwode account.</p>` +
		`<p>Confirm the deletion by clicking the link below:</p>` +
		`<p><a href="` + link + `">Confirm account deletion</a></p>` +
		`<p>The link is valid for 24 hours. If you didn't request this, you can ignore this email and nothing will be deleted.</p>`
}
