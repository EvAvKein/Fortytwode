package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrateAdvisoryLockKey identifies the lock that stops two processes from running
// Migrate at the same time. Postgres "advisory locks" aren't tied to any row or table —
// each is just named by an integer the application picks, and pg_advisory_lock(K) blocks
// until whoever else is holding K releases it. Migrate takes this key with
// pg_advisory_lock on entry and drops it with pg_advisory_unlock on exit, so any
// processes using the same number take turns instead of migrating at once. The value
// itself is meaningless — a shared label, not a secret or a setting — and only has to
// match across processes, so it's hardcoded here; any fixed int64 would do.
const migrateAdvisoryLockKey int64 = 4242_0001

// Migrate applies any embedded migrations not yet recorded in schema_migrations,
// in filename order, each in its own transaction. Filenames are NNNN_name.sql; the
// leading number is the version.
//
// The full SQL of each applied migration is stored in the table, and on every run
// the already-applied files are compared (byte for byte) against their stored copy.
// A mismatch means a migration was edited after it ran — its database no longer
// matches its source — which is reported as an error rather than silently ignored.
//
// 0001 uses IF NOT EXISTS, so an already-populated database is safely stamped at
// version 1 the first time the runner sees it.
func (s *Store) Migrate(ctx context.Context) error {
	// Serialize concurrent migrators (parallel `go test ./...` binaries sharing one DB,
	// or overlapping app starts during a rolling deploy) with a session-level advisory
	// lock. Without it, two processes running CREATE TABLE IF NOT EXISTS at once can trip
	// a pg_type catalog unique-violation — the existence check isn't atomic. The lock is
	// held on a dedicated connection and released explicitly before that connection goes
	// back to the pool, since pgxpool reuses connections and does not clear session locks.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration lock connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	// Unlock on a fresh context so a cancelled ctx can't leave the lock stuck on the
	// pooled connection.
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrateAdvisoryLockKey)

	if _, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INT PRIMARY KEY,
			name       TEXT NOT NULL,
			body       TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	applied := map[int]string{} // version -> stored body
	rows, err := s.pool.Query(ctx, `SELECT version, body FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var (
			version int
			body    string
		)
		if err := rows.Scan(&version, &body); err != nil {
			rows.Close()
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = body
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %q: name must start with a numeric version (NNNN_name.sql): %w", name, err)
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", name, err)
		}

		if stored, ok := applied[version]; ok {
			if stored != string(body) {
				return fmt.Errorf("migration %q (version %d) was modified after being applied; the database no longer matches its source", name, version)
			}
			continue
		}

		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %q: %w", name, err)
		}
		// No-arg Exec uses the simple query protocol, so a file with multiple
		// statements runs as one batch (the extended protocol would reject it).
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("apply migration %q: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name, body) VALUES ($1, $2, $3)`,
			version, name, string(body)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("record migration %q: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %q: %w", name, err)
		}
		fmt.Printf("applied migration %s\n", name)
	}
	return nil
}
