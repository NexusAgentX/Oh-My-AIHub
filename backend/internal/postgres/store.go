// Package postgres is the composition root of persistence: each domain keeps
// its SQL and sqlc output in its own <domain>pg package (ADR-0017), and this
// Store bundles them for wiring in cmd/server.
package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/catalogpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/channelpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/gatewaypg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/identitypg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/keypg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/settingspg"
)

type Store struct {
	Identity *identitypg.Store
	Catalog  *catalogpg.Store
	Settings *settingspg.Store
	Audit    *auditpg.Store
	Ledger   *ledgerpg.Store
	Channels *channelpg.Store
	Keys     *keypg.Store
	Routes   *keypg.Routes
	Gateway  *gatewaypg.Store
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{
		Identity: identitypg.NewStore(pool),
		Catalog:  catalogpg.NewStore(pool),
		Settings: settingspg.NewStore(pool),
		Audit:    auditpg.NewStore(pool),
		Ledger:   ledgerpg.NewStore(pool),
		Channels: channelpg.NewStore(pool),
		Keys:     keypg.NewStore(pool),
		Routes:   keypg.NewRoutes(pool),
		Gateway:  gatewaypg.NewStore(pool),
	}
}
