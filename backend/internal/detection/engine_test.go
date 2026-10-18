package detection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type memoryStore struct {
	reports   map[string]Job
	artifacts map[string][]Artifact
	mu        sync.Mutex
	jobs      map[string]Job
	attempts  map[string]map[string]Attempt
	events    chan string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{reports: map[string]Job{}, artifacts: map[string][]Artifact{}, jobs: map[string]Job{}, attempts: map[string]map[string]Attempt{}, events: make(chan string, 100)}
}
func cloneJob(job Job) Job {
	b, _ := json.Marshal(job)
	var out Job
	_ = json.Unmarshal(b, &out)
	out.Target.Binding.CredentialScope = job.Target.Binding.CredentialScope
	return out
}
func (m *memoryStore) Create(_ context.Context, j Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = cloneJob(j)
	return nil
}
func (m *memoryStore) Get(_ context.Context, id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return cloneJob(v), nil
}
func (m *memoryStore) List(_ context.Context, owner string, admin bool, _ *Cursor, limit int) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Job{}
	for _, j := range m.jobs {
		if j.OwnerID == owner || admin {
			out = append(out, cloneJob(j))
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (m *memoryStore) Save(_ context.Context, j Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = cloneJob(j)
	select {
	case m.events <- j.ID:
	default:
	}
	return nil
}
func (m *memoryStore) Interrupt(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.jobs {
		if j.State == "queued" || j.State == "running" {
			j.State = "interrupted"
			j.Complete = false
			m.jobs[id] = j
		}
	}
	return nil
}
func (m *memoryStore) RecordAttempt(_ context.Context, id string, a Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attempts[id] == nil {
		m.attempts[id] = map[string]Attempt{}
	}
	m.attempts[id][a.ID] = a
	return nil
}
func (m *memoryStore) Attempts(_ context.Context, id string, ids []string) ([]Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Attempt{}
	for _, ref := range ids {
		if a, ok := m.attempts[id][ref]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}
func (m *memoryStore) wait(t *testing.T, id string, states ...string) Job {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		j, err := m.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(states, j.State) {
			return j
		}
		select {
		case <-m.events:
		case <-timer.C:
			t.Fatalf("job stuck: %s", j.State)
		}
	}
}

type fixtureOutbound struct{ client *http.Client }

func (f fixtureOutbound) NormalizeBaseURL(raw string) (string, error) { return raw, nil }
func (f fixtureOutbound) ValidateBaseURL(_ context.Context, raw string) (string, error) {
	return raw, nil
}
func (f fixtureOutbound) Client(context.Context, string, time.Duration) (*http.Client, error) {
	return f.client, nil
}
func (f fixtureOutbound) GatewayClient(context.Context, string) (*http.Client, error) {
	return f.client, nil
}
func (f fixtureOutbound) FreshClient(context.Context, string) (*http.Client, error) {
	return f.client, nil
}
func (f fixtureOutbound) WithExtraBlockedHosts([]string) (channel.Outbound, error) { return f, nil }

type fixtureResolver struct {
	profile TestProfile
	base    string
	mu      sync.Mutex
	digest  string
}

func (f *fixtureResolver) Resolve(_ context.Context, actor Actor, input Target) (Resolved, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if actor.ID != "owner" && !actor.Admin {
		return Resolved{}, ErrNotFound
	}
	return Resolved{Target: Target{Profile: f.profile, Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: input.Binding.TargetID, TargetRevision: 1, CapabilityDigest: f.digest}, ModelType: "text", Model: "native/model:v1"}, Addresses: []string{f.base}}, nil
}
func fixtureManifest(requirements ...string) gatewaycontract.Manifest {
	reqs := []gatewaycontract.Requirement{}
	for _, id := range requirements {
		reqs = append(reqs, gatewaycontract.Requirement{ID: id, Endpoints: []string{"fixture.infer"}, SourceIDs: []string{id}, Assertions: []string{"first", "second"}})
	}
	return gatewaycontract.Manifest{Version: "fixture.v1", Stage: "discovery", Sources: []gatewaycontract.Source{{ID: "fixture", URL: "https://fixture.example/docs", Version: "pinned"}}, Endpoints: []gatewaycontract.Endpoint{{ID: "fixture.infer", Protocol: "openai-responses", Method: "POST", Path: "/v1/responses", Source: "fixture", Feature: "F7", Auth: "platform_key", Scope: "stateless", Model: "explicit", Billing: "inference", Replay: "inference"}}, Suites: []gatewaycontract.Suite{{ID: "fixture.suite", EvidenceLayers: []string{"upstream_protocol"}, ModelTypes: []string{"text"}, Target: "group_model", Protocol: "openai-responses", Feature: "F4", Requirements: reqs}}}
}
func testParameters() Parameters {
	v := gatewaycontract.SearchSettings{Version: "test.v1", Method: "binary", Low: 0, High: 100, HardMaximum: 100, PrecisionMode: "absolute", Precision: 1, Rounds: 8}
	return Parameters{Context: v, MaxOutput: v}
}
func makeService(t *testing.T, r *Registry, m *memoryStore, resolver *fixtureResolver, limits Limits) *Service {
	t.Helper()
	s, err := New(context.Background(), Config{Registry: r, Store: m, Resolver: resolver, Outbound: fixtureOutbound{&http.Client{}}, Limits: limits, CredentialRoot: make([]byte, 32), Synthetic: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func fixtureRunner(requirements ...string) Runner {
	return func(ctx context.Context, s *Session) Result {
		response, err := s.Do(ctx, Request{EndpointID: "fixture.infer", Path: "/v1/responses", Headers: http.Header{"Content-Type": []string{"application/json"}, "authorization": []string{"must-not-cross"}}, Body: []byte(`{"synthetic":"never persist this body"}`), Category: "generate"})
		state, reason := "pass", "ok"
		if err != nil {
			state, reason = "inconclusive", "external_error"
		} else if response.Status == 404 {
			state, reason = "unsupported", "unsupported"
		}
		result := Result{State: state, Reason: reason, Assertions: []Assertion{}}
		for _, req := range requirements {
			for i := range 2 {
				result.Assertions = append(result.Assertions, Assertion{ID: AssertionID(req, i), State: state, Reason: reason, AttemptIDs: []string{response.AttemptID}})
			}
		}
		return result
	}
}
func register(t *testing.T, r *Registry, id string, requirements []string, dependencies []string, run Runner) {
	t.Helper()
	if err := r.Register(Definition{ID: id, SuiteID: "fixture.suite", Requirements: requirements, Dependencies: dependencies, CodeVersion: "test.v1", Layer: "upstream_protocol", Run: run}); err != nil {
		t.Fatal(err)
	}
}

func TestFinalManifestCannotDisappearWhenNoSuitesAreRegistered(t *testing.T) {
	manifest, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []Target{{Binding: market.EvidenceBinding{TargetKind: "group", TargetID: "g"}}, {Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "m"}, ModelType: "text"}, {Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "i"}, ModelType: "image"}, {Binding: market.EvidenceBinding{TargetKind: "gateway", TargetID: "platform"}}} {
		plan, err := r.Plan(target)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Items) == 0 || !HasPending(plan) {
			t.Fatal("missing implementation masquerades as complete")
		}
		for _, item := range plan.Items {
			if slices.Contains(item.Requirements, "codex-special.RL11") {
				t.Fatal("excluded WebSocket counted")
			}
		}
	}
}
func TestSharedRequestIndependentAssertionsAndNoBodyPersistence(t *testing.T) {
	var calls int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer synthetic-private-key" {
			t.Error("wrong auth")
		}
		_, _ = w.Write([]byte(`{"opaque":"response-must-not-persist"}`))
	}))
	defer up.Close()
	r, _ := NewRegistry(fixtureManifest("a", "b"))
	register(t, r, "combined", []string{"a", "b"}, nil, fixtureRunner("a", "b"))
	m := newMemoryStore()
	s := makeService(t, r, m, &fixtureResolver{base: up.URL, digest: strings.Repeat("a", 64)}, DefaultLimits())
	job, err := s.Create(context.Background(), Actor{ID: "owner"}, Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}, ModelType: "text"}, testParameters(), "synthetic-private-key")
	if err != nil {
		t.Fatal(err)
	}
	job = m.wait(t, job.ID, "finished", "failed")
	if job.State != "finished" || len(job.Results) != 1 || calls != 1 || job.Usage.HTTP != 1 || job.Usage.Generate != 1 {
		t.Fatalf("shared request: %+v calls=%d", job, calls)
	}
	if job.Complete {
		t.Fatal("synthetic evidence authorizes real creation")
	}
	encoded, _ := json.Marshal(job)
	for _, secret := range []string{"synthetic-private-key", "response-must-not-persist", "never persist this body"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("sensitive content persisted: %s", secret)
		}
	}
	if _, err = s.Get(context.Background(), Actor{ID: "other"}, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign report access")
	}
	if _, err = s.Get(context.Background(), Actor{ID: "admin", Admin: true}, job.ID); err != nil {
		t.Fatal(err)
	}
}
func TestNegativePrerequisiteStillRunsIndependentScenes(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { mu.Lock(); calls++; mu.Unlock(); w.WriteHeader(404) }))
	defer up.Close()
	r, _ := NewRegistry(fixtureManifest("a", "b", "c"))
	register(t, r, "first", []string{"a"}, nil, fixtureRunner("a"))
	register(t, r, "dependent", []string{"b"}, []string{"first"}, fixtureRunner("b"))
	register(t, r, "independent", []string{"c"}, nil, fixtureRunner("c"))
	m := newMemoryStore()
	s := makeService(t, r, m, &fixtureResolver{base: up.URL, digest: strings.Repeat("a", 64)}, DefaultLimits())
	j, err := s.Create(context.Background(), Actor{ID: "owner"}, Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}}, testParameters(), "key")
	if err != nil {
		t.Fatal(err)
	}
	j = m.wait(t, j.ID, "finished", "failed")
	if j.State != "finished" || len(j.Results) != 3 || calls != 2 {
		t.Fatalf("independent scenes skipped: %+v calls=%d", j, calls)
	}
	for i := range j.Results {
		j.Results[i].Source = "real_upstream"
	}
	if !Complete(j) {
		t.Fatal("complete negative report blocked")
	}
	j.Results[0].Assertions[0].State = "skipped"
	if Complete(j) {
		t.Fatal("skipped masks unfinished assertion")
	}
}
func TestCancellationSaturationRestartAndChangedCredentials(t *testing.T) {
	entered := make(chan struct{}, 2)
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer up.Close()
	r, _ := NewRegistry(fixtureManifest("a"))
	register(t, r, "a", []string{"a"}, nil, fixtureRunner("a"))
	m := newMemoryStore()
	limits := DefaultLimits()
	limits.Workers = 1
	limits.Queue = 1
	limits.PerOwner = 1
	resolver := &fixtureResolver{base: up.URL, digest: strings.Repeat("a", 64)}
	s := makeService(t, r, m, resolver, limits)
	target := Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}}
	j, err := s.Create(context.Background(), Actor{ID: "owner"}, target, testParameters(), "key")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	if _, err = s.Create(context.Background(), Actor{ID: "owner"}, target, testParameters(), "key"); !errors.Is(err, ErrBusy) {
		t.Fatalf("per owner active limit: %v", err)
	}
	if _, err = s.Cancel(context.Background(), Actor{ID: "owner"}, j.ID); err != nil {
		t.Fatal(err)
	}
	j = m.wait(t, j.ID, "cancelled")
	s.Close()
	j, _ = m.Get(context.Background(), j.ID)
	if j.State != "cancelled" || j.Complete || j.Usage.HTTP != 1 {
		t.Fatalf("cancel overwritten or usage lost: %+v", j)
	}
	s = makeService(t, r, m, resolver, limits)
	continued, err := s.Resume(context.Background(), Actor{ID: "owner"}, j.ID, "different-key")
	if err != nil || continued.ID == j.ID || continued.ContinuedFromID == nil || *continued.ContinuedFromID != j.ID {
		t.Fatalf("new credential lost provenance: %+v %v", continued, err)
	}
	_, _ = s.Cancel(context.Background(), Actor{ID: "owner"}, continued.ID)
	s.Close()
	s = makeService(t, r, m, resolver, limits)
	resolver.mu.Lock()
	resolver.digest = strings.Repeat("b", 64)
	resolver.mu.Unlock()
	if _, err = s.Resume(context.Background(), Actor{ID: "owner"}, j.ID, "key"); !errors.Is(err, ErrConflict) {
		t.Fatalf("configuration changed silently: %v", err)
	}
}
func TestWitnessAndPathBoundaries(t *testing.T) {
	path, err := gatewaycontract.ModelActionPath("publishers/x/models/a:v1", "countTokens")
	if err != nil || !matchPath("/v1beta/models/{model}:countTokens", path) {
		t.Fatalf("native model path rejected %s %v", path, err)
	}
	for _, p := range []string{"/v1/responses/../cancel", "/v1/responses/%2e%2e/cancel", "/v1/responses/%2fadmin/cancel", "https://other.example/v1/responses/a/cancel"} {
		if matchPath("/v1/responses/{response_id}/cancel", p) {
			t.Fatalf("unsafe path %s", p)
		}
	}
	if !oversizedEvent([]byte("data: abc\ndata: def\n\n"), 10) || oversizedEvent([]byte("data:a\n\ndata:b\n\n"), 10) {
		t.Fatal("SSE event memory limit")
	}
	item := Planned{ID: "s", Layer: "upstream_protocol", Assertions: []string{"a"}}
	result := Result{ScenarioID: "s", Layer: "upstream_protocol", Source: "real_upstream", State: "pass", Reason: "ok", Assertions: []Assertion{{ID: "a", State: "pass", Reason: "ok", AttemptIDs: []string{"made-up"}}}}
	if ValidateResult(item, result, map[string]Attempt{}) == nil {
		t.Fatal("fabricated witness accepted")
	}
	result.Assertions[0].AttemptIDs = []string{"network-failure"}
	if ValidateResult(item, result, map[string]Attempt{"network-failure": {ID: "network-failure", Sent: true}}) == nil {
		t.Fatal("network failure proves support")
	}
}

