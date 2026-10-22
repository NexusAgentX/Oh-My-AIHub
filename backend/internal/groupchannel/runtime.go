package groupchannel

import (
	"context"
	"slices"
	"sort"
	"sync/atomic"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

type State struct {
	Directory market.Directory
	Data      Data
	Views     map[string]View
	// Private immutable inputs for account-scoped catalog projections. These
	// records contain credentials and must never be serialized as an API DTO.
	Candidates []gateway.CandidateRecord
	Version    string
}

// ModelState authorizes management of an approved ID before it has any online
// channels. A disabled exact-ID rule also prevents a legacy-catalog fallback.
func (s *State) ModelState(id string) (known, enabled bool) {
	for _, v := range s.Directory.Governance {
		if v.ModelID == id && v.Disabled {
			return true, false
		}
	}
	groups := map[string]market.Group{}
	suppliers := map[string]bool{}
	for _, v := range s.Directory.Groups {
		groups[v.ID] = v
	}
	for _, v := range s.Directory.Suppliers {
		suppliers[v.ID] = v.Enabled && v.DeletedAt == nil
	}
	for _, v := range s.Directory.Models {
		if v.ModelID != id {
			continue
		}
		known = true
		g := groups[v.GroupID]
		enabled = enabled || (v.Enabled && v.DeletedAt == nil && g.ID != "" && g.Enabled && g.DeletedAt == nil && suppliers[g.SupplierID])
	}
	return known, enabled
}

type Runtime struct {
	Repository      Repository
	CredentialScope func(Record) (string, error)
	Removed         func(string)
	Edited          func(string)
	// Ready is the contribution publication barrier; called before management
	// and inference reads. It does no I/O and must not call back into this loader.
	Ready     func() error
	Rebuild   func(context.Context) error
	Notify    func()
	directory atomic.Pointer[market.Directory]
	state     atomic.Pointer[State]
}

func (r *Runtime) Check() error {
	if r.Ready != nil {
		return r.Ready()
	}
	return nil
}
func (r *Runtime) Current() (*State, error) {
	if err := r.Check(); err != nil {
		return nil, ErrUnavailable
	}
	s := r.state.Load()
	if s == nil {
		return nil, ErrUnavailable
	}
	return s, nil
}
func (r *Runtime) Install(ctx context.Context, d market.Directory) error {
	r.directory.Store(&d)
	if r.Rebuild != nil {
		if err := r.Rebuild(ctx); err != nil {
			return err
		}
	}
	if r.Notify != nil {
		r.Notify()
	}
	return nil
}
func (r *Runtime) Refresh(ctx context.Context) error {
	if r.Rebuild == nil {
		return ErrUnavailable
	}
	err := r.Rebuild(ctx)
	if err == nil && r.Notify != nil {
		r.Notify()
	}
	return err
}
func (r *Runtime) Wrap(base snapshot.Loader) snapshot.Loader { return &loader{Loader: base, r: r} }

type loader struct {
	snapshot.Loader
	r *Runtime
}

func (l *loader) LoadCandidates(ctx context.Context) ([]gateway.CandidateRecord, error) {
	d := l.r.directory.Load()
	if d == nil {
		return []gateway.CandidateRecord{}, nil
	}
	data, err := l.r.Repository.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	records, views, err := build(*d, data)
	if err != nil {
		return nil, err
	}
	if l.r.CredentialScope != nil {
		byID := map[string]Record{}
		for _, r := range data.Channels {
			byID[r.ID] = r
		}
		scopes := map[string]string{}
		for i := range records {
			cid := records[i].Candidate.ChannelID
			scope, ok := scopes[cid]
			if !ok {
				scope, err = l.r.CredentialScope(byID[cid])
				if err != nil {
					return nil, err
				}
				scopes[cid] = scope
			}
			records[i].Candidate.CredentialScope = scope
		}
	}
	if previous := l.r.state.Load(); previous != nil && l.r.Removed != nil {
		present := map[string]bool{}
		for _, r := range data.Channels {
			present[r.ID] = true
		}
		for _, r := range previous.Data.Channels {
			if !present[r.ID] {
				l.r.Removed(r.ID)
			}
		}
	}
	if previous := l.r.state.Load(); previous != nil && l.r.Edited != nil {
		old := map[string]Record{}
		for _, r := range previous.Data.Channels {
			old[r.ID] = r
		}
		for _, r := range data.Channels {
			before, ok := old[r.ID]
			if ok && (!before.UpdatedAt.Equal(r.UpdatedAt) || previous.Views[r.ID].GroupRevision != views[r.ID].GroupRevision) {
				l.r.Edited(r.ID)
			}
		}
	}
	version, err := ids.NewUUID()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].ModelID != records[j].ModelID {
			return records[i].ModelID < records[j].ModelID
		}
		return records[i].Candidate.ChannelID < records[j].Candidate.ChannelID
	})
	l.r.state.Store(&State{Directory: *d, Data: data, Views: views, Candidates: records, Version: version})
	return records, nil
}
func (l *loader) LoadModels(ctx context.Context) ([]catalog.Model, error) {
	models := []catalog.Model{}
	d := l.r.directory.Load()
	if d == nil {
		return models, nil
	}
	seen := map[string]int{}
	enabled := map[string]bool{}
	for _, s := range d.Suppliers {
		enabled[s.ID] = s.Enabled && s.DeletedAt == nil
	}
	groups := map[string]bool{}
	for _, g := range d.Groups {
		groups[g.ID] = g.Enabled && g.DeletedAt == nil && enabled[g.SupplierID]
	}
	blocked := map[string]bool{}
	for _, g := range d.Governance {
		blocked[g.ModelID] = g.Disabled
	}
	// These are an aggregate query/cache, never a model entity or price source.
	aggregate := map[string]catalog.Model{}
	for _, m := range d.Models {
		if m.DeletedAt != nil {
			continue
		}
		v := aggregate[m.ModelID]
		v.ID = m.ModelID
		v.DisplayName = m.ModelID
		v.Enabled = v.Enabled || m.Enabled && groups[m.GroupID] && !blocked[m.ModelID]
		aggregate[m.ModelID] = v
	}
	for id, m := range aggregate {
		if i, ok := seen[id]; ok {
			models[i].Enabled = (models[i].Enabled || m.Enabled) && !blocked[id]
		} else {
			models = append(models, m)
			seen[id] = len(models) - 1
		}
	}
	for id, disabled := range blocked {
		if !disabled {
			continue
		}
		if i, ok := seen[id]; ok {
			models[i].Enabled = false
		} else {
			models = append(models, catalog.Model{ID: id, DisplayName: id, Enabled: false})
		}
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
func addresses(d market.Directory, group string) []AddressView {
	links := []market.GroupAddress{}
	for _, a := range d.GroupAddresses {
		if a.GroupID == group && a.DeletedAt == nil {
			links = append(links, a)
		}
	}
	sort.SliceStable(links, func(i, j int) bool {
		if links[i].Position != links[j].Position {
			return links[i].Position < links[j].Position
		}
		return links[i].ID < links[j].ID
	})
	byID := map[string]market.Address{}
	for _, a := range d.Addresses {
		if a.DeletedAt == nil {
			byID[a.ID] = a
		}
	}
	out := []AddressView{}
	for _, l := range links {
		if a, ok := byID[l.AddressID]; ok {
			out = append(out, AddressView{ID: l.ID, Label: a.Label, URL: a.BaseURL})
		}
	}
	return out
}
func selected(d market.Directory, r Record) []gateway.Entrance {
	all := addresses(d, r.GroupID)
	byID := map[string]AddressView{}
	for _, a := range all {
		byID[a.ID] = a
	}
	out := []gateway.Entrance{}
	for _, id := range r.AddressIDs {
		if a, ok := byID[id]; ok {
			out = append(out, gateway.Entrance{ID: id, BaseURL: a.URL})
		}
	}
	return out
}
func Build(d market.Directory, data Data) ([]gateway.CandidateRecord, map[string]View, error) {
	return build(d, data)
}
func build(d market.Directory, data Data) ([]gateway.CandidateRecord, map[string]View, error) {
	pricingVersions, err := typical.PricingVersions(d)
	if err != nil {
		return nil, nil, err
	}
	groups := map[string]market.Group{}
	suppliers := map[string]market.Supplier{}
	quotes := map[string]market.Quote{}
	blocked := map[string]bool{}
	models := map[string][]market.GroupModel{}
	choices := map[string]Choice{}
	probes := map[string]Probe{}
	for _, g := range d.Groups {
		groups[g.ID] = g
	}
	for _, s := range d.Suppliers {
		suppliers[s.ID] = s
	}
	for _, q := range d.Quotes {
		quotes[q.ID] = q
	}
	for _, m := range d.Models {
		if m.DeletedAt == nil {
			models[m.GroupID] = append(models[m.GroupID], m)
		}
	}
	for _, g := range d.Governance {
		blocked[g.ModelID] = g.Disabled
	}
	for _, c := range data.Choices {
		choices[c.ChannelID+"/"+c.GroupModelID] = c
	}
	for _, p := range data.Probes {
		probes[p.ChannelID+"/"+p.GroupModelID] = p
	}
	records := []gateway.CandidateRecord{}
	views := map[string]View{}
	for _, r := range data.Channels {
		g := groups[r.GroupID]
		s := suppliers[g.SupplierID]
		effective, sources := ResolveDefaults(r.Overrides, g.Defaults)
		entries := selected(d, r)
		v := View{ID: r.ID, Owner: OwnerView{ID: r.Owner.ID, Username: r.Owner.Username, DisplayName: r.Owner.DisplayName}, Name: r.Name, Summary: r.Summary, Description: r.Description, Status: r.Status, SuspendedReason: r.SuspendedReason, GroupID: r.GroupID, GroupName: g.Name, SupplierName: s.Name, Revision: r.Revision, GroupRevision: g.Revision, Addresses: addresses(d, r.GroupID), SelectedAddressIDs: slices.Clone(r.AddressIDs), Overrides: r.Overrides, Effective: effective, Sources: sources, Models: []ModelView{}, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
		v.Available = g.ID != "" && g.Enabled && g.DeletedAt == nil && s.ID != "" && s.Enabled && s.DeletedAt == nil && r.OwnerActive && r.Status == channel.StatusListed && len(entries) > 0
		if !v.Available {
			v.Reason = "group_channel_unavailable"
		}
		if len(entries) == 0 {
			v.Reason = "no_selected_address"
		}
		book := quotepricing.Book{GroupID: g.ID, GroupRevision: g.Revision, ConnectionVersion: g.ConnectionVersion, Offers: map[string]quotepricing.Offer{}}
		for _, m := range models[g.ID] {
			if m.QuoteID == nil {
				continue
			}
			if q, ok := quotes[*m.QuoteID]; ok && q.GroupModelID == m.ID {
				choice, ok := choices[r.ID+"/"+m.ID]
				discount := int64(ledger.UnitRate)
				if ok {
					discount = choice.DiscountNano
				}
				book.Offers[m.ModelID] = quotepricing.Offer{ModelID: m.ModelID, UpstreamModel: m.UpstreamModel, Revision: m.Revision, Quote: q, DiscountNano: &discount}
			}
		}
		var frozen *quotepricing.Book
		var err error
		if len(book.Offers) > 0 {
			frozen, err = quotepricing.Freeze(book)
			if err != nil {
				return nil, nil, err
			}
		}
		for _, m := range models[g.ID] {
			c, ok := choices[r.ID+"/"+m.ID]
			if !ok {
				c = Choice{DiscountNano: ledger.UnitRate}
			}
			mv := ModelView{GroupModelID: m.ID, ModelID: m.ModelID, DisplayName: m.DisplayName, ModelType: m.ModelType, Excluded: c.Excluded, Discount: discountString(c.DiscountNano), ProbeState: "pending", Formats: []channel.Format{}, FormatTests: map[channel.Format]channel.FormatTest{}, QuoteID: m.QuoteID, Enabled: m.Enabled && !blocked[m.ModelID]}
			p, found := probes[r.ID+"/"+m.ID]
			if found && p.Binding == ProbeBinding(r, g, m) {
				mv.ProbeState = "unavailable"
				mv.FormatTests = p.Tests
				for f, t := range p.Tests {
					if t.OK {
						mv.Formats = append(mv.Formats, f)
					}
				}
				slices.Sort(mv.Formats)
				if len(mv.Formats) > 0 {
					mv.ProbeState = "available"
				}
			}
			v.Models = append(v.Models, mv)
			if !v.Available || !mv.Enabled || mv.Excluded || len(mv.Formats) == 0 || frozen == nil {
				continue
			}
			if _, ok := frozen.Offers[m.ModelID]; !ok {
				continue
			}
			records = append(records, gateway.CandidateRecord{ModelID: m.ModelID, Formats: mv.Formats, Candidate: gateway.Candidate{GroupID: g.ID, GroupModelID: m.ID, ModelType: m.ModelType, PricingVersion: pricingVersions[g.ID], ConfigRevision: r.Revision, CredentialRevision: r.CredentialRevision, Entrances: entries, Quotes: frozen, ChannelID: r.ID, OwnerID: r.Owner.ID, Name: r.Name, BaseURL: entries[0].BaseURL, UpstreamModel: m.UpstreamModel, MultiplierNano: c.DiscountNano, Advanced: Advanced(effective), Credential: r.Credential}})
		}
		sort.Slice(v.Models, func(i, j int) bool { return v.Models[i].ModelID < v.Models[j].ModelID })
		if v.Available && !slices.ContainsFunc(v.Models, func(m ModelView) bool {
			return m.Enabled && !m.Excluded && m.ProbeState == "available" && m.QuoteID != nil
		}) {
			v.Available = false
			v.Reason = "no_usable_models"
		}
		views[r.ID] = v
	}
	return records, views, nil
}
