package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type apiDetectorStore struct {
	reports map[string]detection.Job
	detection.Store
	mu         sync.Mutex
	jobs       map[string]detection.Job
	parameters detection.Parameters
	owner      string
}

func newAPIDetectorStore() *apiDetectorStore {
	v := gatewaycontract.SearchSettings{Version: "fixture.v1", Method: "binary", Low: 0, High: 100, HardMaximum: 100, PrecisionMode: "absolute", Precision: 1, Rounds: 8}
	return &apiDetectorStore{reports: map[string]detection.Job{}, jobs: map[string]detection.Job{}, parameters: detection.Parameters{Context: v, MaxOutput: v}}
}
func (s *apiDetectorStore) Create(_ context.Context, job detection.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
	return nil
}
func (s *apiDetectorStore) Get(_ context.Context, id string) (detection.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return job, detection.ErrNotFound
	}
	return job, nil
}
func (s *apiDetectorStore) Save(_ context.Context, job detection.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
	return nil
}
func (s *apiDetectorStore) List(_ context.Context, owner string, admin bool, cursor *detection.Cursor, limit int) ([]detection.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := []detection.Job{}
	for _, job := range s.jobs {
		if (owner == job.OwnerID || admin) && (cursor == nil || job.CreatedAt.Before(cursor.At) || (job.CreatedAt.Equal(cursor.At) && job.ID < cursor.ID)) {
			jobs = append(jobs, job)
		}
	}
	slices.SortFunc(jobs, func(a, b detection.Job) int {
		if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(b.ID, a.ID)
	})
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}
func (s *apiDetectorStore) Interrupt(context.Context) error { return nil }
func (s *apiDetectorStore) AttemptPage(context.Context, string, *detection.Cursor, int) ([]detection.Attempt, error) {
	return []detection.Attempt{}, nil
}
func (s *apiDetectorStore) Artifacts(context.Context, string) ([]detection.Artifact, error) {
	return []detection.Artifact{}, nil
}
func (s *apiDetectorStore) Parameters(context.Context) (detection.Parameters, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parameters, nil
}
func (s *apiDetectorStore) SetSearch(_ context.Context, _ string, target string, v gatewaycontract.SearchSettings) (gatewaycontract.SearchSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.Validate() != nil || v.Rounds > 128 || v.Rechecks > 8 {
		return v, detection.ErrInvalid
	}
	v.Version = "fixture.updated.v2"
	switch target {
	case "context":
		s.parameters.Context = v
	case "max_output":
		s.parameters.MaxOutput = v
	default:
		return v, detection.ErrInvalid
	}
	return v, nil
}
func (s *apiDetectorStore) Resolve(_ context.Context, actor detection.Actor, input detection.Target) (detection.Resolved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !actor.Admin && actor.ID != s.owner {
		return detection.Resolved{}, detection.ErrNotFound
	}
	return detection.Resolved{Target: detection.Target{GroupID: "00000000-0000-4000-8000-000000000001", Binding: market.EvidenceBinding{TargetKind: input.Binding.TargetKind, TargetID: input.Binding.TargetID, TargetRevision: 1, CapabilityDigest: strings.Repeat("a", 64)}, ModelType: "text", Model: "vendor/model:v1"}, Addresses: []string{"https://synthetic.example"}}, nil
}

type unusedDetectorOutbound struct{ channel.Outbound }

