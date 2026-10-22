package resourcepg

import "github.com/jackc/pgx/v5/pgxpool"

// Store holds the Codex session namespace index. Remote resource ownership is
// no longer tracked: the gateway keeps no state beyond the response itself.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }
