package groupchannel

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type Service struct {
	Repository Repository
	Runtime    *Runtime
	Keyring    *channel.Keyring
}

func (s *Service) Events(ctx context.Context, a Actor, id string) ([]channel.Event, error) {
	if _, err := s.owned(ctx, a, id); err != nil {
		return nil, err
	}
	return s.Repository.Events(ctx, id)
}

func discountString(n int64) string { return money.Amount(n).String() }
func ParseDiscount(s string) (int64, error) {
	n, err := money.Parse(s)
	if err != nil || n < 0 || n > 1000*money.Amount(money.Scale) {
		return 0, ErrInvalid
	}
	return int64(n), nil
}
func validText(s string, max int) bool {
	return utf8.RuneCountInString(s) <= max && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}
func validKey(s string) bool {
	return len(s) > 0 && len(s) <= 4096 && !strings.ContainsFunc(s, unicode.IsControl)
}
func (s *Service) state() (*State, error) {
	if s.Runtime == nil {
		return nil, ErrUnavailable
	}
	return s.Runtime.Current()
}
func (s *Service) owned(ctx context.Context, a Actor, id string) (Record, error) {
	if a.ID == "" {
		return Record{}, ErrNotFound
	}
	if _, err := s.state(); err != nil {
		return Record{}, err
	}
	r, err := s.Repository.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if !a.Admin && r.Owner.ID != a.ID {
		return Record{}, ErrNotFound
	}
	return r, nil
}
func (s *Service) Get(ctx context.Context, a Actor, id string) (View, error) {
	if _, err := s.owned(ctx, a, id); err != nil {
		return View{}, err
	}
	state, err := s.state()
	if err != nil {
		return View{}, err
	}
	v, ok := state.Views[id]
	if !ok {
		return View{}, ErrUnavailable
	}
	return v, nil
}
func (s *Service) List(_ context.Context, a Actor) ([]View, error) {
	state, err := s.state()
	if err != nil {
		return nil, err
	}
	out := []View{}
	for _, r := range state.Data.Channels {
		if a.Admin || r.Owner.ID == a.ID {
			out = append(out, state.Views[r.ID])
		}
	}
	return out, nil
}
func group(d market.Directory, id string) (market.Group, error) {
	for _, g := range d.Groups {
		if g.ID == id && g.Enabled && g.DeletedAt == nil {
			for _, p := range d.Suppliers {
				if p.ID == g.SupplierID && p.Enabled && p.DeletedAt == nil {
					return g, nil
				}
			}
		}
	}
	return market.Group{}, ErrInvalid
}
func validateChoices(d market.Directory, gid string, c []Choice) error {
	if len(c) > 4096 {
		return ErrInvalid
	}
	models := map[string]bool{}
	for _, m := range d.Models {
		if m.GroupID == gid && m.DeletedAt == nil {
			models[m.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, v := range c {
		if !models[v.GroupModelID] || seen[v.GroupModelID] || v.DiscountNano < 0 || v.DiscountNano > 1_000_000_000_000 {
			return ErrInvalid
		}
		seen[v.GroupModelID] = true
	}
	return nil
}
func validateRecord(d market.Directory, r Record) error {
	if strings.TrimSpace(r.Name) == "" || !validText(r.Name, 64) || strings.ContainsFunc(r.Name, unicode.IsControl) || !validText(r.Summary, 160) || !validText(r.Description, 8000) || market.ValidateDefaults(r.Overrides) != nil || len(r.AddressIDs) == 0 || len(r.AddressIDs) > 32 {
		return ErrInvalid
	}
	allowed := map[string]bool{}
	for _, a := range addresses(d, r.GroupID) {
		allowed[a.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range r.AddressIDs {
		if !allowed[id] || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	return nil
}
func (s *Service) Create(ctx context.Context, a Actor, in CreateInput) (View, error) {
	if a.ID == "" {
		return View{}, ErrNotFound
	}
	state, err := s.state()
	if err != nil {
		return View{}, err
	}
	g, err := group(state.Directory, in.GroupID)
	if err != nil {
		return View{}, err
	}
	key := strings.TrimSpace(in.APIKey)
	if !validKey(key) {
		return View{}, ErrInvalid
	}
	id, err := ids.NewUUID()
	if err != nil {
		return View{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = g.Name
	}
	r := Record{ID: id, Owner: channel.Owner{ID: a.ID}, OwnerActive: true, Name: name, Summary: strings.TrimSpace(in.Summary), Description: strings.TrimSpace(in.Description), GroupID: g.ID, Status: channel.StatusListed, AddressIDs: slices.Clone(in.AddressIDs), Overrides: in.Overrides, Revision: 1, CredentialRevision: 1, ProbeEpoch: 1}
	all := addresses(state.Directory, g.ID)
	if in.AddressIDs == nil && len(all) > 0 {
		r.AddressIDs = []string{all[0].ID}
	}
	if err = validateRecord(state.Directory, r); err != nil {
		return View{}, err
	}
	if err = validateChoices(state.Directory, g.ID, in.Models); err != nil {
		return View{}, err
	}
	r.Credential, err = s.Keyring.Encrypt(id, 1, key)
	if err != nil {
		return View{}, err
	}
	if err = s.Repository.Create(ctx, r, in.Models); err != nil {
		return View{}, err
	}
	if err = s.Runtime.Refresh(ctx); err != nil {
		return View{}, err
	}
	return s.Get(ctx, a, id)
}
func (s *Service) Update(ctx context.Context, a Actor, id string, in UpdateInput) (View, error) {
	r, err := s.owned(ctx, a, id)
	if err != nil {
		return View{}, err
	}
	if in.Revision != r.Revision {
		return View{}, ErrConflict
	}
	state, err := s.state()
	if err != nil {
		return View{}, err
	}
	m := Mutation{ExpectedRevision: r.Revision}
	if in.GroupID != nil && *in.GroupID != r.GroupID {
		// Switching suppliers requires an explicitly supplied credential, preventing
		// an old supplier's secret from being sent to a different engine by default.
		if in.APIKey == nil {
			return View{}, ErrInvalid
		}
		r.GroupID = *in.GroupID
		m.ClearChoices = true
		r.ProbeEpoch++
		if in.Models == nil {
			empty := []Choice{}
			in.Models = &empty
		}
		if in.AddressIDs == nil {
			all := addresses(state.Directory, r.GroupID)
			ids := []string{}
			if len(all) > 0 {
				ids = append(ids, all[0].ID)
			}
			in.AddressIDs = &ids
		}
	}
	if _, err = group(state.Directory, r.GroupID); err != nil {
		return View{}, err
	}
	if in.Name != nil {
		r.Name = strings.TrimSpace(*in.Name)
	}
	if in.Summary != nil {
		r.Summary = strings.TrimSpace(*in.Summary)
	}
	if in.Description != nil {
		r.Description = strings.TrimSpace(*in.Description)
	}
	if in.Status != nil {
		if *in.Status != channel.StatusListed && *in.Status != channel.StatusUnlisted {
			return View{}, ErrInvalid
		}
		if r.Status == channel.StatusSuspended {
			return View{}, ErrSuspended
		}
		r.Status = *in.Status
	}
	if in.AddressIDs != nil {
		r.AddressIDs = slices.Clone(*in.AddressIDs)
	}
	if in.Overrides != nil {
		r.Overrides = *in.Overrides
	}
	if in.APIKey != nil {
		key := strings.TrimSpace(*in.APIKey)
		if !validKey(key) {
			return View{}, ErrInvalid
		}
		r.Credential, err = s.Keyring.Encrypt(r.ID, 1, key)
		if err != nil {
			return View{}, err
		}
		r.CredentialRevision++
		m.ReplaceCredential = true
	}
	if err = validateRecord(state.Directory, r); err != nil {
		return View{}, err
	}
	if in.Models != nil {
		if err = validateChoices(state.Directory, r.GroupID, *in.Models); err != nil {
			return View{}, err
		}
		m.ReplaceChoices = true
		m.ClearChoices = m.ClearChoices || len(*in.Models) == 0
		m.Choices = *in.Models
	}
	m.Record = r
	if err = s.Repository.Update(ctx, m); err != nil {
		return View{}, err
	}
	if m.ReplaceCredential {
		s.Keyring.ForgetCredential(id)
	}
	if err = s.Runtime.Refresh(ctx); err != nil {
		return View{}, err
	}
	return s.Get(ctx, a, id)
}
func (s *Service) Delete(ctx context.Context, a Actor, id string, revision int64) error {
	r, err := s.owned(ctx, a, id)
	if err != nil {
		return err
	}
	if r.Revision != revision {
		return ErrConflict
	}
	if err = s.Repository.Delete(ctx, id, revision); err != nil {
		return err
	}
	s.Keyring.ForgetCredential(id)
	return s.Runtime.Refresh(ctx)
}
func (s *Service) Moderate(ctx context.Context, a Actor, id, reason string) error {
	if !a.Admin {
		return ErrNotFound
	}
	if !validText(reason, 1000) {
		return ErrInvalid
	}
	if _, err := s.owned(ctx, a, id); err != nil {
		return err
	}
	if err := s.Repository.Moderate(ctx, a.ID, id, strings.TrimSpace(reason)); err != nil {
		return err
	}
	return s.Runtime.Refresh(ctx)
}
func (s *Service) Retest(ctx context.Context, a Actor, id string, revision int64) (View, error) {
	r, err := s.owned(ctx, a, id)
	if err != nil {
		return View{}, err
	}
	if revision != r.Revision {
		return View{}, ErrConflict
	}
	r.ProbeEpoch++
	if err = s.Repository.Update(ctx, Mutation{Record: r, ExpectedRevision: revision}); err != nil {
		return View{}, err
	}
	if err = s.Runtime.Refresh(ctx); err != nil {
		return View{}, err
	}
	return s.Get(ctx, a, id)
}

// Writes have a finite database/rebuild deadline; this is not a cumulative
// upstream detection budget and does not cap a channel's total probe work.
func WriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 20*time.Second)
}
