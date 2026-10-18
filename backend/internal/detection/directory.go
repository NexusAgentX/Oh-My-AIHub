package detection

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"slices"
	"sort"
)

// ResolveDirectory resolves trusted configuration only. Callers must authorize
// the published target or private contribution before calling it.
func ResolveDirectory(d market.Directory, input Target) (Resolved, error) {
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
			return Resolved{}, ErrNotFound
		}
	} else if input.Binding.TargetKind == "group" {
		groupID = input.Binding.TargetID
	} else {
		return Resolved{}, ErrInvalid
	}
	var group *market.Group
	for i := range d.Groups {
		if d.Groups[i].ID == groupID {
			group = &d.Groups[i]
			break
		}
	}
	if group == nil {
		return Resolved{}, ErrNotFound
	}
	target := Target{DraftID: input.DraftID, GroupID: group.ID, Binding: market.EvidenceBinding{TargetKind: "group", TargetID: group.ID, TargetRevision: group.Revision, CapabilityDigest: group.CapabilityDigest}}
	if model != nil {
		target.Binding.TargetKind = "group_model"
		target.Binding.TargetID = model.ID
		target.Binding.TargetRevision = model.Revision
		target.Binding.CapabilityDigest = model.CapabilityDigest
		target.ModelType = model.ModelType
		target.Model = model.UpstreamModel
		target.ContextWindow = model.ContextWindow
		target.MaxOutputTokens = model.MaxOutputTokens
		if model.QuoteID != nil {
			for _, quote := range d.Quotes {
				if quote.ID == *model.QuoteID && quote.GroupModelID == model.ID {
					target.Profile = profileFor(quote)
					break
				}
			}
		}
		if model.ModelType != "image" {
			target.Profile.Images = nil
		}
	}
	result := Resolved{Target: target, Addresses: []string{}}
	if group.Defaults.UserAgent != nil {
		result.UserAgent = *group.Defaults.UserAgent
	}
	if group.Defaults.HeaderRules != nil {
		result.Headers = *group.Defaults.HeaderRules
	}
	d.GroupAddresses = slices.Clone(d.GroupAddresses)
	sort.SliceStable(d.GroupAddresses, func(i, j int) bool { return d.GroupAddresses[i].Position < d.GroupAddresses[j].Position })
	for _, link := range d.GroupAddresses {
		if link.GroupID == group.ID && link.DeletedAt == nil {
			for _, address := range d.Addresses {
				if address.ID == link.AddressID && address.DeletedAt == nil {
					result.Addresses = append(result.Addresses, address.BaseURL)
					break
				}
			}
		}
	}
	return result, nil
}
