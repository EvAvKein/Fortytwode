package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *Store) TestPool() *pgxpool.Pool {
	return s.pool
}

// TestLoadCampusZones exposes the Open-time index build, which tests otherwise
// can't reach: they open stores through OpenRaw.
func (s *Store) TestLoadCampusZones(ctx context.Context) error {
	return s.loadCampusZones(ctx)
}