func TestQueuedCancellationImmediatelyReleasesAdmission(t *testing.T) {
	entered := make(chan struct{}, 1)
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		entered <- struct{}{}
		<-req.Context().Done()
	}))
	defer up.Close()
	registry, _ := NewRegistry(fixtureManifest("a"))
	register(t, registry, "a", []string{"a"}, nil, fixtureRunner("a"))
	store := newMemoryStore()
	limits := DefaultLimits()
	limits.Workers, limits.Queue, limits.PerOwner = 1, 1, 2
	service := makeService(t, registry, store, &fixtureResolver{base: up.URL, digest: strings.Repeat("a", 64)}, limits)
	defer service.Close()
	actor := Actor{ID: "owner"}
	target := Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}}
	running, err := service.Create(context.Background(), actor, target, testParameters(), "key")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	queued, err := service.Create(context.Background(), actor, target, testParameters(), "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(context.Background(), actor, target, testParameters(), "key"); !errors.Is(err, ErrBusy) {
		t.Fatalf("queue saturation: %v", err)
	}
	if _, err = service.Cancel(context.Background(), actor, queued.ID); err != nil {
		t.Fatal(err)
	}
	replacement, err := service.Create(context.Background(), actor, target, testParameters(), "key")
	if err != nil {
		t.Fatalf("cancelled task retained admission: %v", err)
	}
	_, _ = service.Cancel(context.Background(), actor, replacement.ID)
	_, _ = service.Cancel(context.Background(), actor, running.ID)
}

