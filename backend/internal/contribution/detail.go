package contribution

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type Detail struct {
	Draft          Draft            `json:"draft"`
	Stage          string           `json:"stage"`
	Addresses      []market.Address `json:"addresses"`
	LinkedChannels int64            `json:"linked_channels"`
}

type impactReader interface {
	AffectedChannels(context.Context, market.Changes) (int64, error)
}

// Detail exposes a proposal preview without binding it to detection evidence.
// Stage only reports whether the saved contents can be submitted for review.
func (s *Service) Detail(ctx context.Context, actor Actor, id string) (Detail, error) {
	out := Detail{Addresses: []market.Address{}}
	draft, err := s.Store.Edit(ctx, id, func(d *Draft, u UnitOfWork) error {
		if !authorized(actor, *d) {
			return ErrNotFound
		}
		live, err := u.Directory(ctx)
		if err != nil {
			return err
		}
		wanted := map[string]bool{}
		for _, set := range d.Changes.GroupAddresses {
			for _, link := range set.Links {
				wanted[link.AddressID] = true
			}
		}
		for _, address := range live.Addresses {
			if wanted[address.ID] {
				out.Addresses = append(out.Addresses, address)
			}
		}
		if d.State == "approved" || d.State == "publication_pending" {
			out.Stage = d.State
			return nil
		}
		if _, err = Overlay(*d, live); err != nil {
			switch {
			case errors.Is(err, ErrConflict):
				out.Stage = "conflict"
			case errors.Is(err, market.ErrHasGroups):
				out.Stage = "supplier_has_groups"
			case errors.Is(err, ErrInvalid), errors.Is(err, market.ErrInvalid):
				out.Stage = "invalid"
			default:
				return err
			}
		}
		if r, ok := u.(impactReader); ok {
			out.LinkedChannels, err = r.AffectedChannels(ctx, d.Changes)
			if err != nil {
				return err
			}
		}
		if out.Stage == "" {
			out.Stage = "draft"
			if len(d.PendingPrices) == 0 {
				out.Stage = "ready"
			}
		}
		return nil
	})
	out.Draft = draft
	return out, err
}

type TargetChoice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
	Enabled  bool   `json:"enabled"`
}

func (s *Service) Choices(ctx context.Context, actor Actor, kind, after string, limit int, parentID string) ([]TargetChoice, bool, error) {
	if actor.ID == "" || limit < 1 || limit > 100 {
		return nil, false, ErrInvalid
	}
	d, err := s.Live.Snapshot(ctx)
	if err != nil {
		return nil, false, err
	}
	items := []TargetChoice{}
	switch kind {
	case "address":
		for _, v := range d.Addresses {
			if v.DeletedAt == nil && (parentID == "" || v.SupplierID == parentID) {
				name := v.BaseURL
				if v.Label != "" {
					name = v.Label + " · " + name
				}
				items = append(items, TargetChoice{ID: v.ID, Name: name, ParentID: v.SupplierID, Enabled: true})
			}
		}
	case "supplier":
		for _, v := range d.Suppliers {
			items = append(items, TargetChoice{ID: v.ID, Name: v.Name, Enabled: v.Enabled})
		}
	case "group":
		for _, v := range d.Groups {
			items = append(items, TargetChoice{ID: v.ID, Name: v.Name, ParentID: v.SupplierID, Enabled: v.Enabled})
		}
	case "group_model":
		for _, v := range d.Models {
			items = append(items, TargetChoice{ID: v.ID, Name: v.ModelID, ParentID: v.GroupID, Enabled: v.Enabled})
		}
	default:
		return nil, false, ErrInvalid
	}
	slices.SortFunc(items, func(a, b TargetChoice) int { return strings.Compare(a.ID, b.ID) })
	items = slices.DeleteFunc(items, func(v TargetChoice) bool { return v.ID <= after })
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	return items, more, nil
}
