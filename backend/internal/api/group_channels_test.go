package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
)

type apiGroupChannels struct {
	groupchannel.Repository
	mu      sync.Mutex
	rows    map[string]groupchannel.Record
	choices map[string][]groupchannel.Choice
	probes  []groupchannel.Probe
}

func (s *apiGroupChannels) Events(context.Context, string) ([]channel.Event, error) {
	return []channel.Event{}, nil
}

func (s *apiGroupChannels) Snapshot(context.Context) (groupchannel.Data, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := groupchannel.Data{Probes: reviewClone(s.probes)}
	for id, r := range s.rows {
		out.Channels = append(out.Channels, reviewClone(r))
		out.Choices = append(out.Choices, s.choices[id]...)
	}
	return out, nil
}
func (s *apiGroupChannels) SaveProbe(_ context.Context, r groupchannel.Record, g market.Group, m market.GroupModel, p groupchannel.Probe) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.rows[r.ID]
	if !ok || groupchannel.ProbeBinding(current, g, m) != p.Binding {
		return false, nil
	}
	next := []groupchannel.Probe{}
	for _, old := range s.probes {
		if old.ChannelID != r.ID || old.GroupModelID != m.ID {
			next = append(next, old)
		}
	}
	s.probes = append(next, reviewClone(p))
	return true, nil
}

func (s *apiGroupChannels) Get(_ context.Context, id string) (groupchannel.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return r, groupchannel.ErrNotFound
	}
	return reviewClone(r), nil
}
func (s *apiGroupChannels) Create(_ context.Context, r groupchannel.Record, c []groupchannel.Choice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.CreatedAt = time.Now().UTC()
	r.UpdatedAt = r.CreatedAt
	r.Owner.Username = "synthetic"
	r.Owner.DisplayName = "合成成员"
	s.rows[r.ID] = reviewClone(r)
	for i := range c {
		c[i].ChannelID = r.ID
	}
	s.choices[r.ID] = c
	return nil
}
func (s *apiGroupChannels) Update(_ context.Context, m groupchannel.Mutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.rows[m.Record.ID]
	if !ok {
		return groupchannel.ErrNotFound
	}
	if old.Revision != m.ExpectedRevision {
		return groupchannel.ErrConflict
	}
	m.Record.Revision++
	m.Record.UpdatedAt = time.Now().UTC()
	s.rows[m.Record.ID] = reviewClone(m.Record)
	if m.ReplaceChoices {
		if m.ClearChoices {
			s.choices[m.Record.ID] = nil
		}
		for _, c := range m.Choices {
			next := []groupchannel.Choice{}
			for _, old := range s.choices[m.Record.ID] {
				if old.GroupModelID != c.GroupModelID {
					next = append(next, old)
				}
			}
			c.ChannelID = m.Record.ID
			s.choices[m.Record.ID] = append(next, c)
		}
	}
	return nil
}
func (s *apiGroupChannels) Delete(_ context.Context, id string, revision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return groupchannel.ErrNotFound
	}
	if r.Revision != revision {
		return groupchannel.ErrConflict
	}
	delete(s.rows, id)
	delete(s.choices, id)
	return nil
}
func (s *apiGroupChannels) Moderate(_ context.Context, actor, id, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return groupchannel.ErrNotFound
	}
	if reason == "" {
		r.Status = "listed"
		r.SuspendedReason = nil
	} else {
		r.Status = "suspended"
		r.SuspendedReason = &reason
	}
	s.rows[id] = r
	return nil
}

type groupBaseLoader struct{ snapshot.Loader }

func (groupBaseLoader) LoadCandidates(context.Context) ([]gateway.CandidateRecord, error) {
	return nil, nil
}

