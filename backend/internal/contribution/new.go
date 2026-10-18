package contribution

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type NewInput struct {
	Kind      string
	TargetID  string
	ParentID  string
	Action    string
	Starter   bool
	ModelType string
}

func copyChanges(c market.Changes) market.Changes {
	data, _ := json.Marshal(c)
	var copied market.Changes
	_ = json.Unmarshal(data, &copied)
	return copied
}

// New allocates draft object identities before any detection. Parent and child
// proposals can therefore be measured and published atomically as one bundle.
func (s *Service) New(ctx context.Context, actor Actor, input NewInput) (Draft, error) {
	if actor.ID == "" || s.Live == nil || !slices.Contains([]string{"supplier", "group", "group_model", "model_id"}, input.Kind) {
		return Draft{}, ErrInvalid
	}
	if input.Kind == "model_id" && (!actor.Admin || !gatewaycontract.ValidModelID(input.TargetID)) {
		return Draft{}, ErrInvalid
	}
	if input.Action == "" {
		input.Action = "edit"
	}
	if !slices.Contains([]string{"edit", "delete", "disable", "restore"}, input.Action) {
		return Draft{}, ErrInvalid
	}
	if input.Kind == "model_id" && input.Action == "delete" {
		return Draft{}, ErrInvalid
	}
	if input.ModelType == "" {
		input.ModelType = "text"
	}
	if input.ModelType != "text" && input.ModelType != "image" {
		return Draft{}, ErrInvalid
	}
	live, err := s.Live.Snapshot(ctx)
	if err != nil {
		return Draft{}, err
	}
	c := market.Changes{}
	title := ""
	target := input.TargetID
	if target != "" {
		found := false
		switch input.Kind {
		case "model_id":
			v := market.ModelRule{ModelID: target}
			for _, rule := range live.Governance {
				if rule.ModelID == target {
					v = rule
				}
			}
			c.Governance = []market.Change[market.ModelRule]{{Value: v, BaseRevision: v.Revision}}
			title = target
			found = true

		case "supplier":
			for _, v := range live.Suppliers {
				if v.ID == target {
					c.Suppliers = append(c.Suppliers, market.Change[market.Supplier]{Value: v, BaseRevision: v.Revision})
					title = v.Name
					found = true
				}
			}
		case "group":
			for _, v := range live.Groups {
				if v.ID == target {
					c.Groups = append(c.Groups, market.Change[market.Group]{Value: v, BaseRevision: v.Revision})
					title = v.Name
					found = true
				}
			}
			if found {
				set := market.GroupAddressSet{GroupID: target, Links: []market.GroupAddress{}}
				for _, link := range live.GroupAddresses {
					if link.GroupID == target {
						set.Links = append(set.Links, link)
						for _, v := range live.Addresses {
							if v.ID == link.AddressID {
								c.Addresses = append(c.Addresses, market.Change[market.Address]{Value: v, BaseRevision: v.Revision})
							}
						}
					}
				}
				c.GroupAddresses = append(c.GroupAddresses, set)
			}
		case "group_model":
			for _, v := range live.Models {
				if v.ID == target {
					c.Models = append(c.Models, market.Change[market.GroupModel]{Value: v, BaseRevision: v.Revision})
					title = v.ModelID
					found = true
				}
			}
			for _, q := range live.Quotes {
				if q.GroupModelID == target {
					c.Quotes = append(c.Quotes, q)
				}
			}
		}
		if !found {
			return Draft{}, ErrNotFound
		}
	} else {
		if input.Action != "edit" {
			return Draft{}, ErrInvalid
		}
		switch input.Kind {
		case "supplier":
			target, err = ids.NewUUID()
			c.Suppliers = []market.Change[market.Supplier]{{Value: market.Supplier{ID: target, ContributorID: actor.ID, Kind: "relay", Enabled: true}}}
			title = "新供应商"
		case "group":
			if !supplierPresent(live, market.Changes{}, input.ParentID) {
				return Draft{}, ErrNotFound
			}
			target, err = addGroup(&c, input.ParentID)
			title = "新分组"
		case "group_model":
			if !groupPresent(live, market.Changes{}, input.ParentID) {
				return Draft{}, ErrNotFound
			}
			target, err = addModel(&c, input.ParentID, input.ModelType)
			title = "新模型报价"
		}
		if err != nil {
			return Draft{}, err
		}
	}
	base := copyChanges(c)
	if input.TargetID == "" {
		base = market.Changes{}
	}
	if input.Kind == "group_model" && input.TargetID != "" && input.Action == "edit" {
		for i := range c.Quotes {
			c.Quotes[i].ID, err = ids.NewUUID()
			if err != nil {
				return Draft{}, err
			}
		}
	}
	if input.Kind == "group_model" && input.Action != "edit" {
		c.Quotes = nil
	}
	if input.Action == "delete" || input.Action == "disable" || input.Action == "restore" {
		now := time.Now().UTC()
		enabled := input.Action == "restore"
		switch input.Kind {
		case "model_id":
			c.Governance[0].Value.Disabled = !enabled
		case "supplier":
			if input.Action == "delete" {
				for _, group := range live.Groups {
					if group.SupplierID == target && group.DeletedAt == nil {
						return Draft{}, market.ErrHasGroups
					}
				}
				c.Suppliers[0].Value.DeletedAt = &now
			}
			c.Suppliers[0].Value.Enabled = enabled
		case "group":
			if input.Action == "delete" {
				c.Groups[0].Value.DeletedAt = &now
				c.GroupAddresses = nil
				c.Addresses = nil
			}
			c.Groups[0].Value.Enabled = enabled
		case "group_model":
			if input.Action == "delete" {
				c.Models[0].Value.DeletedAt = &now
				c.Quotes = nil
			}
			c.Models[0].Value.Enabled = enabled
		}
	}
	if input.Starter && input.TargetID == "" {
		parent := target
		if input.Kind == "supplier" {
			parent, err = addGroup(&c, target)
			if err != nil {
				return Draft{}, err
			}
		}
		if input.Kind == "supplier" || input.Kind == "group" {
			if _, err = addModel(&c, parent, input.ModelType); err != nil {
				return Draft{}, err
			}
		}
	}
	draft, err := newDraft(actor, input.Kind, target, title, c)
	if err != nil {
		return Draft{}, err
	}
	draft.Base = base
	draft.PendingPrices = []string{}
	for _, quote := range c.Quotes {
		known := false
		for _, prior := range base.Quotes {
			known = known || prior.GroupModelID == quote.GroupModelID
		}
		if !known {
			draft.PendingPrices = append(draft.PendingPrices, quote.GroupModelID)
		}
	}
	if err = s.Store.Create(ctx, draft); err != nil {
		return Draft{}, err
	}
	return draft, nil
}
func supplierPresent(live market.Directory, c market.Changes, id string) bool {
	for _, change := range c.Suppliers {
		if change.Value.ID == id {
			return change.Value.DeletedAt == nil
		}
	}
	for _, v := range live.Suppliers {
		if v.ID == id && v.DeletedAt == nil {
			return true
		}
	}
	return false
}
func groupPresent(live market.Directory, c market.Changes, id string) bool {
	for _, change := range c.Groups {
		if change.Value.ID == id {
			return change.Value.DeletedAt == nil
		}
	}
	for _, v := range live.Groups {
		if v.ID == id && v.DeletedAt == nil {
			return true
		}
	}
	return false
}
func addGroup(c *market.Changes, supplierID string) (string, error) {
	id, err := ids.NewUUID()
	if err != nil {
		return "", err
	}
	c.Groups = append(c.Groups, market.Change[market.Group]{Value: market.Group{ID: id, SupplierID: supplierID, Enabled: true}})
	if err = addAddress(c, id, supplierID); err != nil {
		return "", err
	}
	return id, nil
}
func addAddress(c *market.Changes, groupID, supplierID string) error {
	id, err := ids.NewUUID()
	if err != nil {
		return err
	}
	linkID, err := ids.NewUUID()
	if err != nil {
		return err
	}
	c.Addresses = append(c.Addresses, market.Change[market.Address]{Value: market.Address{ID: id, SupplierID: supplierID}})
	for i := range c.GroupAddresses {
		if c.GroupAddresses[i].GroupID == groupID {
			c.GroupAddresses[i].Links = append(c.GroupAddresses[i].Links, market.GroupAddress{ID: linkID, GroupID: groupID, SupplierID: supplierID, AddressID: id})
			return nil
		}
	}
	c.GroupAddresses = append(c.GroupAddresses, market.GroupAddressSet{GroupID: groupID, Links: []market.GroupAddress{{ID: linkID, GroupID: groupID, SupplierID: supplierID, AddressID: id}}})
	return nil
}
func addModel(c *market.Changes, groupID, modelType string) (string, error) {
	id, err := ids.NewUUID()
	if err != nil {
		return "", err
	}
	quoteID, err := ids.NewUUID()
	if err != nil {
		return "", err
	}
	c.Models = append(c.Models, market.Change[market.GroupModel]{Value: market.GroupModel{ID: id, GroupID: groupID, ModelType: modelType, Enabled: true}})
	c.Quotes = append(c.Quotes, market.Quote{ID: quoteID, GroupModelID: id, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Details: map[string]money.Amount{}}, Tiers: []market.Tier{}, UnitPrices: []market.UnitPrice{}})
	return id, nil
}

