package detection

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestMissingCodexArtifactsKeepOfficialLayerPending(t *testing.T) {
	r, s, _ := codexTestSession(t, "https://fixture.example")
	if err := r.RegisterCodexCLI(context.Background(), filepath.Join(t.TempDir(), "missing-probe"), filepath.Join(t.TempDir(), "missing-cli")); err != nil {
		t.Fatal(err)
	}
	s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
	item := codexLayerItem(t, r, s, "codex-special.RL13", "official_cli")
	if item.Implemented {
		t.Fatal("missing official artifact became executable")
	}
}

func TestOfficialCodexDetectorBridgeRecordsIndependentCLIProof(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	seen := map[string]int{}
	var seenMu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		defer seenMu.Unlock()
		seen[r.URL.Path]++
		if r.Header.Get("Authorization") != "Bearer synthetic-private-detector-key" {
			t.Error("private test Key boundary failed")
		}
		switch r.URL.Path {
		case "/backend-api/codex/models":
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model","display_name":"Synthetic model","description":"Isolated fixture","model_messages":{"instructions_template":"Use only the declared synthetic tools."},"supported_reasoning_levels":[],"shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,"support_verbosity":false,"truncation_policy":{"mode":"tokens","limit":10000},"experimental_supported_tools":[],"input_modalities":["text"],"supports_experimental_context":true,"context_window":32768}]}`)
		case "/backend-api/codex/alpha/notes/v2/thread_hint":
			_, _ = io.WriteString(w, `{"text":""}`)
		case "/backend-api/codex/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", codexprobe.Marker))
		default:
			t.Error("CLI escaped fixed detector paths", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
	s.Target.Model, s.Target.ModelType = "", ""
	if err := r.RegisterCodexGateway(); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"RL13", "RL15"} {
		item := codexLayerItem(t, r, s, "codex-special."+id, "official_cli")
		result := service.run(context.Background(), s, item, nil)
		if result.State != "pass" || result.Layer != "official_cli" || result.Source != "synthetic" {
			t.Fatalf("official CLI did not supply its own scoped proof: %+v", result)
		}
		if id == "RL13" && (result.Usage.Generate != 1 || result.Usage.InputTokens != 10 || result.Usage.OutputTokens != 4) {
			t.Fatal("actual CLI inference was omitted or counted twice", result.Usage)
		}
		if id == "RL15" && (result.Usage.Generate != 0 || result.Usage.InputTokens != 0 || result.Usage.OutputTokens != 0) {
			t.Fatal("catalog-only profile invented inference usage", result.Usage)
		}
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if seen["/backend-api/codex/alpha/notes/v2/thread_hint"] == 0 || seen["/backend-api/codex/responses"] != 1 {
		t.Fatal("notes did not activate, or catalog profile added inference calls", seen)
	}
}