func TestGroupChannelAPIInheritanceChoicesACLAndContract(t *testing.T) {
	repo := &apiGroupChannels{rows: map[string]groupchannel.Record{}, choices: map[string][]groupchannel.Choice{}}
	runtime := &groupchannel.Runtime{Repository: repo}
	runtime.Rebuild = func(ctx context.Context) error {
		_, err := runtime.Wrap(groupBaseLoader{}).LoadCandidates(ctx)
		return err
	}
	sid, gid, aid, link, mid, qid := "00000000-0000-4000-8000-000000000071", "00000000-0000-4000-8000-000000000072", "00000000-0000-4000-8000-000000000073", "00000000-0000-4000-8000-000000000074", "00000000-0000-4000-8000-000000000075", "00000000-0000-4000-8000-000000000076"
	d := market.Directory{Suppliers: []market.Supplier{{ID: sid, Name: "合成供应商", Enabled: true}}, Groups: []market.Group{{ID: gid, SupplierID: sid, Name: "合成分组", Enabled: true, Revision: 1, ConnectionVersion: 1}}, Addresses: []market.Address{{ID: aid, BaseURL: "https://synthetic.example", Label: "直连"}}, GroupAddresses: []market.GroupAddress{{ID: link, GroupID: gid, AddressID: aid}}, Models: []market.GroupModel{{ID: mid, GroupID: gid, ModelID: "vendor/model:1", UpstreamModel: "vendor/model:1", DisplayName: "合成模型", ModelType: "text", Enabled: true, Revision: 1, QuoteID: &qid}}, Quotes: []market.Quote{{ID: qid, GroupModelID: mid, Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}}}
	if err := runtime.Install(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	service := &groupchannel.Service{Repository: repo, Runtime: runtime}
	p := newPlatform(t, func(deps *Dependencies) { service.Keyring = deps.Channels.Keyring; deps.GroupChannels = service })
	owner, _ := p.member(t, "group-owner")
	other, _ := p.member(t, "group-other")
	admin := p.admin(t)
	owner.expect(t, 401, "GET", "/api/group-channels", nil, withoutCookie)
	owner.expect(t, 403, "GET", "/api/admin/group-channels", nil)
	owner.expect(t, 200, "GET", "/api/group-channels/sources", nil)
	owner.expect(t, 200, "GET", "/api/group-channels", nil)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"message":"unsupported synthetic protocol"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"synthetic","object":"chat.completion","created":1,"model":"vendor/model:1","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	prober := groupchannel.NewProber(context.Background(), groupchannel.ProbeConfig{Runtime: runtime, Repository: repo, Keyring: service.Keyring, Outbound: func(context.Context) (channel.Outbound, error) { return newTestOutbound(t, upstream), nil }})
	defer prober.Close()
	created := owner.expect(t, 201, "POST", "/api/group-channels", map[string]any{"group_id": gid, "api_key": "synthetic-key"})
	id := created["id"].(string)
	path := "/api/group-channels/" + id

	deadline := time.Now().Add(5 * time.Second)
	for {
		view, e := service.Get(context.Background(), groupchannel.Actor{ID: created["owner"].(map[string]any)["id"].(string)}, id)
		if e != nil {
			t.Fatal(e)
		}
		if view.Models[0].ProbeState == "available" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("group+Key did not automatically finish the real synthetic HTTP probe")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if created["name"] != "合成分组" || created["selected_address_ids"].([]any)[0] != link {
		t.Fatal("automatic defaults missing")
	}
	if _, ok := created["api_key"]; ok {
		t.Fatal("credential leaked")
	}
	owner.expect(t, 200, "GET", path, nil)
	owner.expect(t, 200, "GET", path+"/events", nil)
	other.expect(t, 404, "GET", path+"/events", nil)
	other.expect(t, 404, "GET", path, nil)
	other.expect(t, 404, "PATCH", path, map[string]any{"revision": 1, "name": "stolen"})
	admin.expect(t, 200, "GET", "/api/admin/group-channels", nil)
	admin.expect(t, 200, "GET", "/api/admin/group-channels/"+id, nil)
	updated := owner.expect(t, 200, "PATCH", path, map[string]any{"revision": 1, "overrides": map[string]any{"header_rules": map[string]any{}}, "models": []any{map[string]any{"group_model_id": mid, "excluded": true, "discount": "0"}}})
	if updated["models"].([]any)[0].(map[string]any)["discount"] != "0" {
		t.Fatal("zero discount lost")
	}
	owner.expect(t, 409, "PATCH", path, map[string]any{"revision": 1, "name": "stale"})
	pending := owner.expect(t, 202, "POST", path+"/test", map[string]any{"revision": updated["revision"]})
	if !pending["models"].([]any)[0].(map[string]any)["excluded"].(bool) {
		t.Fatal("retest restored exclusion")
	}
	admin.expect(t, http.StatusNoContent, "POST", "/api/admin/group-channels/"+id+"/moderate", map[string]string{"reason": "合成下架原因"})
	owner.expect(t, 409, "PATCH", path, map[string]any{"revision": pending["revision"], "status": "listed"})
	admin.expect(t, http.StatusNoContent, "POST", "/api/admin/group-channels/"+id+"/moderate", map[string]string{"reason": ""})
	owner.expect(t, http.StatusNoContent, "DELETE", path, map[string]any{"revision": pending["revision"]})
	owner.expect(t, 404, "GET", path, nil)
}
