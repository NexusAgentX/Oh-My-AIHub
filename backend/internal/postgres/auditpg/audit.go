// Package auditpg writes append-only audit events. Every domain records its
// administrative actions through Record inside its own transaction.
package auditpg

import (
	"context"
	"encoding/json"
)

// Event is one audit row. An empty ActorID records a system actor.
type Event struct {
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	Reason     string
	Details    map[string]any
}

// Record inserts the event through db, normally the caller's open transaction.
func Record(ctx context.Context, db DBTX, event Event) error {
	details, err := json.Marshal(event.Details)
	if err != nil {
		return err
	}
	var actor *string
	if event.ActorID != "" {
		actor = &event.ActorID
	}
	return New(db).InsertAuditEvent(ctx, InsertAuditEventParams{
		ActorAccountID: actor,
		Action:         event.Action,
		TargetType:     event.TargetType,
		TargetID:       event.TargetID,
		Reason:         event.Reason,
		Details:        details,
	})
}
