package store

import "github.com/jackc/pgx/v5/pgxpool"

func (s *Store) TestPool() *pgxpool.Pool {
	return s.pool
}
