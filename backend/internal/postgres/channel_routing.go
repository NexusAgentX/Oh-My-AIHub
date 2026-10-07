package postgres

import (
	"context"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/channelpg"
)

// ResolveRoutingTargets lets a ledger transaction act as a channel.RoutingStore,
// so offers are resolved inside the same transaction as the ledger writes.
func (t *LedgerTransaction) ResolveRoutingTargets(ctx context.Context, offerIDs []string) ([]channel.PoolOfferStatus, []channel.RoutingTarget, error) {
	return channelpg.ResolveRoutingTargets(ctx, t.Tx, offerIDs)
}

var _ channel.RoutingStore = (*LedgerTransaction)(nil)
