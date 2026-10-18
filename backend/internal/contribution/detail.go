package contribution

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type Detail struct {
	Draft          Draft      `json:"draft"`
	Evaluation     Evaluation `json:"evaluation"`
	Stage          string     `json:"stage"`
	LinkedChannels int64      `json:"linked_channels"`
}
type impactReader interface {
	AffectedChannels(context.Context, market.Changes) (int64, error)
}

func (s *Service) Detail(ctx context.Context, actor Actor, id string) (Detail, error) {
	out := Detail{Evaluation: Evaluation{Targets: []TargetReview{}}}
	draft, err := s.Store.Edit(ctx, id, func(d *Draft, u UnitOfWork) error {
		if !authorized(actor, *d) {
			return ErrNotFound
		}
		if d.State == "approved" || d.State == "publication_pending" {
			out.Stage = d.State
			return nil
		}
		live, err := u.Directory(ctx)
		if err != nil {
			return err
		}
		out.Evaluation, err = Evaluate(ctx, actor, *d, live, u, s.Planner)
		if err != nil {
			if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrInvalid) && !errors.Is(err, market.ErrHasGroups) {
				return err
			}
			out.Stage = "invalid"
			if errors.Is(err, ErrConflict) {
				out.Stage = "conflict"
			}
			if errors.Is(err, market.ErrHasGroups) {
				out.Stage = "supplier_has_groups"
			}
		}
		if r, ok := u.(impactReader); ok {
			out.LinkedChannels, err = r.AffectedChannels(ctx, d.Changes)
			if err != nil {
				return err
			}
		}
		if out.Stage == "" {
			out.Stage = d.State
			if d.State == "draft" || d.State == "rejected" || d.State == "withdrawn" {
				out.Stage = "draft"
				if len(d.PendingPrices) == 0 && out.Evaluation.Ready {
					out.Stage = "ready"
				}
			}
		}
		return nil
	})
	if out.Evaluation.Targets == nil {
		out.Evaluation.Targets = []TargetReview{}
	}
	out.Draft = draft
	return out, err
}

// DraftResolver authorizes private proposals independently from published
// ownership. An explicit draft ID prevents two private edits of one live ID
// from accidentally measuring each other's configuration.
type DraftResolver struct {
	Service  *Service
	Fallback detection.Resolver
}

func (r DraftResolver) Resolve(ctx context.Context, actor detection.Actor, input detection.Target) (detection.Resolved, error) {
	if input.DraftID == "" {
		if r.Fallback == nil {
			return detection.Resolved{}, detection.ErrNotFound
		}
		return r.Fallback.Resolve(ctx, actor, input)
	}
	if r.Service == nil {
		return detection.Resolved{}, detection.ErrNotFound
	}
	d, err := r.Service.Get(ctx, Actor{ID: actor.ID, Admin: actor.Admin}, input.DraftID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			err = detection.ErrNotFound
		}
		return detection.Resolved{}, err
	}
	if !slices.Contains([]string{"draft", "submitted", "rejected", "withdrawn"}, d.State) {
		return detection.Resolved{}, detection.ErrConflict
	}
	live, err := r.Service.Live.Snapshot(ctx)
	if err != nil {
		return detection.Resolved{}, err
	}
	directory, targets, err := Overlay(d, live)
	if err != nil {
		return detection.Resolved{}, detection.ErrConflict
	}
	if !slices.ContainsFunc(targets, func(t detection.Target) bool {
		return t.Binding.TargetKind == input.Binding.TargetKind && t.Binding.TargetID == input.Binding.TargetID
	}) {
		return detection.Resolved{}, detection.ErrNotFound
	}
	return detection.ResolveDirectory(directory, input)
}
func (s *Service) TrackTask(ctx context.Context, actor Actor, job detection.Job) error {
	if job.Target.DraftID == "" {
		return nil
	}
	_, err := s.Store.Edit(ctx, job.Target.DraftID, func(d *Draft, u UnitOfWork) error {
		if !authorized(actor, *d) || (!actor.Admin && job.OwnerID != d.OwnerID) {
			return ErrNotFound
		}
		if !editable(actor, *d) {
			return ErrConflict
		}
		if slices.Contains(d.TaskIDs, job.ID) {
			return nil
		}
		if len(d.TaskIDs) >= 1024 {
			return ErrInvalid
		}
		d.TaskIDs = append(d.TaskIDs, job.ID)
		advance(d)
		return u.Audit(ctx, actor.ID, "detection_linked", map[string]any{"task_id": job.ID, "revision": d.Revision})
	})
	return err
}

type TargetChoice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
	Enabled  bool   `json:"enabled"`
}

func (s *Service) Choices(ctx context.Context, actor Actor, kind, after string, limit int) ([]TargetChoice, bool, error) {
	if actor.ID == "" || limit < 1 || limit > 100 {
		return nil, false, ErrInvalid
	}
	d, err := s.Live.Snapshot(ctx)
	if err != nil {
		return nil, false, err
	}
	items := []TargetChoice{}
	switch kind {
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