func TestSharedScenarioCannotBorrowOtherModelTypeAssertions(t *testing.T) {
	manifest := fixtureManifest("text_only", "both_types")
	manifest.Suites[0].ModelTypes = []string{"text", "image"}
	manifest.Suites[0].Requirements[0].ModelTypes = []string{"text"}
	registry, err := NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	err = registry.Register(Definition{ID: "shared", SuiteID: "fixture.suite", Requirements: []string{"text_only", "both_types"}, CodeVersion: "test.v1", Layer: "upstream_protocol", Run: fixtureRunner("text_only", "both_types")})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("mixed applicability accepted: %v", err)
	}
}

func (m *memoryStore) AddArtifact(_ context.Context, id string, a Artifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.artifacts[id] = append(m.artifacts[id], a)
	return nil
}
func (m *memoryStore) Artifacts(_ context.Context, id string) ([]Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.artifacts[id]), nil
}
func (m *memoryStore) SaveArtifact(_ context.Context, id string, a Artifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, v := range m.artifacts[id] {
		if v.ID == a.ID {
			m.artifacts[id][i] = a
			return nil
		}
	}
	return ErrNotFound
}

func (m *memoryStore) RecordResult(_ context.Context, _ string, _ Result) error { return nil }

func TestCancellationCleansOnlyWitnessedResources(t *testing.T) {
	created := make(chan struct{}, 1)
	deleted := make(chan string, 2)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted <- r.URL.Path
			w.WriteHeader(204)
			return
		}
		_, _ = w.Write([]byte(`{"id":"owned-response"}`))
	}))
	defer up.Close()
	manifest := fixtureManifest("a")
	manifest.Endpoints = append(manifest.Endpoints, gatewaycontract.Endpoint{ID: "fixture.delete", Protocol: "openai-responses", Method: "DELETE", Path: "/v1/responses/{response_id}", Source: "fixture", Feature: "F7", Auth: "platform_key", Scope: "remote_resource", Model: "owned_context", Billing: "none", Replay: "write"})
	r, _ := NewRegistry(manifest)
	register(t, r, "a", []string{"a"}, nil, func(ctx context.Context, s *Session) Result {
		response, err := s.Do(ctx, Request{EndpointID: "fixture.infer", Path: "/v1/responses", Body: []byte(`{}`), Category: "generate"})
		if err != nil {
			panic(err)
		}
		if err := s.TrackResource(ctx, "unrelated-attempt", "response", "someone-else", "fixture.delete", "/v1/responses/someone-else"); err == nil {
			panic("unowned cleanup accepted")
		}
		if err := s.TrackResource(ctx, response.AttemptID, "response", "owned-response", "fixture.delete", "/v1/responses/owned-response"); err != nil {
			panic(err)
		}
		created <- struct{}{}
		<-ctx.Done()
		return Result{State: "invalid"}
	})
	m := newMemoryStore()
	s := makeService(t, r, m, &fixtureResolver{base: up.URL, digest: strings.Repeat("a", 64)}, DefaultLimits())
	job, err := s.Create(context.Background(), Actor{ID: "owner"}, Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}}, testParameters(), "key")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-created:
	case <-time.After(3 * time.Second):
		t.Fatal("create not witnessed")
	}
	if _, err := s.Cancel(context.Background(), Actor{ID: "owner"}, job.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case path := <-deleted:
		if path != "/v1/responses/owned-response" {
			t.Fatal("cleaned foreign resource")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel cleanup missing")
	}
	s.Close()
	job, _ = m.Get(context.Background(), job.ID)
	if job.State != "cancelled" || job.Usage.HTTP != 2 || job.Usage.Cleanup != 1 {
		t.Fatalf("cleanup consumption lost: %+v", job.Usage)
	}
	artifacts, _ := m.Artifacts(context.Background(), job.ID)
	if len(artifacts) != 1 || !artifacts[0].Removed {
		t.Fatal("cleanup status not retained")
	}
}
func TestPartialRetestKeepsIndependentEvidence(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()
	r, _ := NewRegistry(fixtureManifest("a", "b"))
	register(t, r, "a", []string{"a"}, nil, fixtureRunner("a"))
	register(t, r, "b", []string{"b"}, nil, fixtureRunner("b"))
	m := newMemoryStore()
	s := makeService(t, r, m, &fixtureResolver{base: up.URL, digest: strings.Repeat("a", 64)}, DefaultLimits())
	job, err := s.Create(context.Background(), Actor{ID: "owner"}, Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}}, testParameters(), "key")
	if err != nil {
		t.Fatal(err)
	}
	job = m.wait(t, job.ID, "finished")
	old := job.Results[1]
	// Wait for terminal cleanup/removal before scheduling a new run; this is an
	// in-process lifecycle condition, not an upstream time-based capability test.
	s.mu.Lock()
	_, live := s.live[job.ID]
	s.mu.Unlock()
	if live {
		s.Close()
		s = makeService(t, r, m, &fixtureResolver{base: up.URL, digest: strings.Repeat("a", 64)}, DefaultLimits())
	}
	if _, err := s.Retest(context.Background(), Actor{ID: "owner"}, job.ID, "key", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	job = m.wait(t, job.ID, "finished")
	if len(job.Results) != 2 || job.Usage.HTTP != 3 || calls != 3 {
		t.Fatalf("partial retest reran everything: %+v calls=%d", job.Usage, calls)
	}
	kept := false
	for _, result := range job.Results {
		if result.ID == old.ID {
			kept = true
		}
	}
	if !kept {
		t.Fatal("independent original evidence lost")
	}
}

