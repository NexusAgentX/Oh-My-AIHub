// Package postgres is the composition root of persistence: each domain keeps
// its SQL and sqlc output in its own <domain>pg package (ADR-0017), and this
// Store bundles them for wiring in cmd/server.
package postgres

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/forumpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/supplierpg"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/c2cpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/catalogpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/channelpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/gatewaypg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/identitypg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/keypg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/observepg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/settingspg"
)

type Store struct {
	Suppliers *supplierpg.Store
	// Books is where the stores that book (ledger, gateway, C2C, observe)
	// report what their ledger transactions did; Cached subscribes the
	// gateway's balance cache to it.
	Books *ledgerpg.Books

	Forum    *forumpg.Store
	Identity *identitypg.Store
	Catalog  *catalogpg.Store
	Settings *settingspg.Store
	Audit    *auditpg.Store
	Ledger   *ledgerpg.Store
	Channels *channelpg.Store
	Keys     *keypg.Store
	Routes   *keypg.Routes
	Gateway  *gatewaypg.Store
	C2C      *c2cpg.Store
	Observe  *observepg.Store
}

func New(pool *pgxpool.Pool) *Store {
	books := ledgerpg.NewBooks()
	return &Store{
		Books:     books,
		Suppliers: supplierpg.NewStore(pool),
		Forum:     forumpg.NewStore(pool),
		Identity:  identitypg.NewStore(pool),
		Catalog:   catalogpg.NewStore(pool),
		Settings:  settingspg.NewStore(pool),
		Audit:     auditpg.NewStore(pool),
		Ledger:    ledgerpg.NewStore(pool, books),
		Channels:  channelpg.NewStore(pool),
		Keys:      keypg.NewStore(pool),
		Routes:    keypg.NewRoutes(pool),
		Gateway:   gatewaypg.NewStore(pool, books),
		C2C:       c2cpg.NewStore(pool, books),
		Observe:   observepg.NewStore(pool, books),
	}
}
