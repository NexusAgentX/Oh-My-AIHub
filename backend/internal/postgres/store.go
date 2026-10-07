package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/c2cpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/catalogpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/channelpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/feeratepg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/gatewaypg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/identitypg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/opspg"
)

// Each domain package names its implementation Store; the aliases give the
// embedded fields distinct names.
type (
	identityStore = identitypg.Store
	catalogStore  = catalogpg.Store
	feeRateStore  = feeratepg.Store
	channelStore  = channelpg.Store
	ledgerStore   = ledgerpg.Store
	gatewayStore  = gatewaypg.Store
	c2cStore      = c2cpg.Store
	opsStore      = opspg.Store
)

// Store is the composition root of persistence. Domains migrated to sqlc live
// in their own <domain>pg packages and are embedded here, so their methods are
// promoted and the services keep receiving one value. Domains that still use
// hand-written SQL implement their methods directly in this package until they
// are migrated (see ADR-0017).
type Store struct {
	*identityStore
	*catalogStore
	*feeRateStore
	*channelStore
	*ledgerStore
	*gatewayStore
	*c2cStore
	*opsStore

	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	ledgerStore := ledgerpg.NewStore(pool)
	return &Store{
		identityStore: identitypg.NewStore(pool),
		catalogStore:  catalogpg.NewStore(pool),
		feeRateStore:  feeratepg.NewStore(pool),
		channelStore:  channelpg.NewStore(pool),
		ledgerStore:   ledgerStore,
		gatewayStore:  gatewaypg.NewStore(pool),
		c2cStore:      c2cpg.NewStore(pool),
		opsStore:      opspg.NewStore(pool, ledgerStore),
		pool:          pool,
	}
}

type scanner interface {
	Scan(...any) error
}

// insertAudit stays for the domains that have not moved to sqlc yet; migrated
// domains call auditpg.Record directly.
func insertAudit(ctx context.Context, db auditpg.DBTX, actorID, action, targetType, targetID, reason string, details map[string]any) error {
	return auditpg.Record(ctx, db, auditpg.Event{
		ActorID: actorID, Action: action, TargetType: targetType, TargetID: targetID,
		Reason: reason, Details: details,
	})
}
