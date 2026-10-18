package detectionpg

import (
	"context"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

// DirectoryResolver authorizes approved targets. The contribution feature adds
// a draft resolver under the same interface; clients cannot supply raw outbound
// URLs or use another contributor's target as an approval bypass.
type DirectoryResolver struct{ Market market.Store }

func (r DirectoryResolver) Resolve(ctx context.Context, actor detection.Actor, input detection.Target) (detection.Resolved, error) {
	if actor.ID == "" || r.Market == nil {
		return detection.Resolved{}, detection.ErrNotFound
	}
	d, err := r.Market.Snapshot(ctx)
	if err != nil {
		return detection.Resolved{}, err
	}
	resolved, err := detection.ResolveDirectory(d, input)
	if err != nil {
		return detection.Resolved{}, err
	}
	var supplierID string
	for _, g := range d.Groups {
		if g.ID == resolved.Target.GroupID {
			supplierID = g.SupplierID
			break
		}
	}
	authorized := false
	for _, v := range d.Suppliers {
		if v.ID == supplierID && (actor.Admin || v.ContributorID == actor.ID) {
			authorized = true
			break
		}
	}
	if !authorized {
		return detection.Resolved{}, detection.ErrNotFound
	}
	return resolved, nil
}
