// Package store persists app accounts (each owning one 42 snapshot in a JSONB
// column) and login sessions in Postgres. The snapshot is a cache over the 42
// API, read whole and rendered whole, so there is no relational model beyond the
// account row and its sessions.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/snapshot"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup matches no row. ErrDuplicate is returned
// when an insert violates a unique constraint (email or ft_id already taken).
var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("already exists")
)

// Store owns a connection pool to the database.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to dsn and applies any pending migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.Migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return s, nil
}

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

// Ping verifies the database is reachable (used by the /healthcheck endpoint).
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Account is an app account plus its 42 snapshot and visibility settings. The
// password hash is never carried here; it is returned separately by AccountByEmail.
type Account struct {
	ID         int64
	Email      string
	FtID       int64
	FtLogin    string
	Data       map[string]json.RawMessage
	FetchedAt  time.Time
	IsPublic   bool            // profile viewable without an account
	Visibility map[string]bool // per-section public overrides (missing -> code default)
}

// The column list shared by the account lookups, plain and table-aliased (for
// the sessions join).
const (
	accountCols  = "id, email, ft_id, ft_login, data, fetched_at, is_public, visibility"
	accountColsA = "a.id, a.email, a.ft_id, a.ft_login, a.data, a.fetched_at, a.is_public, a.visibility"
)

// scanAccount reads the accountCols (in order) from a row.
func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	var data, vis []byte
	if err := row.Scan(&a.ID, &a.Email, &a.FtID, &a.FtLogin, &data, &a.FetchedAt, &a.IsPublic, &vis); err != nil {
		return Account{}, err
	}
	if err := json.Unmarshal(data, &a.Data); err != nil {
		return Account{}, err
	}
	if len(vis) > 0 {
		if err := json.Unmarshal(vis, &a.Visibility); err != nil {
			return Account{}, err
		}
	}
	return a, nil
}

// CreateAccount inserts a new account with the given snapshot and returns its id.
// The snapshot is curated (snapshot.Curate) before storage, so the row never holds
// raw third-party data. A unique-constraint violation (email or ft_id taken) is
// reported as ErrDuplicate.
func (s *Store) CreateAccount(ctx context.Context, email, passwordHash string, ftID int64, ftLogin string, data map[string]json.RawMessage) (int64, error) {
	blob, err := json.Marshal(snapshot.Curate(data))
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.pool.QueryRow(ctx, `
		INSERT INTO accounts (email, password_hash, ft_id, ft_login, data, fetched_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, now())
		RETURNING id`,
		email, passwordHash, ftID, ftLogin, string(blob)).Scan(&id)
	if isUniqueViolation(err) {
		return 0, ErrDuplicate
	}
	return id, err
}

// AccountByEmail returns the account and its password hash for login.
func (s *Store) AccountByEmail(ctx context.Context, email string) (Account, string, error) {
	var a Account
	var data, vis []byte
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT `+accountCols+`, password_hash FROM accounts WHERE email = $1`, email).
		Scan(&a.ID, &a.Email, &a.FtID, &a.FtLogin, &data, &a.FetchedAt, &a.IsPublic, &vis, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, "", ErrNotFound
	}
	if err != nil {
		return Account{}, "", err
	}
	if err := json.Unmarshal(data, &a.Data); err != nil {
		return Account{}, "", err
	}
	if len(vis) > 0 {
		if err := json.Unmarshal(vis, &a.Visibility); err != nil {
			return Account{}, "", err
		}
	}
	return a, hash, nil
}

// AccountByLogin returns the account for a 42 login (the public profile key).
func (s *Store) AccountByLogin(ctx context.Context, login string) (Account, error) {
	return s.accountWhere(ctx, "ft_login = $1", login)
}

// AccountByFtID returns the account bound to a 42 user id, if any.
func (s *Store) AccountByFtID(ctx context.Context, ftID int64) (Account, error) {
	return s.accountWhere(ctx, "ft_id = $1", ftID)
}

func (s *Store) accountWhere(ctx context.Context, cond string, arg any) (Account, error) {
	a, err := scanAccount(s.pool.QueryRow(ctx, `SELECT `+accountCols+` FROM accounts WHERE `+cond, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// UpdateSnapshot curates then merges a re-sync's resources into the account's
// snapshot (a resource absent from this run keeps its previous value) and bumps
// fetched_at. Curate is presence-driven, so the merge preserves untouched resources.
func (s *Store) UpdateSnapshot(ctx context.Context, accountID int64, data map[string]json.RawMessage) error {
	blob, err := json.Marshal(snapshot.Curate(data))
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE accounts SET data = data || $2::jsonb, fetched_at = now() WHERE id = $1`,
		accountID, string(blob))
	return err
}

// UpdateVisibility sets the public opt-in and the per-section overrides.
func (s *Store) UpdateVisibility(ctx context.Context, accountID int64, isPublic bool, sections map[string]bool) error {
	blob, err := json.Marshal(sections)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE accounts SET is_public = $2, visibility = $3::jsonb WHERE id = $1`,
		accountID, isPublic, string(blob))
	return err
}

// UpdateEmail changes the account's email address. A unique-constraint violation
// (the new email is already taken) is reported as ErrDuplicate.
func (s *Store) UpdateEmail(ctx context.Context, accountID int64, email string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE accounts SET email = $2 WHERE id = $1`,
		accountID, email)
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	return err
}

