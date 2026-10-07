package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/catalogpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/channelpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/feeratepg"
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
	*opsStore

	pool *pgxpool.Pool
	// gatewayCommitHook is a deterministic test seam for the PostgreSQL
	// "commit succeeded but the acknowledgement was lost" outcome. Production
	// stores leave it nil. Callers must still disambiguate every returned commit
	// error by rereading the immutable business fact.
	gatewayCommitHook func(operation, resourceID string) error
}

func New(pool *pgxpool.Pool) *Store {
	ledgerStore := ledgerpg.NewStore(pool)
	return &Store{
		identityStore: identitypg.NewStore(pool),
		catalogStore:  catalogpg.NewStore(pool),
		feeRateStore:  feeratepg.NewStore(pool),
		channelStore:  channelpg.NewStore(pool),
		ledgerStore:   ledgerStore,
		opsStore:      opspg.NewStore(pool, ledgerStore),
		pool:          pool,
	}
}

func (s *Store) commitGatewayTransaction(ctx context.Context, tx pgx.Tx, operation, resourceID string) error {
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if s.gatewayCommitHook != nil {
		return s.gatewayCommitHook(operation, resourceID)
	}
	return nil
}

type scanner interface {
	Scan(...any) error
}

type rowQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type tierQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// insertAudit stays for the domains that have not moved to sqlc yet; migrated
// domains call auditpg.Record directly.
func insertAudit(ctx context.Context, db auditpg.DBTX, actorID, action, targetType, targetID, reason string, details map[string]any) error {
	return auditpg.Record(ctx, db, auditpg.Event{
		ActorID: actorID, Action: action, TargetType: targetType, TargetID: targetID,
		Reason: reason, Details: details,
	})
}

// loadModelPriceTiers adapts the catalog tier loader to the narrow read-only
// interfaces still used by the hand-written channel and gateway code. It goes
// away when those domains are migrated and pass a full transaction instead.
func loadModelPriceTiers(ctx context.Context, queryer tierQueryer, modelIDs []string) (map[string][]ledger.PriceTier, error) {
	return catalogpg.PriceTiersByModel(ctx, readOnlyDB{queryer}, modelIDs)
}

func priceTiersEqual(left, right []ledger.PriceTier) bool {
	return catalogpg.PriceTiersEqual(left, right)
}

// readOnlyDB lets a Query-only handle satisfy catalogpg.DBTX.
type readOnlyDB struct{ tierQueryer }

func (readOnlyDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("postgres: readOnlyDB does not support Exec")
}

func (readOnlyDB) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("postgres: readOnlyDB does not support QueryRow")
}
