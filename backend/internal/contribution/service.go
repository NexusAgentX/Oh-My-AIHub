package contribution

import (
	"context"
	"errors"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func authorized(actor Actor, draft Draft) bool {
	return actor.ID != "" && (actor.Admin || draft.OwnerID == actor.ID)
}
func editable(actor Actor, draft Draft) bool {
	return slices.Contains([]string{"draft", "rejected", "withdrawn"}, draft.State) || actor.Admin && draft.State == "submitted"
}
func (s *Service) Get(ctx context.Context, actor Actor, id string) (Draft, error) {
	draft, err := s.Store.Get(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	if !authorized(actor, draft) {
		return Draft{}, ErrNotFound
	}
	return draft, nil
}
func (s *Service) List(ctx context.Context, actor Actor, queue bool, cursor *Cursor, limit int) ([]Summary, error) {
	if actor.ID == "" || (queue && !actor.Admin) || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	return s.Store.List(ctx, actor, queue, cursor, limit)
}
func (s *Service) Save(ctx context.Context, actor Actor, id string, revision int64, title string, changes market.Changes, reports []string, declaredPrices []string, blanks ...map[string][]string) (Draft, error) {
	if title == "" || utf8.RuneCountInString(title) > 128 || len(reports) > 1024 {
		return Draft{}, ErrInvalid
	}
	return s.Store.Edit(ctx, id, func(draft *Draft, u UnitOfWork) error {
		if !authorized(actor, *draft) {
			return ErrNotFound
		}
		if draft.Revision != revision || !editable(actor, *draft) {
			return ErrConflict
		}
		if len(changes.Governance) > 0 && !actor.Admin {
			return ErrInvalid
		}
		if !sameIdentities(draft.Changes, changes) || !rootPresent(*draft, changes) {
			return ErrInvalid
		}
		for _, reportID := range reports {
			report, err := u.Report(ctx, reportID)
			if err != nil {
				return err
			}
			if !actor.Admin && report.OwnerID != draft.OwnerID {
				return ErrNotFound
			}
		}
		for i := range changes.Suppliers {
			if changes.Suppliers[i].BaseRevision == 0 {
				changes.Suppliers[i].Value.ContributorID = draft.OwnerID
			}
		}
		draft.Title = title
		draft.Changes = changes
		draft.ReportIDs = append([]string{}, reports...)
		priorBlanks := draft.PriceBlanks
		draft.PriceBlanks = map[string][]string{}
		draft.PendingPrices = []string{}
		for _, quote := range changes.Quotes {
			if len(blanks) > 0 {
				draft.PriceBlanks[quote.GroupModelID] = slices.Clone(blanks[0][quote.GroupModelID])
			} else if !slices.Contains(declaredPrices, quote.GroupModelID) {
				draft.PriceBlanks[quote.GroupModelID] = slices.Clone(priorBlanks[quote.GroupModelID])
				if len(draft.PriceBlanks[quote.GroupModelID]) == 0 {
					draft.PriceBlanks[quote.GroupModelID] = defaultPriceBlanks()
				}
			}
			if !slices.Contains(declaredPrices, quote.GroupModelID) {
				draft.PendingPrices = append(draft.PendingPrices, quote.GroupModelID)
			}
		}
		draft.State = "draft"
		draft.ReviewReason = ""
		draft.ReviewedBy = nil
		draft.SubmittedAt = nil
		advance(draft)
		return u.Audit(ctx, actor.ID, "edited", map[string]any{"revision": draft.Revision})
	})
}
func advance(draft *Draft) { draft.Revision++; draft.UpdatedAt = time.Now().UTC() }

func (s *Service) Submit(ctx context.Context, actor Actor, id string, revision int64) (Draft, error) {
	before, err := s.Get(ctx, actor, id)
	if err != nil {
		return Draft{}, err
	}
	if err = s.validateAddresses(ctx, before); err != nil {
		return Draft{}, err
	}
	return s.Store.Edit(ctx, id, func(draft *Draft, u UnitOfWork) error {
		if !authorized(actor, *draft) {
			return ErrNotFound
		}
		if draft.Revision != revision || !editable(actor, *draft) {
			return ErrConflict
		}
		if len(draft.PendingPrices) > 0 {
			return ErrInvalid
		}
		live, err := u.Directory(ctx)
		if err != nil {
			return err
		}
		evaluation, err := Evaluate(ctx, actor, *draft, live, u, s.Planner)
		if err != nil {
			return err
		}
		if !evaluation.Ready {
			return ErrReports
		}
		now := time.Now().UTC()
		draft.State = "submitted"
		draft.SubmittedAt = &now
		draft.ReviewReason = ""
		draft.ReviewedBy = nil
		advance(draft)
		return u.Audit(ctx, actor.ID, "submitted", map[string]any{"revision": draft.Revision, "report_count": len(evaluation.Reports)})
	})
}
func (s *Service) Reject(ctx context.Context, actor Actor, id string, revision int64, reason string) (Draft, error) {
	if actor.ID == "" || !actor.Admin || reason == "" || utf8.RuneCountInString(reason) > 1000 {
		return Draft{}, ErrInvalid
	}
	return s.Store.Edit(ctx, id, func(draft *Draft, u UnitOfWork) error {
		if draft.Revision != revision || draft.State != "submitted" {
			return ErrConflict
		}
		draft.State = "rejected"
		draft.ReviewReason = reason
		draft.ReviewedBy = &actor.ID
		advance(draft)
		return u.Audit(ctx, actor.ID, "rejected", map[string]any{"revision": draft.Revision, "reason": reason})
	})
}
func (s *Service) Withdraw(ctx context.Context, actor Actor, id string, revision int64) (Draft, error) {
	return s.Store.Edit(ctx, id, func(draft *Draft, u UnitOfWork) error {
		if !authorized(actor, *draft) {
			return ErrNotFound
		}
		if draft.Revision != revision || !slices.Contains([]string{"draft", "submitted", "rejected"}, draft.State) {
			return ErrConflict
		}
		draft.State = "withdrawn"
		advance(draft)
		return u.Audit(ctx, actor.ID, "withdrawn", map[string]any{"revision": draft.Revision})
	})
}
func (s *Service) Approve(ctx context.Context, actor Actor, id string, revision int64, reason string) (Draft, error) {
	if actor.ID == "" || !actor.Admin || utf8.RuneCountInString(reason) > 1000 || s.Publisher == nil {
		return Draft{}, ErrInvalid
	}
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	before, err := s.Get(ctx, actor, id)
	if err != nil {
		return Draft{}, err
	}
	if before.Revision != revision {
		return Draft{}, ErrConflict
	}
	if before.State == "approved" {
		return before, nil
	}
	if before.State != "publication_pending" {
		if err = s.validateAddresses(ctx, before); err != nil {
			return Draft{}, err
		}
	}
	s.Publisher.Block()
	published, err := s.Store.Edit(ctx, id, func(draft *Draft, u UnitOfWork) error {
		if draft.Revision != revision {
			return ErrConflict
		}
		if draft.State == "publication_pending" {
			return nil
		}
		if draft.State != "submitted" {
			return ErrConflict
		}
		if len(draft.PendingPrices) > 0 {
			return ErrInvalid
		}
		live, err := u.Directory(ctx)
		if err != nil {
			return err
		}
		evaluation, err := Evaluate(ctx, actor, *draft, live, u, s.Planner)
		if err != nil {
			return err
		}
		if !evaluation.Ready {
			return ErrReports
		}
		for i := range draft.Changes.Suppliers {
			if draft.Changes.Suppliers[i].BaseRevision == 0 {
				draft.Changes.Suppliers[i].Value.ContributorID = draft.OwnerID
			}
		}
		if err = u.Publish(ctx, actor.ID, draft.Changes); err != nil {
			return err
		}
		for _, report := range evaluation.Reports {
			if err = u.RecordEvidence(ctx, report); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		draft.State = "publication_pending"
		draft.PublishedAt = &now
		draft.ReviewedBy = &actor.ID
		draft.ReviewReason = reason
		advance(draft)
		return u.Audit(ctx, actor.ID, "approved", map[string]any{"revision": draft.Revision, "reason": reason, "report_count": len(evaluation.Reports)})
	})
	if err != nil {
		if !errors.Is(err, ErrCommitUncertain) {
			s.Publisher.Restore()
			return Draft{}, err
		}
		// Resolve a lost COMMIT acknowledgement under a fresh row lock. A plain
		// read could still see the old version while the first COMMIT finishes.
		resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		confirmed, resolveErr := s.Store.Edit(resolveCtx, id, func(*Draft, UnitOfWork) error { return nil })
		cancel()
		if resolveErr != nil {
			return before, ErrCommitUncertain
		}
		if confirmed.State != "publication_pending" && confirmed.State != "approved" {
			s.Publisher.Restore()
			return confirmed, err
		}
		published = confirmed
	}
	// Cancellation cannot turn a committed publication into a claimed rollback.
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err = s.Publisher.Refresh(refreshCtx); err != nil {
		return published, ErrPublication
	}
	finished, err := s.Store.Edit(refreshCtx, id, func(draft *Draft, u UnitOfWork) error {
		if draft.State == "approved" {
			return nil
		}
		if draft.State != "publication_pending" {
			return ErrConflict
		}
		draft.State = "approved"
		advance(draft)
		return u.Audit(refreshCtx, actor.ID, "runtime_ready", map[string]any{"revision": draft.Revision})
	})
	if err != nil {
		return published, ErrPublication
	}
	return finished, nil
}
func (s *Service) validateAddresses(ctx context.Context, draft Draft) error {
	for _, change := range draft.Changes.Addresses {
		if change.Value.DeletedAt != nil {
			continue
		}
		unchanged := false
		for _, base := range draft.Base.Addresses {
			unchanged = unchanged || base.Value.ID == change.Value.ID && base.Value.BaseURL == change.Value.BaseURL && base.Value.SupplierID == change.Value.SupplierID
		}
		if unchanged {
			continue
		}
		if s.Outbound == nil {
			return ErrInvalid
		}
		if _, err := s.Outbound.ValidateBaseURL(ctx, change.Value.BaseURL); err != nil {
			return market.ErrInvalid
		}
	}
	return nil
}

// Additions are allocated through New/AddChild. Editing cannot inject a
// foreign object or replace its base revision behind the review.
func sameIdentities(old, next market.Changes) bool {
	known := map[string]int64{}
	for _, v := range old.Governance {
		known["rule:"+v.Value.ModelID] = v.BaseRevision
	}
	for _, v := range next.Governance {
		if base, ok := known["rule:"+v.Value.ModelID]; !ok || base != v.BaseRevision {
			return false
		}
	}
	for _, v := range old.Suppliers {
		known["supplier:"+v.Value.ID] = v.BaseRevision
	}
	for _, v := range old.Groups {
		known["group:"+v.Value.ID] = v.BaseRevision
	}
	for _, v := range old.Addresses {
		known["address:"+v.Value.ID] = v.BaseRevision
	}
	for _, v := range old.Models {
		known["model:"+v.Value.ID] = v.BaseRevision
	}
	for _, v := range next.Suppliers {
		if base, ok := known["supplier:"+v.Value.ID]; !ok || base != v.BaseRevision {
			return false
		}
	}
	for _, v := range next.Groups {
		if base, ok := known["group:"+v.Value.ID]; !ok || base != v.BaseRevision {
			return false
		}
	}
	for _, v := range next.Addresses {
		if base, ok := known["address:"+v.Value.ID]; !ok || base != v.BaseRevision {
			return false
		}
	}
	for _, v := range next.Models {
		if base, ok := known["model:"+v.Value.ID]; !ok || base != v.BaseRevision {
			return false
		}
	}
	quotes := map[string]string{}
	for _, v := range old.Quotes {
		quotes[v.ID] = v.GroupModelID
	}
	for _, v := range next.Quotes {
		if parent, ok := quotes[v.ID]; !ok || parent != v.GroupModelID {
			return false
		}
	}
	links := map[string]string{}
	for _, set := range old.GroupAddresses {
		for _, link := range set.Links {
			links[link.ID] = set.GroupID
		}
	}
	for _, set := range next.GroupAddresses {
		for _, link := range set.Links {
			if parent, ok := links[link.ID]; !ok || parent != set.GroupID {
				return false
			}
		}
	}
	return true
}

func rootPresent(draft Draft, c market.Changes) bool {
	switch draft.Kind {
	case "model_id":
		for _, change := range c.Governance {
			if change.Value.ModelID == draft.TargetID {
				return true
			}
		}
	case "supplier":
		for _, change := range c.Suppliers {
			if change.Value.ID == draft.TargetID {
				return true
			}
		}
	case "group":
		for _, change := range c.Groups {
			if change.Value.ID == draft.TargetID {
				return true
			}
		}
	case "group_model":
		for _, change := range c.Models {
			if change.Value.ID == draft.TargetID {
				return true
			}
		}
	}
	return false
}

func newDraft(actor Actor, kind, targetID, title string, c market.Changes) (Draft, error) {
	if actor.ID == "" || !slices.Contains([]string{"supplier", "group", "group_model", "model_id"}, kind) || targetID == "" || title == "" {
		return Draft{}, ErrInvalid
	}
	id, err := ids.NewUUID()
	if err != nil {
		return Draft{}, err
	}
	now := time.Now().UTC()
	return Draft{ID: id, OwnerID: actor.ID, Title: title, Kind: kind, TargetID: targetID, State: "draft", Revision: 1, Changes: c, ReportIDs: []string{}, TaskIDs: []string{}, CreatedAt: now, UpdatedAt: now}, nil
}

// Recover completes only already committed publications after a restart. It
// never approves a submitted proposal or replays market changes.
func (s *Service) Recover(ctx context.Context) error {
	var cursor *Cursor
	for {
		items, err := s.Store.List(ctx, Actor{ID: "00000000-0000-0000-0000-000000000000", Admin: true}, true, cursor, 100)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.State != "publication_pending" {
				continue
			}
			d, err := s.Store.Get(ctx, item.ID)
			if err != nil {
				return err
			}
			if d.ReviewedBy == nil {
				return ErrInvalid
			}
			if _, err = s.Approve(ctx, Actor{ID: *d.ReviewedBy, Admin: true}, d.ID, d.Revision, d.ReviewReason); err != nil {
				return err
			}
		}
		if len(items) < 100 {
			return nil
		}
		last := items[len(items)-1]
		cursor = &Cursor{At: last.CreatedAt, ID: last.ID}
	}
}

func defaultPriceBlanks() []string {
	return []string{"/prices/input", "/prices/output", "/prices/cache_write", "/prices/cache_read"}
}
