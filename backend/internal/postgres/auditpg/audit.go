// Package auditpg writes and reads the append-only audit log. Every domain
// records its administrative actions through Record inside its own
// transaction (ADR-0020).
package auditpg

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
)

// Event is one audit row. An empty ActorID records a system actor.
type Event struct {
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	Reason     string
	Detail     map[string]any
}

// Record inserts the event through db, normally the caller's open transaction.
func Record(ctx context.Context, db DBTX, event Event) error {
	if event.Detail == nil {
		event.Detail = map[string]any{}
	}
	detail, err := json.Marshal(event.Detail)
	if err != nil {
		return err
	}
	var actor *string
	if event.ActorID != "" {
		actor = &event.ActorID
	}
	return New(db).InsertAudit(ctx, InsertAuditParams{
		ActorID:    actor,
		Action:     event.Action,
		TargetType: event.TargetType,
		TargetID:   event.TargetID,
		Reason:     event.Reason,
		Detail:     detail,
	})
}

// Store implements audit.Store.
type Store struct {
	q *Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{q: New(pool)} }

func (s *Store) ListAudit(ctx context.Context, filter audit.Filter) ([]audit.Entry, error) {
	rows, err := s.q.ListAudit(ctx, ListAuditParams{
		Action: filter.Action, TargetType: filter.TargetType, TargetID: filter.TargetID,
		ActorID: filter.ActorID, BeforeID: filter.BeforeID, RowLimit: int32(filter.Limit),
	})
	if err != nil {
		return nil, err
	}
	entries := make([]audit.Entry, 0, len(rows))
	for _, row := range rows {
		entry := audit.Entry{
			ID: row.ID, Action: row.Action, TargetType: row.TargetType, TargetID: row.TargetID,
			Reason: row.Reason, Detail: row.Detail, CreatedAt: row.CreatedAt,
		}
		if row.ActorID != nil {
			entry.Actor = &audit.Actor{ID: *row.ActorID, Username: deref(row.ActorUsername), DisplayName: deref(row.ActorDisplayName)}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