func (s *Service) AddChild(ctx context.Context, actor Actor, id string, revision int64, kind, parentID, modelType string) (Draft, error) {
	if modelType == "" {
		modelType = "text"
	}
	if modelType != "text" && modelType != "image" {
		return Draft{}, ErrInvalid
	}
	return s.Store.Edit(ctx, id, func(draft *Draft, u UnitOfWork) error {
		if !authorized(actor, *draft) {
			return ErrNotFound
		}
		if draft.Revision != revision || !editable(actor, *draft) {
			return ErrConflict
		}
		live, err := u.Directory(ctx)
		if err != nil {
			return err
		}
		switch kind {
		case "group":
			if !supplierPresent(live, draft.Changes, parentID) {
				return ErrNotFound
			}
			_, err = addGroup(&draft.Changes, parentID)
		case "group_model":
			if !groupPresent(live, draft.Changes, parentID) {
				return ErrNotFound
			}
			var modelID string
			modelID, err = addModel(&draft.Changes, parentID, modelType)
			if err == nil {
				draft.PendingPrices = append(draft.PendingPrices, modelID)
			}
		case "address":
			supplierID := ""
			for _, v := range draft.Changes.Groups {
				if v.Value.ID == parentID {
					supplierID = v.Value.SupplierID
				}
			}
			if supplierID == "" {
				return ErrInvalid
			}
			err = addAddress(&draft.Changes, parentID, supplierID)
		default:
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		c := draft.Changes
		if len(c.Suppliers)+len(c.Groups)+len(c.Addresses)+len(c.GroupAddresses)+len(c.Models)+len(c.Quotes) > 1024 {
			return ErrInvalid
		}
		draft.State = "draft"
		draft.SubmittedAt = nil
		advance(draft)
		return u.Audit(ctx, actor.ID, "child_added", map[string]any{"revision": draft.Revision, "kind": kind})
	})
}