func TestDetectionTaskAndSettingsAPIContract(t *testing.T) {
	store := newAPIDetectorStore()
	manifest, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := detection.NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	service, err := detection.New(context.Background(), detection.Config{Registry: registry, Store: store, Settings: store, Resolver: store, Outbound: &unusedDetectorOutbound{}, Limits: detection.DefaultLimits(), CredentialRoot: make([]byte, 32), Synthetic: true})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	p := newPlatform(t, func(d *Dependencies) { d.Detection = service })
	admin := p.admin(t)
	owner, ownerID := p.member(t, "detector-owner")
	other, _ := p.member(t, "detector-other")
	store.owner = ownerID
	base := "/api/detection/jobs"
	owner.expect(t, 401, http.MethodGet, base, nil, withoutCookie)
	owner.expect(t, 403, http.MethodGet, "/api/admin/detection/search", nil)
	parameters := admin.expect(t, 200, http.MethodGet, "/api/admin/detection/search", nil)["parameters"].(map[string]any)
	settings := parameters["context"].(map[string]any)
	settings["method"] = "ternary"
	admin.expect(t, 200, http.MethodPut, "/api/admin/detection/search/context", settings)
	target := "00000000-0000-4000-8000-000000000002"
	input := map[string]string{"target_kind": "group_model", "target_id": target, "api_key": "synthetic-private-detector-key"}
	created := owner.expect(t, 202, http.MethodPost, base, input)["job"].(map[string]any)
	id := created["id"].(string)
	path := base + "/" + id
	if created["state"] != "suite_pending" || created["complete"] != false {
		t.Fatal("missing suites claim completion")
	}
	encoded, _ := json.Marshal(created)
	if strings.Contains(string(encoded), input["api_key"]) || strings.Contains(string(encoded), "credential_scope") {
		t.Fatal("task leaks private material")
	}
	owner.expect(t, 200, http.MethodGet, path, nil)
	admin.expect(t, 200, http.MethodGet, path, nil)
	other.expect(t, 404, http.MethodGet, path, nil)
	other.expect(t, 404, http.MethodGet, path+"/attempts", nil)
	other.expect(t, 404, http.MethodGet, path+"/artifacts", nil)
	owner.expect(t, 200, http.MethodGet, path+"/attempts", nil)
	owner.expect(t, 200, http.MethodGet, path+"/artifacts", nil)
	owner.expect(t, 200, http.MethodPost, path+"/cancel", nil)
	owner.expect(t, 202, http.MethodPost, path+"/resume", map[string]string{"api_key": input["api_key"]})
	continued := owner.expect(t, 202, http.MethodPost, path+"/resume", map[string]string{"api_key": "different-key"})["job"].(map[string]any)
	if continued["id"] == id || continued["continued_from_id"] != id {
		t.Fatal("new key reused old credential provenance")
	}
	store.mu.Lock()
	job := store.jobs[id]
	job.State = "failed"
	store.jobs[id] = job
	store.mu.Unlock()
	snapshot, err := store.Finish(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	owner.expect(t, 200, http.MethodGet, "/api/detection/reports/"+*snapshot.ReportID, nil)
	other.expect(t, 404, http.MethodGet, "/api/detection/reports/"+*snapshot.ReportID, nil)
	owner.expect(t, 200, http.MethodGet, path+"/reports", nil)
	owner.expect(t, 202, http.MethodPost, path+"/retest", map[string]any{"api_key": input["api_key"], "scenarios": []string{job.Plan.Items[0].ID}})
	for range 2 {
		owner.expect(t, 202, http.MethodPost, base, input)
	}
	page := owner.expect(t, 200, http.MethodGet, base+"?limit=1", nil)
	if page["next_cursor"] == nil {
		t.Fatal("job list cursor missing")
	}
	owner.expect(t, 200, http.MethodGet, base+"?limit=1&cursor="+page["next_cursor"].(string), nil)
	otherPage := other.expect(t, 200, http.MethodGet, base, nil)
	if len(otherPage["items"].([]any)) != 0 {
		t.Fatal("other owner report listed")
	}
	owner.expect(t, 400, http.MethodGet, base+"?cursor=invalid", nil)
	owner.expect(t, 422, http.MethodPost, base, map[string]string{"target_kind": "group_model", "target_id": "not-uuid", "api_key": "key"})
	other.expect(t, 404, http.MethodPost, base, input)
}

func (s *apiDetectorStore) Finish(ctx context.Context, job detection.Job) (detection.Job, error) {
	id, err := ids.NewUUID()
	if err != nil {
		return job, err
	}
	job.ReportID = &id
	job.ExecutionActive = false
	s.mu.Lock()
	s.reports[id] = job
	s.mu.Unlock()
	return job, s.Save(ctx, job)
}
func (s *apiDetectorStore) GetReport(_ context.Context, id string) (detection.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.reports[id]
	if !ok {
		return job, detection.ErrNotFound
	}
	return job, nil
}
func (s *apiDetectorStore) ReportPage(_ context.Context, id string, _ *detection.Cursor, limit int) ([]detection.ReportSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []detection.ReportSummary{}
	for reportID, job := range s.reports {
		if job.ID == id {
			out = append(out, detection.ReportSummary{ID: reportID, TaskID: job.ID, State: job.State, Complete: job.Complete, CreatedAt: job.UpdatedAt})
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