// UpdatePassword changes the account's password hash.
func (s *Store) UpdatePassword(ctx context.Context, accountID int64, passwordHash string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE accounts SET password_hash = $2 WHERE id = $1`,
		accountID, passwordHash)
	return err
}

// AccountPasswordHash returns the current password hash for an account.
func (s *Store) AccountPasswordHash(ctx context.Context, accountID int64) (string, error) {
	var hash string
	err := s.pool.QueryRow(ctx,
		`SELECT password_hash FROM accounts WHERE id = $1`, accountID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

// DeleteAccount erases an account and everything tied to it: the row holds the 42
// snapshot, and sessions cascade via the foreign key, so this is a full erasure
// (GDPR Art. 17). A missing id is not an error.
func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, id)
	return err
}

// CreateSession persists a session id for an account with the given expiry.
func (s *Store) CreateSession(ctx context.Context, id string, accountID int64, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (id, account_id, expires_at) VALUES ($1, $2, $3)`,
		id, accountID, expiresAt)
	return err
}

// SessionAccount returns the account for a non-expired session, or ErrNotFound.
func (s *Store) SessionAccount(ctx context.Context, sessionID string) (Account, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+accountColsA+`
		FROM sessions s JOIN accounts a ON a.id = s.account_id
		WHERE s.id = $1 AND s.expires_at > now()`, sessionID)
	a, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// PurgeExpiredSessions deletes sessions past their expiry. Lookups already
// filter on expires_at, so this is pure hygiene/data-minimisation — without it
// expired rows would accumulate forever. Returns the number removed.
func (s *Store) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteSession removes a session (logout). A missing id is not an error.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID)
	return err
}

// DeleteOtherSessions removes every session for an account except the one given.
// Used after sensitive account changes (email/password) to sign out other devices.
func (s *Store) DeleteOtherSessions(ctx context.Context, accountID int64, exceptSessionID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM sessions WHERE account_id = $1 AND id <> $2`,
		accountID, exceptSessionID)
	return err
}

// ReserveSync atomically claims a sync slot for a 42 user, enforcing one fetch
// per cooldown. It returns (0, true) when the slot is free (recording now() as
// the user's last sync), or (retryAfter, false) when the user synced within the
// cooldown — retryAfter being how long until they may sync again. The check and
// the timestamp write happen in one statement so two concurrent syncs for the
// same user can't both pass. Release the slot with ReleaseSync if the fetch fails.
func (s *Store) ReserveSync(ctx context.Context, ftID int64, cooldown time.Duration) (time.Duration, bool, error) {
	// The conditional ON CONFLICT updates a row only when the previous sync is
	// older than the cooldown, so RowsAffected reports whether the slot was free.
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO sync_cooldowns (ft_id, last_sync_at) VALUES ($1, now())
		ON CONFLICT (ft_id) DO UPDATE SET last_sync_at = now()
			WHERE sync_cooldowns.last_sync_at <= now() - make_interval(secs => $2)`,
		ftID, cooldown.Seconds())
	if err != nil {
		return 0, false, err
	}
	if tag.RowsAffected() == 1 {
		return 0, true, nil
	}
	// Blocked: read the existing timestamp to report the remaining cooldown.
	var last time.Time
	if err := s.pool.QueryRow(ctx, `SELECT last_sync_at FROM sync_cooldowns WHERE ft_id = $1`, ftID).Scan(&last); err != nil {
		return 0, false, err
	}
	return time.Until(last.Add(cooldown)), false, nil
}

// ReleaseSync clears a 42 user's cooldown slot. Called when a reserved sync fails
// so the user can retry immediately rather than wait out the cooldown.
func (s *Store) ReleaseSync(ctx context.Context, ftID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sync_cooldowns WHERE ft_id = $1`, ftID)
	return err
}

// SyncCooldown reports whether a 42 user is currently within the cooldown, and
// the time remaining if so, without claiming a slot. It lets a caller reject a
// known user early (before spending any API request); ReserveSync remains the
// authoritative, atomic claim.
func (s *Store) SyncCooldown(ctx context.Context, ftID int64, cooldown time.Duration) (time.Duration, bool, error) {
	var last time.Time
	err := s.pool.QueryRow(ctx, `SELECT last_sync_at FROM sync_cooldowns WHERE ft_id = $1`, ftID).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if remaining := time.Until(last.Add(cooldown)); remaining > 0 {
		return remaining, true, nil
	}
	return 0, false, nil
}

// PurgeStaleCooldowns deletes sync-cooldown rows whose last sync is older than
// olderThan. Those rows only matter inside the cooldown window, so purging them is
// data-minimisation retention (GDPR Art. 5(1)(e)); it also clears rows left by
// anonymous syncs that never became accounts. Returns the number removed.
func (s *Store) PurgeStaleCooldowns(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM sync_cooldowns WHERE last_sync_at < now() - make_interval(secs => $1)`,
		olderThan.Seconds())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation (23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
