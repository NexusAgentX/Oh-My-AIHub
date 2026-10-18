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
	var model *market.GroupModel
	groupID := ""
	if input.Binding.TargetKind == "group_model" {
		for i := range d.Models {
			if d.Models[i].ID == input.Binding.TargetID {
				model = &d.Models[i]
				groupID = model.GroupID
				break
			}
		}
		if model == nil {
			return detection.Resolved{}, detection.ErrNotFound
		}
	} else if input.Binding.TargetKind == "group" {
		groupID = input.Binding.TargetID
	} else {
		return detection.Resolved{}, detection.ErrInvalid
	}
	var group *market.Group
	for i := range d.Groups {
		if d.Groups[i].ID == groupID {
			group = &d.Groups[i]
			break
		}
	}
	if group == nil {
		return detection.Resolved{}, detection.ErrNotFound
	}
	authorized := false
	for _, supplier := range d.Suppliers {
		if supplier.ID == group.SupplierID && (actor.Admin || supplier.ContributorID == actor.ID) {
			authorized = true
			break
		}
	}
	if !authorized {
		return detection.Resolved{}, detection.ErrNotFound
	}
	target := detection.Target{GroupID: group.ID, Binding: market.EvidenceBinding{TargetKind: "group", TargetID: group.ID, TargetRevision: group.Revision, CapabilityDigest: group.CapabilityDigest}}
	if model != nil {
		target.Binding.TargetKind = "group_model"
		target.Binding.TargetID = model.ID
		target.Binding.TargetRevision = model.Revision
		target.Binding.CapabilityDigest = model.CapabilityDigest
		target.ModelType = model.ModelType
		target.Model = model.UpstreamModel
	}
	result := detection.Resolved{Target: target, Addresses: []string{}}
	if group.Defaults.UserAgent != nil {
		result.UserAgent = *group.Defaults.UserAgent
	}
	if group.Defaults.HeaderRules != nil {
		result.Headers = *group.Defaults.HeaderRules
	}
	for _, link := range d.GroupAddresses {
		if link.GroupID == group.ID {
			for _, address := range d.Addresses {
				if address.ID == link.AddressID {
					result.Addresses = append(result.Addresses, address.BaseURL)
					break
				}
			}
		}
	}
	return result, nil
}
