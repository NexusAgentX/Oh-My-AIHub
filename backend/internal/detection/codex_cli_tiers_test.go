package detection

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
)

func TestOfficialCodexTiersVerifyActualWireAndDowngradeInsteadOfCatalogClaims(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	for _, downgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "matched", true: "downgraded"}[downgrade], func(t *testing.T) {
			var mu sync.Mutex
			seen := map[string]int{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/backend-api/codex/models" {
					_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
					return
				}
				if r.URL.Path != "/backend-api/codex/responses" {
					t.Error("tier proof used another path", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				requested := str(body["service_tier"])
				if requested != "" && requested != "priority" {
					t.Error("CLI sent unconfigured or invented tier", requested)
				}
				mu.Lock()
				seen[requested]++
				mu.Unlock()
				actual := requested
				if actual == "" || downgrade {
					actual = "default"
				}
				stream := strings.ReplaceAll(responseStreamFixture(), "你好 🌍", codexprobe.Marker)
				stream = strings.ReplaceAll(stream, `"object":"response"`, `"object":"response","service_tier":"`+actual+`"`)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, stream)
			}))
			defer up.Close()
			r, s, service := codexTestSession(t, up.URL)
			s.Target.Profile.ServiceTiers = []string{"openai:fast"}
			if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
				t.Fatal(err)
			}
			result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL24", "official_cli"), nil)
			if result.State == "invalid" || len(result.Settings) != 2 || result.Settings[0].Wire != "omitted" || !result.Settings[0].Accepted || result.Settings[1].Wire != "priority" || !result.Settings[1].Accepted || result.Usage.Generate != 2 {
				t.Fatal("actual CLI tier selection was not recorded", result)
			}
			fast := result.Settings[1]
			if downgrade {
				if fast.State != "inconclusive" || fast.Actual != "openai:default" || result.State == "pass" {
					t.Fatal("catalog/config request became actual fast service", result)
				}
			} else if result.State != "pass" || fast.State != "pass" || fast.Actual != "openai:fast" {
				t.Fatal("actual supported tier was lost", result)
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[""] != 1 || seen["priority"] != 1 {
				t.Fatal("default sentinel was fabricated or tiers duplicated", seen)
			}
		})
	}
}
