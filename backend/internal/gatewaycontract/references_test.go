package gatewaycontract_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func TestNativeContentsRoundTripWithoutResourceRegistration(t *testing.T) {
	for _, tc := range []struct{ protocol, input string }{
		{"openai-chat-completions", `{"model":"alias","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,fixture"}},{"type":"input_audio","input_audio":{"data":"fixture","format":"wav"}},{"type":"file","file":{"filename":"synthetic.pdf","file_data":"data:application/pdf;base64,fixture"}}]}],"prompt_cache_key":"not-a-resource"}`},
		{"openai-responses", `{"model":"alias","store":false,"input":[{"type":"reasoning","id":"unregistered","encrypted_content":"opaque"},{"type":"compaction","id":"also-unregistered","encrypted_content":"opaque-window"},{"role":"user","content":[{"type":"input_file","filename":"synthetic.pdf","file_data":"data:application/pdf;base64,fixture"},{"type":"input_image","image_url":"https://example.invalid/synthetic.png"}]}],"unknown":{"previous_response_id":"extension-must-not-be-interpreted"}}`},
		{"native-messages", `{"model":"alias","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"synthetic","signature":"opaque-signature"}]},{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"fixture"}}]}]}`},
		{"native-generate-content", `{"model":"alias","contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"audio/wav","data":"fixture"}},{"fileData":{"mimeType":"video/mp4","fileUri":"https://example.invalid/synthetic.mp4"}},{"text":"fixture","thoughtSignature":"opaque-signature"}]}]}`},
		{"codex-special", `{"model":"alias","prompt":"synthetic edit","images":[{"image_url":"data:image/png;base64,fixture"}],"background":"opaque","quality":"auto","size":"auto"}`},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			input := []byte(tc.input)
			refs, err := gatewaycontract.RequestReferences(tc.protocol, input)
			if err != nil || len(refs) != 0 {
				t.Fatal(refs, err)
			}
			want := bytes.Replace(input, []byte(`"alias"`), []byte(`"vendor/model:v1"`), 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(io.LimitReader(r.Body, 4096))
				if err != nil || !bytes.Equal(got, want) {
					t.Error("native content changed")
				}
				_, _ = w.Write(got)
			}))
			defer upstream.Close()
			_, scan, err := gateway.ScanRequest(input)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := gateway.UpstreamBody(input, scan, "vendor/model:v1", false)
			res, err := http.Post(upstream.URL, "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			got, err := io.ReadAll(io.LimitReader(res.Body, 4096))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("JSON round trip changed native content")
			}
		})
	}
}

func TestOnlyExplicitStandardReferencesConstrainRouting(t *testing.T) {
	for _, protocol := range []string{"openai-responses", "codex-special"} {
		refs, err := gatewaycontract.RequestReferences(protocol, []byte(`{"previous_response_id":"resp-owned","conversation":{"id":"conv-owned"},"input":[{"type":"item_reference","id":"item-owned"},{"type":"reasoning","id":"opaque-id","encrypted_content":"opaque"}]}`))
		if err != nil || len(refs) != 3 || refs[0].Kind != "response" || refs[1].Kind != "conversation" || refs[2].Kind != "item" {
			t.Fatal(refs, err)
		}
	}
	refs, err := gatewaycontract.RequestReferences("native-generate-content", []byte(`{"cachedContent":"cachedContents/owned"}`))
	if err != nil || len(refs) != 1 || refs[0].Kind != "cache" {
		t.Fatal(refs, err)
	}
	for _, body := range []string{`null`, `[]`, `{"previous_response_id":123}`, `{"conversation":{}}`, `{"input":[{"type":"item_reference"}]}`} {
		if _, err := gatewaycontract.RequestReferences("openai-responses", []byte(body)); err == nil {
			t.Fatal("invalid reference accepted", body)
		}
	}
	var oversized []map[string]string
	for range 129 {
		oversized = append(oversized, map[string]string{"type": "item_reference", "id": "item"})
	}
	body, _ := json.Marshal(map[string]any{"input": oversized})
	if _, err := gatewaycontract.RequestReferences("openai-responses", body); err == nil {
		t.Fatal("unbounded reference list")
	}
}

func TestCompactWindowCanContinueOnAnotherChannelInOrder(t *testing.T) {
	window := json.RawMessage(`[{"type":"message","role":"user","content":"synthetic-before"},{"type":"compaction","id":"internal-unregistered","encrypted_content":"opaque-window"},{"type":"message","role":"user","content":"synthetic-after"}]`)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			t.Error("wrong native compact path")
		}
		_, _ = w.Write([]byte(`{"output":` + string(window) + `}`))
	}))
	defer first.Close()
	res, err := http.Post(first.URL+"/v1/responses/compact", "application/json", bytes.NewBufferString(`{"model":"m","input":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var compact struct {
		Output json.RawMessage `json:"output"`
	}
	err = json.NewDecoder(res.Body).Decode(&compact)
	_ = res.Body.Close()
	if err != nil || !bytes.Equal(compact.Output, window) {
		t.Fatal("compact window changed")
	}
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Input json.RawMessage `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || !bytes.Equal(input.Input, window) {
			t.Error("complete compaction window reordered or truncated")
		}
		_, _ = io.WriteString(w, `{"id":"resp-new"}`)
	}))
	defer second.Close()
	body := []byte(`{"model":"m","store":false,"input":` + string(compact.Output) + `}`)
	refs, err := gatewaycontract.RequestReferences("openai-responses", body)
	if err != nil || len(refs) != 0 {
		t.Fatal("compaction created source binding", refs, err)
	}
	res, err = http.Post(second.URL+"/v1/responses", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
}

func TestExcludedResourcesDoNotGainSharedCredentialAuthorization(t *testing.T) {
	for _, tc := range []struct{ protocol, body string }{
		{"openai-responses", `{"input":[{"type":"input_file","file_id":"file-other"}]}`},
		{"codex-special", `{"model":"image-model","images":[{"file_id":"file-other"}],"prompt":"synthetic"}`},
		{"openai-responses", `{"tools":[{"type":"file_search","vector_store_ids":["vs-other"]}]}`},
		{"openai-responses", `{"tools":[{"type":"code_interpreter","container":"cntr-other"}]}`},
		{"openai-chat-completions", `{"messages":[{"role":"user","content":[{"type":"file","file":{"file_id":"file-other"}}]}]}`},
		{"native-messages", `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"file","file_id":"file-other"}}]}]}`},
		{"native-generate-content", `{"contents":[{"parts":[{"fileData":{"fileUri":"https://generativelanguage.googleapis.com/v1beta/files/other"}}]}]}`},
	} {
		refs, err := gatewaycontract.RequestReferences(tc.protocol, []byte(tc.body))
		if err != nil || len(refs) != 1 {
			t.Fatal(refs, err)
		}
		if _, err := gatewaycontract.ResolveReferences(gatewaycontract.Principal{AccountID: "a", KeyID: "k", Active: true}, refs, func(gatewaycontract.Reference) ([]gatewaycontract.Resource, error) {
			if refs[0].Kind != "container" {
				t.Fatal("excluded resource requested ownership lookup")
			}
			return nil, nil
		}); err == nil {
			t.Fatal("unknown excluded resource acquired shared access")
		}
	}
	refs, err := gatewaycontract.RequestReferences("openai-responses", []byte(`{"tools":[{"type":"function","name":"f","parameters":{"properties":{"file_id":{"type":"string"}}}}],"extension":{"file_id":"plain-data"}}`))
	if err != nil || len(refs) != 0 {
		t.Fatal("tool schema interpreted as remote resource", refs, err)
	}
}
