package api

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type apiReviewStore struct {
	mu     sync.Mutex
	drafts map[string]contribution.Draft
	live   market.Directory
}

func reviewClone[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}
func newAPIReviewStore() *apiReviewStore {
	return &apiReviewStore{drafts: map[string]contribution.Draft{}}
}
func (s *apiReviewStore) Create(_ context.Context, d contribution.Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafts[d.ID] = reviewClone(d)
	return nil
}
func (s *apiReviewStore) Get(_ context.Context, id string) (contribution.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[id]
	if !ok {
		return d, contribution.ErrNotFound
	}
	return reviewClone(d), nil
}
func (s *apiReviewStore) List(_ context.Context, actor contribution.Actor, queue bool, cursor *contribution.Cursor, limit int) ([]contribution.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []contribution.Summary{}
	for _, d := range s.drafts {
		if (queue && (d.State == "submitted" || d.State == "publication_pending") || !queue && d.OwnerID == actor.ID) && (cursor == nil || d.CreatedAt.Before(cursor.At) || d.CreatedAt.Equal(cursor.At) && d.ID < cursor.ID) {
			out = append(out, contribution.Summary{ID: d.ID, Title: d.Title, Kind: d.Kind, State: d.State, Revision: d.Revision, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt})
		}
	}
	slices.SortFunc(out, func(a, b contribution.Summary) int {
		if n := b.CreatedAt.Compare(a.CreatedAt); n != 0 {
			return n
		}
		return strings.Compare(b.ID, a.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *apiReviewStore) Edit(ctx context.Context, id string, f func(*contribution.Draft, contribution.UnitOfWork) error) (contribution.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[id]
	if !ok {
		return d, contribution.ErrNotFound
	}
	next := reviewClone(d)
	before := reviewClone(s.live)
	if err := f(&next, apiReviewUnit{s}); err != nil {
		s.live = before
		return contribution.Draft{}, err
	}
	s.drafts[id] = reviewClone(next)
	return next, nil
}
func (s *apiReviewStore) Snapshot(context.Context) (market.Directory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return reviewClone(s.live), nil
}
func (s *apiReviewStore) Apply(context.Context, string, market.Changes) error {
	return market.ErrInvalid
}

type apiReviewUnit struct{ s *apiReviewStore }

func (u apiReviewUnit) Directory(context.Context) (market.Directory, error) {
	return reviewClone(u.s.live), nil
}
func (u apiReviewUnit) Report(context.Context, string) (detection.Job, error) {
	return detection.Job{}, detection.ErrNotFound
}
func (u apiReviewUnit) ApprovedReports(context.Context, []string) ([]string, error) {
	return []string{}, nil
}
func (u apiReviewUnit) Publish(_ context.Context, _ string, c market.Changes) error {
	d, _, err := contribution.Overlay(contribution.Draft{Changes: c}, u.s.live)
	u.s.live = d
	return err
}
func (apiReviewUnit) RecordEvidence(context.Context, detection.Job) error         { return nil }
func (apiReviewUnit) Audit(context.Context, string, string, map[string]any) error { return nil }

type apiReviewPlanner struct{}

func (apiReviewPlanner) Plan(detection.Target) (detection.Plan, error) {
	return detection.Plan{Items: []detection.Planned{{ID: "pending.fixture", Implemented: false}}}, nil
}
func TestContributionAPIPrivateReviewRevisionAndSchema(t *testing.T) {
	store := newAPIReviewStore()
	runtime := contribution.NewRuntime(store, nil)
	service := &contribution.Service{Store: store, Live: store, Planner: apiReviewPlanner{}, Publisher: runtime}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`<link rel="icon" href="/favicon.png">`))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	}))
	defer upstream.Close()
	policy := newTestOutbound(t, upstream)
	p := newPlatform(t, func(d *Dependencies) { d.Contributions = service; d.Suppliers.Outbound = policy })
	admin := p.admin(t)
	owner, _ := p.member(t, "contributor")
	other, _ := p.member(t, "other-contributor")
	owner.expect(t, 401, "GET", "/api/contributions", nil, withoutCookie)
	owner.expect(t, 403, "GET", "/api/admin/contributions", nil)
	d := owner.expect(t, 201, "POST", "/api/contributions", map[string]any{"kind": "supplier"})["draft"].(map[string]any)
	id := d["id"].(string)
	path := "/api/contributions/" + id
	other.expect(t, 404, "GET", path, nil)
	owner.expect(t, 200, "GET", path, nil)
	owner.expect(t, 200, "GET", "/api/contributions", nil)
	changes := d["changes"].(map[string]any)
	v := changes["suppliers"].([]any)[0].(map[string]any)["value"].(map[string]any)
	v["name"] = "合成供应商"
	v["website_url"] = "https://synthetic.example"
	save := map[string]any{"revision": d["revision"], "title": "合成贡献", "changes": changes, "report_ids": []string{}}
	d = owner.expect(t, 200, "PUT", path, save)["draft"].(map[string]any)
	owner.expect(t, 409, "PUT", path, save)
	owner.expect(t, 403, "POST", "/api/admin/contributions/"+id+"/approve", map[string]any{"revision": d["revision"]})
	d = owner.expect(t, 200, "POST", path+"/submit", map[string]any{"revision": d["revision"]})["draft"].(map[string]any)
	admin.expect(t, 200, "GET", "/api/admin/contributions", nil)
	d = admin.expect(t, 200, "POST", "/api/admin/contributions/"+id+"/reject", map[string]any{"revision": d["revision"], "reason": "请补充资料"})["draft"].(map[string]any)
	d = owner.expect(t, 200, "POST", path+"/submit", map[string]any{"revision": d["revision"]})["draft"].(map[string]any)
	d = admin.expect(t, 200, "POST", "/api/admin/contributions/"+id+"/approve", map[string]any{"revision": d["revision"]})["draft"].(map[string]any)
	owner.expect(t, 200, "GET", path, nil)
	owner.expect(t, 200, "GET", "/api/contributions/targets?kind=supplier", nil)
	if d["state"] != "approved" {
		t.Fatal("approval missing")
	}
	child := owner.expect(t, 201, "POST", "/api/contributions", map[string]any{"kind": "supplier"})["draft"].(map[string]any)
	childID := child["id"].(string)
	parent := child["target_id"].(string)
	child = owner.expect(t, 200, "POST", "/api/contributions/"+childID+"/children", map[string]any{"revision": child["revision"], "kind": "group", "parent_id": parent})["draft"].(map[string]any)
	owner.expect(t, 200, "POST", "/api/contributions/"+childID+"/withdraw", map[string]any{"revision": child["revision"]})
	page := owner.expect(t, 200, "GET", "/api/contributions?limit=1", nil)
	if page["next_cursor"] == nil {
		t.Fatal("cursor missing")
	}
	owner.expect(t, 200, "GET", "/api/contributions?limit=1&cursor="+page["next_cursor"].(string), nil)
	owner.expect(t, 200, "POST", "/api/contributions/icon", map[string]string{"website_url": "https://synthetic.example"})
	owner.expect(t, 422, "POST", "/api/contributions", map[string]string{"kind": "model_id", "target_id": "model"})
}
func TestContributionMoneyUsesDecimalsAndBlankDoesNotBecomeFree(t *testing.T) {
	c := contributionChanges{Quotes: []contributionQuote{{ID: "quote", GroupModelID: "model", BillingUnit: "per_million_tokens", Prices: contributionPrices{Input: "0.000000001", Output: "2", CacheWrite: "0", CacheRead: "0"}}}}
	domain, declared, err := c.domain()
	if err != nil || len(declared) != 1 || domain.Quotes[0].Prices.Input != 1 {
		t.Fatal("decimal quote lost", err)
	}
	v := changesJSON(domain, nil)
	data, _ := json.Marshal(v)
	if !strings.Contains(string(data), `"input":"0.000000001"`) {
		t.Fatal("public quote exposes nanos", string(data))
	}
	c.Quotes[0].Prices.Input = ""
	_, declared, err = c.domain()
	if err != nil || len(declared) != 0 {
		t.Fatal("blank quote became explicit free", err)
	}
	c.Quotes[0].Prices.Input = "0"
	_, declared, err = c.domain()
	if err != nil || len(declared) != 1 {
		t.Fatal("explicit zero rejected", err)
	}
	c.Quotes[0].Prices.Input = "0.0000000001"
	if _, _, err = c.domain(); err == nil {
		t.Fatal("excess precision silently rounded")
	}
}
