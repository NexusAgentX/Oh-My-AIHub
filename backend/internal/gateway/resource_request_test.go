package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func TestNativeReferenceScanPreservesProtocolSemanticsWithoutWalkingOpaqueContents(t *testing.T) {
	for _, tc := range []struct{ protocol, body string }{
		{"openai-responses", `{"model":"m","previous_response_id":"r","conversation":{"id":"c"},"input":[{"type":"item_reference","id":"i"},{"type":"reasoning","id":"opaque","encrypted_content":"cipher"},{"type":"message","content":[{"type":"input_file","file_id":"f"}]}],"tools":[{"type":"code_interpreter","container":{"type":"auto","file_ids":["f2"]}}]}`},
		{"openai-chat-completions", `{"model":"m","messages":[{"role":"assistant","audio":{"id":"audio-1"}},{"role":"user","content":[{"type":"file","file":{"file_id":"f"}}]}],"unknown":{"file_id":"plain"}}`},
		{"native-messages", `{"model":"m","container":"cntr-1","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"text","signature":"opaque-id"}]},{"role":"user","content":[{"type":"document","source":{"file_id":"f"}}]}]}`},
		{"native-generate-content", `{"cachedContent":"cachedContents/own","contents":[{"parts":[{"thoughtSignature":"opaque","fileData":{"fileUri":"https://generativelanguage.googleapis.com/v1beta/files/own"}}]}]}`},
	} {
		want, err := gatewaycontract.RequestReferences(tc.protocol, []byte(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		got, err := nativeRequestReferences(tc.protocol, []byte(tc.body))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v err %v want %v", tc.protocol, got, err, want)
		}
	}
	escaped := []byte(`{"previous_response_id":"own","previous_\u0072esponse_id":"foreign"}`)
	if _, err := nativeRequestReferences("openai-responses", escaped); err == nil {
		t.Fatal("duplicate escaped reference created ambiguous upstream interpretation")
	}
	got, err := nativeRequestReferences("openai-chat-completions", []byte(`{"mes\u0073ages":[{"role":"user","content":[{"type":"file","file":{"file_\u0069d":"foreign"}}]}]}`))
	if err != nil || len(got) != 1 || got[0].Kind != "file" {
		t.Fatal("escaped reference bypassed authorization", got, err)
	}
}
func BenchmarkNativeReferencesLongStatelessPrompt(b *testing.B) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("x", 1<<20) + `"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"file_id":{"type":"string"}}}}}]}`)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		refs, err := nativeRequestReferences("openai-chat-completions", body)
		if err != nil || len(refs) != 0 {
			b.Fatal(refs, err)
		}
	}
}
