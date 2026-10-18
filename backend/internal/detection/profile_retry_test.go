package detection

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestProfileChangeRetestsSelectorsAndPreservesUnrelatedEvidence(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer up.Close()
	r, err := NewRegistry(fixtureManifest("proto.base", "proto.cache_tier", "proto.other"))
	if err != nil {
		t.Fatal(err)
	}
	var runs [3]atomic.Int64
	for i, req := range []string{"proto.base", "proto.cache_tier", "proto.other"} {
		run := fixtureRunner(req)
		register(t, r, req, []string{req}, nil, func(ctx context.Context, s *Session) Result { runs[i].Add(1); return run(ctx, s) })
	}
	m := newMemoryStore()
	resolver := &fixtureResolver{base: up.URL, digest: "fixed"}
	s := makeService(t, r, m, resolver, DefaultLimits())
	job, err := s.Create(context.Background(), Actor{ID: "owner"}, Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "target"}}, testParameters(), "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	job = m.wait(t, job.ID, "finished", "failed")
	if job.State != "finished" {
		t.Fatal("initial fixture failed")
	}
	resolver.mu.Lock()
	resolver.profile = TestProfile{ServiceTiers: []string{"openai:fast"}}
	resolver.mu.Unlock()
	if _, err = s.Retest(context.Background(), Actor{ID: "owner"}, job.ID, "synthetic-key", []string{"proto.base"}); err != nil {
		t.Fatal(err)
	}
	job = m.wait(t, job.ID, "finished", "failed")
	if runs[0].Load() != 2 || runs[1].Load() != 2 || runs[2].Load() != 1 || len(job.Results) != 3 || len(job.Target.Profile.ServiceTiers) != 1 {
		t.Fatal("profile change repeated unrelated scenarios or reused old tier evidence")
	}
}
