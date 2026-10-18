package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
)

func codexTestSession(t *testing.T, base string) (*Registry, *Session, *Service) {
	t.Helper()
	r, s, service := nativeTestSession(t, base)
	if err := r.RegisterCodexNative(); err != nil {
		t.Fatal(err)
	}
	return r, s, service
}

func TestCodexCatalogSharesExactUpstreamEvidenceWithoutClaimingOtherLayers(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/backend-api/codex/models" || r.URL.Query().Get("client_version") != "0.163.0" || r.Header.Get("Authorization") != "Bearer synthetic-private-detector-key" {
			t.Error("catalog probe used a different path/version/credential")
		}
		_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model","display_name":"private catalog text","supports_reasoning_summaries":true,"context_window":9999999}],"account":"private upstream account"}`)
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
	s.Target.Model, s.Target.ModelType = "", ""
	for _, req := range []string{"codex-special.RL13", "codex-special.RL15"} {
		item := nativeItem(t, r, s, req)
		result := service.run(context.Background(), s, item, nil)
		if result.State != "pass" || len(result.Assertions) != 1 || len(result.Settings) != 0 || len(result.Capacity) != 0 {
			t.Fatalf("native catalog supplied foreign-layer or declared-capability proof: %+v", result)
		}
		encoded, _ := json.Marshal(result)
		for _, private := range []string{"private catalog text", "private upstream account", "synthetic-private-detector-key", "9999999"} {
			if strings.Contains(string(encoded), private) {
				t.Fatal("raw catalog/credential entered report")
			}
		}
	}
	if calls != 1 || s.usage.HTTP != 1 {
		t.Fatal("identical auth/catalog request was repeated", calls)
	}
	plan, err := r.Plan(s.Target)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if slices.Contains(item.Requirements, "codex-special.RL13") && item.Layer != "upstream_protocol" && item.Implemented {
			t.Fatal("raw upstream proof claimed CLI/platform implementation", item)
		}
	}
	if !HasPending(plan) {
		t.Fatal("missing Codex scenarios disappeared from creation denominator")
	}
}

func TestCodexCatalogRejectsDuplicateAndMalformedModelIDs(t *testing.T) {
	for _, body := range []string{`{"models":[{"slug":"valid"},{"slug":"valid"}]}`, `{"models":[{"slug":"bad id"}]}`, `{"data":[{"id":"standard-models-is-not-codex"}]}`} {
		t.Run(body, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer up.Close()
			r, s, service := codexTestSession(t, up.URL)
			s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
			result := service.run(context.Background(), s, nativeItem(t, r, s, "codex-special.RL15"), nil)
			if result.State != "fail" || result.Reason != "assertion_failed" {
				t.Fatal("malformed catalog counted as supported", result)
			}
		})
	}
}

func TestCodexCompressionUsesNativePathAndIndependentPlainRequest(t *testing.T) {
	for _, rejectCompressed := range []bool{false, true} {
		t.Run(map[bool]string{false: "supported", true: "unsupported"}[rejectCompressed], func(t *testing.T) {
			seen := map[string]int{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/backend-api/codex/responses" && r.URL.Path != "/v1/responses" {
					t.Error("unknown compression test path")
				}
				encoding := r.Header.Get("Content-Encoding")
				seen[r.URL.Path+":"+encoding]++
				wire, _ := io.ReadAll(r.Body)
				plain, err := nativeencoding.Decode(r.Context(), encoding, wire)
				if err != nil || !strings.Contains(string(plain), nativeMarker) {
					t.Error("invalid actual compressed payload", err)
				}
				if encoding == "zstd" && rejectCompressed && r.URL.Path != "/v1/responses" {
					w.WriteHeader(415)
					_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"unsupported_parameter","message":"unsupported content encoding"}}`)
					return
				}
				_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", nativeMarker, nil))
			}))
			defer up.Close()
			r, s, service := codexTestSession(t, up.URL)
			s.resolved.Headers.Set = []market.HeaderSet{{Name: "Content-Encoding", Value: "gzip"}, {Name: "Content-Length", Value: "1"}}
			item := nativeItem(t, r, s, "codex-special.RL14")
			result := service.run(context.Background(), s, item, nil)
			if len(result.Assertions) != 5 || result.Assertions[2].State != "pass" || result.Assertions[3].State != "pass" || result.Assertions[4].State != "pass" || seen["/backend-api/codex/responses:"] != 1 || seen["/backend-api/codex/responses:zstd"] != 1 || seen["/v1/responses:zstd"] != 1 || seen["/v1/responses:"] != 1 {
				t.Fatalf("compression/plain observations collapsed: %+v seen=%v", result, seen)
			}
			want := "pass"
			if rejectCompressed {
				want = "unsupported"
			}
			if result.Assertions[0].State != want || result.Assertions[1].State != want {
				t.Fatal("wrong actual compression verdict", result)
			}
			// Reusing this exact request later neither calls nor meters it twice.
			second := service.run(context.Background(), s, item, nil)
			if !rejectCompressed && (second.State != "pass" || second.Usage.HTTP != 0 || second.Usage.InputTokens != 0 || second.Usage.OutputTokens != 0) {
				t.Fatal("shared witness was called/metered twice", second)
			}
		})
	}
}

func TestCodexActualTiersDoNotInferRequestedTierFromAcceptance(t *testing.T) {
	for _, actual := range []string{"priority", "default", "unknown-private-tier"} {
		t.Run(actual, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				requested := str(body["service_tier"])
				if requested != "default" && requested != "priority" {
					t.Error("wrong enabled native tier", requested)
				}
				o := nativeFixtureWithCalls("openai-responses", nativeMarker, nil)
				o["service_tier"] = requested
				if requested == "priority" {
					o["service_tier"] = actual
				}
				_ = json.NewEncoder(w).Encode(o)
			}))
			defer up.Close()
			r, s, service := codexTestSession(t, up.URL)
			s.Target.Profile.ServiceTiers = []string{"openai:fast", "gemini:flex"}
			result := service.run(context.Background(), s, nativeItem(t, r, s, "codex-special.RL24"), nil)
			if len(result.Settings) != 2 || calls != 2 || result.Assertions[0].State != "pass" {
				t.Fatal("tier acceptance/configuration evidence missing", result)
			}
			setting := result.Settings[1]
			if actual == "priority" {
				if result.State != "pass" || setting.State != "pass" || setting.Actual != "openai:fast" {
					t.Fatal("actual priority receipt lost", result)
				}
			} else if result.State == "pass" || setting.State == "pass" || !setting.Accepted || result.Assertions[1].State == "pass" {
				t.Fatal("accepted/downgraded tier was reported as actual fast", result)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "unknown-private-tier") || strings.Contains(string(encoded), nativeMarker) {
				t.Fatal("private tier/text persisted")
			}
		})
	}
}
