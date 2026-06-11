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
