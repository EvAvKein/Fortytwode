package storetest

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	migrateOnce sync.Once
	migrateErr  error
)

func OpenStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()

	// Run migrations exactly once across all parallel tests.
	migrateOnce.Do(func() {
		st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
		if err != nil {
			migrateErr = err
			return
		}
		st.Close()
	})
	if migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}

	// Each test gets its own pool for connection isolation, but no re-migration.
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return store.OpenRaw(pool)
}
