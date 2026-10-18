package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
)

func TestOfficialCodexImagesGenerateEditUseIndependentModelAndActualProducts(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var mu sync.Mutex
	inferences, generated, edited := 0, 0, 0
	const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=="
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-text","input_modalities":["text"]}]}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		body, err := decodeNative(raw)
		if err != nil {
			t.Error("CLI image request is not bounded native JSON", err)
		}
		switch r.URL.Path {
		case "/backend-api/codex/alpha/notes/v2/thread_hint":
			_, _ = io.WriteString(w, `{"text":""}`)
		case "/backend-api/codex/responses":
			inferences++
			if str(body["model"]) != "synthetic-text" {
				t.Error("image quote/model was used for main conversation")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if inferences == 1 || inferences == 3 {
				args := nativeObject{"prompt": "Synthetic blue circle"}
				call := "image-generate"
				if inferences == 3 {
					args["prompt"], args["num_last_images_to_include"], call = "Synthetic green circle", 1, "image-edit"
				}
				_, _ = io.WriteString(w, codexCallStreamFixture(nativeCall{ID: call, Namespace: "image_gen", Name: "imagegen"}, args))
			} else {
				_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", codexprobe.Marker))
			}
		case "/backend-api/codex/images/generations", "/backend-api/codex/images/edits":
			if str(body["model"]) != "synthetic-image" {
				t.Error("native image request borrowed main model", str(body["model"]))
			}
			if strings.HasSuffix(r.URL.Path, "/edits") {
				edited++
				if !strings.Contains(string(raw), pixel) || strings.Contains(string(raw), "referenced_image_paths") {
					t.Error("edit did not use actual prior inline image")
				}
			} else {
				generated++
			}
			_ = json.NewEncoder(w).Encode(nativeObject{"created": 1, "data": []any{nativeObject{"b64_json": pixel, "generation_id": "synthetic-product"}}})
		default:
			t.Error("image CLI accessed a local file/resource endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	s.Target.Model, s.Target.ModelType = "synthetic-image", "image"
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL25", "official_cli"), nil)
	mu.Lock()
	defer mu.Unlock()
	if result.State != "pass" || inferences != 4 || generated != 1 || edited != 1 || result.Usage.Images != 2 {
		var proof codexCLIProof
		_ = json.Unmarshal(s.Recall("codex-cli-profile-v1:image_lifecycle"), &proof)
		t.Logf("semantic image metadata client=%+v wire=%+v", proof.Client, proof.Wire)
		t.Fatal("native CLI image lifecycle incomplete", result, inferences, generated, edited)
	}
}

func TestCLIImageGuardRejectsHostPathsAndUncontrolledTools(t *testing.T) {
	base := nativeObject{"output": []any{nativeObject{"type": "function_call", "namespace": "image_gen", "name": "imagegen", "arguments": `{"prompt":"Synthetic image"}`}}}
	if !safeCLIImageOutput(base) {
		t.Fatal("synthetic in-memory image tool rejected")
	}
	optional := cloneNative(base)
	object(list(optional["output"])[0])["arguments"] = `{"prompt":"Synthetic image","referenced_image_paths":[],"num_last_images_to_include":null}`
	if !safeCLIImageOutput(optional) {
		t.Fatal("safe optional empty values were confused with host file paths")
	}
	for _, args := range []string{`{"prompt":"x","referenced_image_paths":["/private/host"]}`, `{"prompt":"x","num_last_images_to_include":5}`, `{"prompt":"x","extra":true}`} {
		copy := cloneNative(base)
		object(list(copy["output"])[0])["arguments"] = args
		if safeCLIImageOutput(copy) {
			t.Fatal("uncontrolled image input accepted")
		}
	}
	copy := cloneNative(base)
	object(list(copy["output"])[0])["name"] = "shell_command"
	if safeCLIImageOutput(copy) {
		t.Fatal("undeclared client command accepted")
	}
}