func (m *memoryStore) AttemptPage(ctx context.Context, id string, _ *Cursor, limit int) ([]Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Attempt{}
	for _, a := range m.attempts[id] {
		out = append(out, a)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memoryStore) Finish(ctx context.Context, job Job) (Job, error) {
	id := fmt.Sprintf("report-%d", len(job.Results)) + job.UpdatedAt.String()
	job.ReportID = &id
	m.mu.Lock()
	m.reports[id] = cloneJob(job)
	m.mu.Unlock()
	return job, m.Save(ctx, job)
}
func (m *memoryStore) GetReport(_ context.Context, id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.reports[id]
	if !ok {
		return job, ErrNotFound
	}
	return cloneJob(job), nil
}
func (m *memoryStore) ReportPage(context.Context, string, *Cursor, int) ([]ReportSummary, error) {
	return []ReportSummary{}, nil
}

func TestThreeEvidenceLayersCannotReplaceEachOther(t *testing.T) {
	manifest := fixtureManifest("a")
	manifest.Suites[0].EvidenceLayers = []string{"upstream_protocol", "gateway_proxy", "official_cli"}
	r, err := NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "native", []string{"a"}, nil, fixtureRunner("a"))
	target := Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}, ModelType: "text"}
	plan, err := r.Plan(target)
	if err != nil || len(plan.Items) != 3 || !HasPending(plan) {
		t.Fatalf("one evidence layer satisfied all: %+v %v", plan, err)
	}
	r, err = NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range manifest.Suites[0].EvidenceLayers {
		if err := r.Register(Definition{ID: layer, SuiteID: "fixture.suite", Requirements: []string{"a"}, CodeVersion: "fixture.v1", Layer: layer, Run: fixtureRunner("a")}); err != nil {
			t.Fatal(err)
		}
	}
	plan, err = r.Plan(target)
	if err != nil || len(plan.Items) != 3 || HasPending(plan) {
		t.Fatalf("independent layers not expressible: %+v %v", plan, err)
	}
}

