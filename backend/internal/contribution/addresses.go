package contribution

import (
	"context"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"slices"
)

// ReuseAddress adds an approved same-supplier address reference. It does not
// copy or modify that address, and never admits another private proposal.
func (s *Service) ReuseAddress(ctx context.Context, actor Actor, id string, revision int64, groupID, addressID string) (Draft, error) {
	return s.Store.Edit(ctx, id, func(d *Draft, u UnitOfWork) error {
		if !authorized(actor, *d) {
			return ErrNotFound
		}
		if d.Revision != revision || !editable(actor, *d) {
			return ErrConflict
		}
		var group *market.Group
		for i := range d.Changes.Groups {
			if d.Changes.Groups[i].Value.ID == groupID {
				group = &d.Changes.Groups[i].Value
				break
			}
		}
		if group == nil || group.DeletedAt != nil {
			return ErrInvalid
		}
		live, err := u.Directory(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, a := range live.Addresses {
			if a.ID == addressID && a.SupplierID == group.SupplierID && a.DeletedAt == nil {
				found = true
			}
		}
		if !found {
			return ErrNotFound
		}
		index := -1
		for i, set := range d.Changes.GroupAddresses {
			if set.GroupID == groupID {
				index = i
				break
			}
		}
		if index < 0 {
			d.Changes.GroupAddresses = append(d.Changes.GroupAddresses, market.GroupAddressSet{GroupID: groupID, Links: []market.GroupAddress{}})
			index = len(d.Changes.GroupAddresses) - 1
		}
		set := &d.Changes.GroupAddresses[index]
		if slices.ContainsFunc(set.Links, func(l market.GroupAddress) bool { return l.AddressID == addressID }) {
			return ErrConflict
		}
		// Remove only unfilled new placeholders from this group. Existing addresses
		// and any real draft URL remain explicit, reviewable operations.
		empty := map[string]bool{}
		for _, a := range d.Changes.Addresses {
			if a.BaseRevision == 0 && a.Value.BaseURL == "" && slices.ContainsFunc(set.Links, func(l market.GroupAddress) bool { return l.AddressID == a.Value.ID }) {
				empty[a.Value.ID] = true
			}
		}
		set.Links = slices.DeleteFunc(set.Links, func(l market.GroupAddress) bool { return empty[l.AddressID] })
		d.Changes.Addresses = slices.DeleteFunc(d.Changes.Addresses, func(a market.Change[market.Address]) bool {
			if !empty[a.Value.ID] {
				return false
			}
			for _, other := range d.Changes.GroupAddresses {
				for _, link := range other.Links {
					if link.AddressID == a.Value.ID {
						return false
					}
				}
			}
			return true
		})
		if len(set.Links) >= 32 {
			return ErrInvalid
		}
		linkID, err := ids.NewUUID()
		if err != nil {
			return err
		}
		set.Links = append(set.Links, market.GroupAddress{ID: linkID, GroupID: groupID, SupplierID: group.SupplierID, AddressID: addressID, Position: int32(len(set.Links))})
		d.State = "draft"
		d.SubmittedAt = nil
		d.ReviewReason = ""
		d.ReviewedBy = nil
		advance(d)
		return u.Audit(ctx, actor.ID, "address_reused", map[string]any{"revision": d.Revision, "group_id": groupID, "address_id": addressID})
	})
}
