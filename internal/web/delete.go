package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/routes"
	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/EvAvKein/Fortytwode/internal/view/pages"
)

// deleteLink builds the absolute deletion-confirmation URL for a token. It points
// at a GET page (not the destructive endpoint) so a link prefetch only renders a
// confirmation page — the account is erased only by the button's POST.
func (s *Server) deleteLink(token string) string {
	return s.originBase() + routes.PageConfirmDelete + "?token=" + url.QueryEscape(token)
}

// issueDeletion mints a fresh deletion token, persists its hash and request time,
// and fires the confirmation email asynchronously. Mirrors issueVerification: a
// dropped send isn't fatal (the user can request again), so it is logged, not
// surfaced. Nothing is erased here — only the consumed link deletes the account.
func (s *Server) issueDeletion(ctx context.Context, accountID int64, email, login string) {
	token := randomToken()
	if err := s.store.SetDeleteToken(ctx, accountID, tokenHash(token), time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: set delete token for account %d: %v\n", accountID, err)
		return
	}
	link := s.deleteLink(token)
	go func() {
		if err := s.email.SendDeletionConfirmation(context.Background(), email, login, link); err != nil {
			fmt.Fprintf(os.Stderr, "warning: send deletion email for account %d: %v\n", accountID, err)
		}
	}()
}

// handleRequestDelete starts the deletion flow for the logged-in account: instead
// of erasing anything, it emails a confirmation link and shows a "check your inbox"
// page. The account is only deleted once that link is followed and confirmed. The
// per-account request cap bounds how often a session can spray deletion mail.
func (s *Server) handleRequestDelete(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	acc, ok := s.currentAccount(r)
	if !ok {
		renderStatus(w, r, http.StatusUnauthorized, pages.LoginForm("Please log in to continue", ""))
		return
	}
	if s.gateUnverified(w, r, acc) {
		return
	}
	if !s.deleteRequests.allowed(acc.ID) {
		renderStatus(w, r, http.StatusTooManyRequests,
			pages.DeleteRequested(acc.Email, "Too many requests — wait a little before trying again.", acc.FtLogin))
		return
	}
	s.deleteRequests.recordFailed(acc.ID)
	s.issueDeletion(r.Context(), acc.ID, acc.Email, acc.FtLogin)
	http.Redirect(w, r, routes.PageSettings+"?deletion=requested", http.StatusSeeOther)
}

// handleDeletePending renders the confirmation page reached from the deletion
// email. It validates the token read-only (so a bad link shows the failure page
// and a prefetch erases nothing); the page's button POSTs to handleConfirmDelete.
func (s *Server) handleDeletePending(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		renderStatus(w, r, http.StatusBadRequest, pages.DeleteFailed(s.viewerLogin(r)))
		return
	}
	if _, err := s.store.PeekDeleteToken(r.Context(), tokenHash(token), deleteTokenTTL); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			renderStatus(w, r, http.StatusBadRequest, pages.DeleteFailed(s.viewerLogin(r)))
			return
		}
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	render(w, r, pages.DeleteConfirm(token, s.viewerLogin(r)))
}

// handleConfirmDelete consumes the deletion token and erases the account (sessions
// cascade via the foreign key — full erasure, GDPR Art. 17). An invalid/expired or
// already-consumed token renders the failure page. If this browser held the
// deleted account's session, it is ended so the stale cookie is cleared.
func (s *Server) handleConfirmDelete(w http.ResponseWriter, r *http.Request) {
	if s.rejectCrossSite(w, r) {
		return
	}
	token := r.FormValue("token")
	if token == "" {
		renderStatus(w, r, http.StatusBadRequest, pages.DeleteFailed(s.viewerLogin(r)))
		return
	}
	// Resolve the session before the delete cascades it away, so we can tell
	// whether this browser's cookie now points at a gone account.
	cur, loggedIn := s.currentAccount(r)
	acc, err := s.store.DeleteByToken(r.Context(), tokenHash(token), deleteTokenTTL)
	if errors.Is(err, store.ErrNotFound) {
		renderStatus(w, r, http.StatusBadRequest, pages.DeleteFailed(s.viewerLogin(r)))
		return
	}
	if err != nil {
		http.Error(w, "Could not delete account", http.StatusInternalServerError)
		return
	}
	if loggedIn && cur.ID == acc.ID {
		s.endSession(w, r)
	}
	http.Redirect(w, r, routes.PageHome, http.StatusSeeOther)
}
