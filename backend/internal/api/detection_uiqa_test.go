//go:build uiqa

package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/fingerprint"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

// Explicit opt-in synthetic HTTP harness for actual browser QA. It uses the
// real handlers/services and no database, host login, paid key or upstream.
func TestDetectionUIHarness(t *testing.T) {
	testDetectionUIHarness(t, false)
}

func TestCodexDetectionUIHarness(t *testing.T) {
	testDetectionUIHarness(t, true)
}

func testDetectionUIHarness(t *testing.T, codex bool) {
	if os.Getenv("AIHUB_DETECTION_UI_QA") != "1" {
		t.Skip("explicit synthetic UI harness only")
	}
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
	p := newPlatform(t, func(d *Dependencies) { d.Detection = service; d.CookieSecure = false })
	admin := &client{spec: p.spec, handler: p.handler}
	account := admin.expect(t, 201, http.MethodPost, "/api/instance/initialize", map[string]string{"username": "qaadmin", "display_name": "合成验收管理员", "password": "Synthetic-QA-2026"})["account"].(map[string]any)
	owner := account["id"].(string)
	store.owner = owner
	targetID, _ := ids.NewUUID()
	job, err := service.Start(context.Background(), detection.Actor{ID: owner, Admin: true}, detection.Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: targetID}}, "synthetic-ui-key")
	if err != nil {
		t.Fatal(err)
	}
	job.State = "finished"
	job.ExecutionActive = false
	job.Target.Model = "demo-text-2026"
	job.Usage = detection.Usage{HTTP: 8, Generate: 3, Count: 1, Query: 2, Cleanup: 2, InputTokens: 1234, OutputTokens: 56}
	job.Results = []detection.Result{}
	if codex {
		items := []detection.Planned{}
		for _, item := range job.Plan.Items {
			if strings.HasPrefix(item.ID, "codex-special.RL09") || item.ID == "codex-special.RL21@official_cli" {
				items = append(items, item)
			}
		}
		if len(items) != 4 {
			t.Fatal("Codex report fixture lost the three actual proof layers")
		}
		job.Plan.Items = items
		job.Target.Model = "codex-synthetic-2026"
	}
	for i := 0; i < 4; i++ {
		item := job.Plan.Items[i]
		item.Implemented = true
		job.Plan.Items[i] = item
		id, _ := ids.NewUUID()
		state := "pass"
		reason := "ok"
		if i == 2 {
			state = "unsupported"
			reason = "unsupported"
		}
		result := detection.Result{SourceTaskID: job.ID, ID: id, ScenarioID: item.ID, State: state, Reason: reason, Source: "synthetic", Layer: item.Layer, Assertions: []detection.Assertion{}, Attempts: []detection.Attempt{}, StartedAt: job.CreatedAt, FinishedAt: job.CreatedAt.Add(time.Second)}
		for _, a := range item.Assertions {
			result.Assertions = append(result.Assertions, detection.Assertion{ID: a, State: state, Reason: reason, AttemptIDs: []string{}})
		}
		if codex {
			if item.ID == "codex-special.RL21@official_cli" {
				result.State, result.Reason = "inconclusive", "client_disabled"
				for k := range result.Assertions {
					result.Assertions[k].State, result.Assertions[k].Reason = "inconclusive", "client_disabled"
				}
			}
			result.Attempts = []detection.Attempt{{ID: "synthetic-receipt", EndpointID: "codex.responses", Method: "POST", Path: "/backend-api/codex/responses", Status: 200, Reason: "ok", Category: "generate", DurationMS: 12, Sent: true, ResponseReceived: true, At: job.CreatedAt}}
		} else {
			uiDetectionMetadata(t, &result, i, job.Parameters)
		}
		job.Results = append(job.Results, result)
	}
	job.UpdatedAt = job.CreatedAt.Add(time.Second)
	job, err = store.Finish(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	var mode atomic.Value
	mode.Store("normal")
	stopped := make(chan struct{})
	server := &http.Server{Addr: "127.0.0.1:18490", ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__uiqa/stop" && r.Method == "POST" {
			w.WriteHeader(204)
			select {
			case <-stopped:
			default:
				close(stopped)
			}
			return
		}
		if r.URL.Path == "/__uiqa/mode" && r.Method == "POST" {
			next := r.URL.Query().Get("value")
			mode.Store(next)
			if next == "processing" {
				store.mu.Lock()
				current := store.jobs[job.ID]
				current.State = "running"
				current.ExecutionActive = true
				store.jobs[job.ID] = current
				store.mu.Unlock()
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/api/detection/jobs" && r.Method == "GET" && mode.Load() == "empty" {
			writeJSON(w, 200, map[string]any{"items": []any{}, "next_cursor": nil})
			return
		}
		if mode.Load() == "error" && r.Method == "GET" && (r.URL.Path == "/api/detection/jobs" || r.URL.Path == "/api/admin/detection/search") {
			writeError(w, 503, "synthetic_unavailable", "合成测试：服务暂不可用")
			return
		}
		if mode.Load() == "forbidden" && r.Method == "GET" && r.URL.Path == "/api/detection/jobs/"+job.ID {
			writeError(w, 403, "forbidden", "无权查看此报告")
			return
		}
		p.handler.ServeHTTP(w, r)
	})
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			close(stopped)
		}
	}()
	fmt.Printf("UI_QA_READY task=%s report=%s target=%s api=http://127.0.0.1:18490\n", job.ID, *job.ReportID, targetID)
	<-stopped
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func uiDetectionMetadata(t *testing.T, r *detection.Result, index int, params detection.Parameters) {
	t.Helper()
	switch index {
	case 0:
		declared, accepted, actual := int64(32768), int64(16384), int64(120)
		r.Capacity = []detection.CapacityReport{{Kind: "context", Unit: "tokens", Declared: &declared, Settings: params.Context, ParameterAcceptedLower: &accepted, VerifiedLower: &actual, Completed: true, Stop: "unconfirmed", Steps: []detection.CapacityStep{{Requested: 4096, Round: 1, CapacityProbe: detection.CapacityProbe{Accepted: true, Semantic: true, Actual: &actual, RequestedApproximate: true, Reason: "ok", AttemptIDs: []string{}}}}}}
	case 1:
		samples := []fingerprint.Sample{}
		for i := range 3 {
			var b strings.Builder
			for j := range 300 {
				fmt.Fprintf(&b, "%d ", (i*7+j*13)%100+1)
			}
			samples = append(samples, fingerprint.Sample{Text: b.String(), ExpectedCount: 300})
		}
		fp, err := fingerprint.Analyze(samples)
		if err != nil {
			t.Fatal(err)
		}
		r.Fingerprint = &fp
	case 2:
		r.Images = []detection.ImageEvidence{{Variant: detection.ImageVariant{Unit: "successful_image", Size: "256x256", Quality: "high"}, Transport: "json", State: "inconclusive", Reason: "not_observed", Requested: 1, Returned: 1, Verified: 1, Products: []detection.ImageProperties{{Width: 256, Height: 256, Format: "png"}}, UnpricedTokens: true, AttemptIDs: []string{}}}
	case 3:
		r.Settings = []detection.SettingEvidence{{Kind: "service_tier", Configured: "anthropic:priority", Wire: "auto", Actual: "anthropic:standard", State: "inconclusive", Reason: "not_observed", Accepted: true, UsageReliable: true, AttemptIDs: []string{}}, {Kind: "cache", Configured: "5m", Wire: "5m", State: "pass", Reason: "ok", Accepted: true, UsageReliable: true, CacheWriteTokens: 2048, CacheReadTokens: 2048, AttemptIDs: []string{}}}
		r.Quotas = []detection.QuotaEvidence{{Header: "x-ratelimit-remaining-tokens", Remaining: 10000, AttemptID: "10000000-0000-4000-8000-000000000099"}}
	}
}