func TestImplementedScenariosRunWhileOtherSuitesRemainPending(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer up.Close()
	m := newMemoryStore()
	r, err := NewRegistry(fixtureManifest("native", "future"))
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "native", []string{"native"}, nil, fixtureRunner("native"))
	s := makeService(t, r, m, &fixtureResolver{base: up.URL, digest: "fixed"}, DefaultLimits())
	job, err := s.Create(context.Background(), Actor{ID: "owner"}, Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}}, testParameters(), "synthetic-secret")
	if err != nil || job.State != "queued" {
		t.Fatalf("implemented suite blocked by future work: %+v %v", job, err)
	}
	job = m.wait(t, job.ID, "suite_pending", "failed")
	if job.State != "suite_pending" || len(job.Results) != 1 || job.Results[0].ScenarioID != "native" || job.Usage.HTTP != 1 || job.Complete || job.ReportID == nil {
		t.Fatalf("partial execution lost evidence or faked completion: %+v", job)
	}
	// Terminal persistence precedes execute's deferred secret/slot cleanup.
	// Wait for that worker boundary; Resume is allowed to report busy before it.
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		idle := s.live[job.ID] == nil
		s.mu.Unlock()
		if idle {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("terminal worker did not release its slot")
		}
	}
	if _, err = s.Resume(context.Background(), Actor{ID: "owner"}, job.ID, "synthetic-secret"); err != nil {
		t.Fatal(err)
	}
	job = m.wait(t, job.ID, "suite_pending", "failed")
	if job.Usage.HTTP != 1 || len(job.Results) != 1 || job.Complete {
		t.Fatal("resume repeated completed work or authorized incomplete report")
	}
}
func TestConditionalObservationIsOwnedByManifest(t *testing.T) {
	manifest := fixtureManifest("a")
	manifest.Suites[0].Requirements[0].ConditionalAssertions = []string{"a/a2"}
	r, err := NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "a", []string{"a"}, nil, fixtureRunner("a"))
	// Caller mutation must not alter a task's canonical requirement declaration.
	manifest.Suites[0].Requirements[0].ConditionalAssertions = append(manifest.Suites[0].Requirements[0].ConditionalAssertions, "a/a1")
	plan, err := r.Plan(Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "t"}, ModelType: "text"})
	if err != nil {
		t.Fatal(err)
	}
	item := plan.Items[0]
	if !slices.Equal(item.Conditional, []string{"a/a2"}) {
		t.Fatalf("mutable or missing applicability: %+v", item.Conditional)
	}
	result := Result{ScenarioID: item.ID, Layer: item.Layer, Source: "synthetic", State: "partial", Reason: "not_observed", Assertions: []Assertion{{ID: "a/a1", State: "pass", Reason: "ok", AttemptIDs: []string{"attempt"}}, {ID: "a/a2", State: "unobserved", Reason: "not_observed", AttemptIDs: []string{"attempt"}}}}
	witnesses := map[string]Attempt{"attempt": {ID: "attempt", Sent: true, ResponseReceived: true, Status: 200}}
	if ValidateResult(item, result, witnesses) != nil {
		t.Fatal("declared conditional observation requires another request")
	}
	result.Assertions[0].State = "unobserved"
	result.Assertions[0].Reason = "not_observed"
	if ValidateResult(item, result, witnesses) == nil {
		t.Fatal("mandatory assertion silently made conditional")
	}
}

