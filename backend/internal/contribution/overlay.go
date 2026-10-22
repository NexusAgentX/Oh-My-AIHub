package contribution

import (
	"slices"
	"sort"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"unicode/utf8"
)

// Overlay validates a proposal against its live bases and constructs only its
// private view. It is shared by draft preview and transactional approval; no
// unapproved value is installed in the live directory.
func Overlay(draft Draft, live market.Directory) (market.Directory, error) {
	c := draft.Changes
	if len(c.Governance)+len(c.Suppliers)+len(c.Groups)+len(c.Addresses)+len(c.GroupAddresses)+len(c.Models)+len(c.Quotes) > 1024 {
		return market.Directory{}, ErrInvalid
	}
	suppliers := map[string]market.Supplier{}
	groups := map[string]market.Group{}
	addresses := map[string]market.Address{}
	models := map[string]market.GroupModel{}
	quotes := map[string]market.Quote{}
	for _, v := range live.Suppliers {
		suppliers[v.ID] = v
	}
	for _, v := range live.Groups {
		groups[v.ID] = v
	}
	for _, v := range live.Addresses {
		addresses[v.ID] = v
	}
	for _, v := range live.Models {
		models[v.ID] = v
	}
	for _, v := range live.Quotes {
		quotes[v.GroupModelID] = v
	}
	bases := map[string]bool{}
	check := func(id string, base, revision int64, exists bool) error {
		if id == "" || base < 0 || bases[id] {
			return ErrInvalid
		}
		bases[id] = true
		if (base == 0 && exists) || (base > 0 && (!exists || base != revision)) {
			return ErrConflict
		}
		return nil
	}
	changedGroups, changedModels := map[string]bool{}, map[string]bool{}
	for _, change := range c.Suppliers {
		v := change.Value
		prior, exists := suppliers[v.ID]
		if err := check(v.ID, change.BaseRevision, prior.Revision, exists); err != nil {
			return market.Directory{}, err
		}
		if market.ValidateSupplier(v) != nil {
			return market.Directory{}, ErrInvalid
		}
		if v.DeletedAt != nil {
			for _, group := range live.Groups {
				if group.SupplierID == v.ID && group.DeletedAt == nil {
					return market.Directory{}, market.ErrHasGroups
				}
			}
		}
		v.Revision = change.BaseRevision + 1
		if change.BaseRevision == 0 {
			v.ContributorID = draft.OwnerID
		} else {
			v.ContributorID = prior.ContributorID
		}
		suppliers[v.ID] = v
	}
	for _, change := range c.Groups {
		v := change.Value
		prior, exists := groups[v.ID]
		if err := check(v.ID, change.BaseRevision, prior.Revision, exists); err != nil {
			return market.Directory{}, err
		}
		parent, ok := suppliers[v.SupplierID]
		if !ok || parent.DeletedAt != nil || (exists && prior.SupplierID != v.SupplierID) || market.ValidateGroup(v) != nil {
			return market.Directory{}, ErrInvalid
		}
		v.Revision = change.BaseRevision + 1
		v.CapabilityDigest = market.GroupCapabilityDigest(v)
		groups[v.ID] = v
		changedGroups[v.ID] = true
	}
	for _, change := range c.Addresses {
		v := change.Value
		prior, exists := addresses[v.ID]
		if err := check(v.ID, change.BaseRevision, prior.Revision, exists); err != nil {
			return market.Directory{}, err
		}
		parent, ok := suppliers[v.SupplierID]
		if !ok || parent.DeletedAt != nil || (exists && prior.SupplierID != v.SupplierID) || v.BaseURL == "" {
			return market.Directory{}, ErrInvalid
		}
		v.Revision = change.BaseRevision + 1
		addresses[v.ID] = v
	}
	links := slices.Clone(live.GroupAddresses)
	setSeen := map[string]bool{}
	for _, set := range c.GroupAddresses {
		group, ok := groups[set.GroupID]
		if !ok || setSeen[set.GroupID] || !changedGroups[set.GroupID] || group.DeletedAt != nil || len(set.Links) > 32 {
			return market.Directory{}, ErrInvalid
		}
		setSeen[set.GroupID] = true
		links = slices.DeleteFunc(links, func(link market.GroupAddress) bool { return link.GroupID == set.GroupID })
		seen := map[string]bool{}
		for i, link := range set.Links {
			address, ok := addresses[link.AddressID]
			if !ok || address.DeletedAt != nil || address.SupplierID != group.SupplierID || link.SupplierID != group.SupplierID || link.GroupID != group.ID || link.ID == "" || seen[link.AddressID] {
				return market.Directory{}, ErrInvalid
			}
			seen[link.AddressID] = true
			link.Position = int32(i)
			links = append(links, link)
		}
	}
	for _, change := range c.Models {
		v := change.Value
		prior, exists := models[v.ID]
		if err := check(v.ID, change.BaseRevision, prior.Revision, exists); err != nil {
			return market.Directory{}, err
		}
		if v.UpstreamModel == "" {
			v.UpstreamModel = v.ModelID
		}
		if v.DisplayName == "" {
			v.DisplayName = v.ModelID
		}
		group, ok := groups[v.GroupID]
		if !ok || group.DeletedAt != nil || (exists && prior.GroupID != v.GroupID) || market.ValidateGroupModel(v) != nil {
			return market.Directory{}, ErrInvalid
		}
		v.Revision = change.BaseRevision + 1
		v.CapabilityDigest = market.ModelCapabilityDigest(group.CapabilityDigest, v)
		models[v.ID] = v
		changedModels[v.ID] = true
	}
	quoteSeen := map[string]bool{}
	for _, quote := range c.Quotes {
		model, ok := models[quote.GroupModelID]
		if !ok || !changedModels[quote.GroupModelID] || quoteSeen[quote.GroupModelID] || quote.ID == "" || model.DeletedAt != nil || market.ValidateQuote(quote) != nil || (quote.BillingUnit == "per_successful_image" && model.ModelType != "image") {
			return market.Directory{}, ErrInvalid
		}
		quoteSeen[quote.GroupModelID] = true
		if prior, exists := quotes[model.ID]; exists {
			quote.Version = prior.Version + 1
		} else {
			quote.Version = 1
		}
		quotes[model.ID] = quote
	}
	out := market.Directory{Governance: slices.Clone(live.Governance), Suppliers: []market.Supplier{}, Groups: []market.Group{}, Addresses: []market.Address{}, GroupAddresses: []market.GroupAddress{}, Models: []market.GroupModel{}, Quotes: []market.Quote{}}
	for _, change := range c.Governance {
		v := change.Value
		prior := int64(0)
		index := -1
		for i, rule := range out.Governance {
			if rule.ModelID == v.ModelID {
				prior = rule.Revision
				index = i
			}
		}
		if !gatewaycontract.ValidModelID(v.ModelID) || utf8.RuneCountInString(v.Reason) > 1000 {
			return market.Directory{}, ErrInvalid
		}
		if err := check("rule:"+v.ModelID, change.BaseRevision, prior, index >= 0); err != nil {
			return market.Directory{}, err
		}
		v.Revision = change.BaseRevision + 1
		if index < 0 {
			out.Governance = append(out.Governance, v)
		} else {
			out.Governance[index] = v
		}
	}
	for _, v := range suppliers {
		if v.DeletedAt == nil {
			out.Suppliers = append(out.Suppliers, v)
		}
	}
	for _, v := range groups {
		if v.DeletedAt == nil {
			out.Groups = append(out.Groups, v)
		}
	}
	for _, v := range addresses {
		if v.DeletedAt == nil {
			out.Addresses = append(out.Addresses, v)
		}
	}
	for _, v := range links {
		if group, ok := groups[v.GroupID]; ok && group.DeletedAt == nil && addresses[v.AddressID].DeletedAt == nil {
			out.GroupAddresses = append(out.GroupAddresses, v)
		}
	}
	for _, v := range models {
		group, ok := groups[v.GroupID]
		if !ok || group.DeletedAt != nil || v.DeletedAt != nil {
			continue
		}
		v.CapabilityDigest = market.ModelCapabilityDigest(group.CapabilityDigest, v)
		if quote, exists := quotes[v.ID]; exists {
			quoteID := quote.ID
			v.QuoteID = &quoteID
			out.Quotes = append(out.Quotes, quote)
		} else if changedModels[v.ID] {
			return market.Directory{}, ErrInvalid
		}
		out.Models = append(out.Models, v)
	}
	modelTypes := map[string]string{}
	references := map[string]bool{}
	for _, model := range out.Models {
		if model.IsReference {
			group := groups[model.GroupID]
			if suppliers[group.SupplierID].Kind != "official_api" {
				return market.Directory{}, ErrInvalid
			}
		}
		if !model.Enabled {
			continue
		}
		if prior, exists := modelTypes[model.ModelID]; exists && prior != model.ModelType {
			return market.Directory{}, ErrConflict
		}
		modelTypes[model.ModelID] = model.ModelType
		if model.IsReference && model.Enabled {
			group := groups[model.GroupID]
			supplier := suppliers[group.SupplierID]
			if supplier.Kind != "official_api" || references[model.ModelID] {
				return market.Directory{}, ErrConflict
			}
			references[model.ModelID] = true
		}
	}
	sort.Slice(out.Suppliers, func(i, j int) bool { return out.Suppliers[i].ID < out.Suppliers[j].ID })
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].ID < out.Groups[j].ID })
	sort.Slice(out.Addresses, func(i, j int) bool { return out.Addresses[i].ID < out.Addresses[j].ID })
	sort.Slice(out.Models, func(i, j int) bool { return out.Models[i].ID < out.Models[j].ID })
	sort.Slice(out.Quotes, func(i, j int) bool { return out.Quotes[i].GroupModelID < out.Quotes[j].GroupModelID })
	return out, nil
}
