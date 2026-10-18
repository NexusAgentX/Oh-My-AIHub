package detection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodexSearchConsumesActualOpaqueResultAndNeverFetchesURLs(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/backend-api/codex/alpha/search":
			if !strings.Contains(string(raw), `"model":"synthetic-model"`) {
				t.Error("search lost explicit model context")
			}
			_, _ = io.WriteString(w, `{"output":"Opaque search result.","encrypted_output":"private-search+cipher==","results":[{"type":"text_result","url":"https://docs.example/first","unknown":{"preserve":true}}]}`)
		case "/backend-api/codex/responses":
			if !strings.Contains(string(raw), `"type":"encrypted_content"`) || !strings.Contains(string(raw), "private-search+cipher==") || strings.Contains(string(raw), "https://docs.example/first") {
				t.Error("search follow-up bypassed actual encrypted result")
			}
			_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", "https://docs.example/first", nil))
		default:
			t.Fatal("detector fetched a returned URL or another endpoint")
		}
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	result := service.run(context.Background(), s, nativeItem(t, r, s, "codex-special.RL16"), nil)
	if result.State != "pass" || calls != 2 || result.Usage.Query != 1 || result.Usage.Generate != 1 {
		t.Fatal("native search did not complete its actual continuation", result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private-search") || strings.Contains(string(encoded), "docs.example") {
		t.Fatal("search response/cipher/URLs entered persisted metadata")
	}
}

func TestCodexNativeImagesHaveOwnEndpointsAndGatewayImageModelACL(t *testing.T) {
	image := base64.StdEncoding.EncodeToString(nativeFixture("image-a.png"))
	for _, layer := range []string{"upstream_protocol", "gateway_proxy"} {
		t.Run(layer, func(t *testing.T) {
			calls := map[string]int{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls[r.URL.Path]++
				if r.URL.Path == "/backend-api/codex/models" {
					_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-image-model"}]}`)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				if str(body["model"]) != "synthetic-image-model" || body["mask"] != nil || body["output_format"] != nil || !strings.HasPrefix(r.URL.Path, "/backend-api/codex/images/") {
					t.Error("native image request used public API shape, text model or another endpoint")
				}
				if strings.HasSuffix(r.URL.Path, "/edits") && len(list(body["images"])) != 2 {
					t.Error("native edit lost inline images")
				}
				_ = json.NewEncoder(w).Encode(nativeObject{"created": 1, "data": []any{nativeObject{"b64_json": image, "generation_id": "private-image"}}, "usage": nativeObject{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}})
			}))
			defer up.Close()
			r, s, service := codexTestSession(t, up.URL)
			s.Target.Model, s.Target.ModelType = "synthetic-image-model", "image"
			if err := r.RegisterCodexGateway(); err != nil {
				t.Fatal(err)
			}
			result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL25", layer), nil)
			wantAssertions := 3
			if layer == "gateway_proxy" {
				wantAssertions = 4
			}
			if result.State != "pass" || len(result.Assertions) != wantAssertions || result.Usage.Images != 2 || result.Usage.InputTokens != 4 || result.Usage.OutputTokens != 6 || calls["/backend-api/codex/images/generations"] != 1 || calls["/backend-api/codex/images/edits"] != 1 {
				t.Fatalf("image scope/permission/products failed: %+v calls=%v", result, calls)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), image) || strings.Contains(string(encoded), "private-image") {
				t.Fatal("image body/identifier entered report")
			}
		})
	}
}