func TestNewCredentialForksProvenanceAndReusesEngineEvidence(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	manifest := fixtureManifest("auth", "capability")
	manifest.Suites[0].Requirements[0].CredentialDependent = true
	registry, err := NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	register(t, registry, "auth", []string{"auth"}, nil, fixtureRunner("auth"))
	register(t, registry, "capability", []string{"capability"}, nil, fixtureRunner("capability"))
	store := newMemoryStore()
	resolver := &fixtureResolver{base: upstream.URL, digest: strings.Repeat("a", 64)}
	service := makeService(t, registry, store, resolver, DefaultLimits())
	old, err := service.Create(context.Background(), Actor{ID: "owner"}, Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}}, testParameters(), "key-a")
	if err != nil {
		t.Fatal(err)
	}
	old = store.wait(t, old.ID, "finished")
	service.Close()
	service = makeService(t, registry, store, resolver, DefaultLimits())
	continued, err := service.Retest(context.Background(), Actor{ID: "owner"}, old.ID, "key-b", []string{"auth"})
	if err != nil {
		t.Fatal(err)
	}
	continued = store.wait(t, continued.ID, "finished")
	service.Close()
	if continued.ID == old.ID || continued.ContinuedFromID == nil || len(continued.Results) != 2 || continued.Usage.HTTP != 1 || calls != 3 {
		t.Fatalf("credential change forced full rerun or hid source: %+v calls=%d", continued, calls)
	}
	for _, result := range continued.Results {
		if result.ScenarioID == "capability" && (result.SourceTaskID != old.ID || result.SourceReportID == nil || *result.SourceReportID != *old.ReportID) {
			t.Fatal("reused result lost original report/credential provenance")
		}
	}
	snapshot, err := service.GetReport(context.Background(), Actor{ID: "owner"}, *old.ReportID)
	if err != nil || snapshot.Usage.HTTP != 2 {
		t.Fatal("continuation rewrote original immutable report")
	}
}

func TestCodexImageEvidenceUsesTheImageModelTarget(t *testing.T) {
	manifest, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"text", "image"} {
		plan, err := r.Plan(Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "offering"}, ModelType: kind})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, item := range plan.Items {
			if slices.Contains(item.Requirements, "codex-special.RL25") {
				count++
			}
		}
		if (kind == "text" && count != 0) || (kind == "image" && count != 3) {
			t.Fatalf("RL25 %s target has %d layers", kind, count)
		}
	}
}
