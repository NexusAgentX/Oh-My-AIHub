package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestServeCompressedResponsesKeepsOpaqueBytes(t *testing.T) {
	const input = `{"input":[{"type":"reasoning","id":"opaque-id","encrypted_content":"opaque-secret"}], "model" : "gpt-test", "store":false}`
	seen := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		wire, _ := io.ReadAll(r.Body)
		body, decodeErr := nativeencoding.Decode(r.Context(), r.Header.Get("Content-Encoding"), wire)
		if decodeErr != nil || r.Header.Get("Content-Encoding") != "zstd" || r.ContentLength != int64(len(wire)) {
			t.Errorf("incoherent request encoding: %v length %d/%d", decodeErr, r.ContentLength, len(wire))
		}
		want := strings.Replace(input, `"gpt-test"`, `"upstream-model"`, 1)
		if string(body) != want {
			t.Error("model splice changed opaque/native bytes")
		}
		if r.Header.Get("Digest") != "" || r.Header.Get("Authorization") != "Bearer sk-synthetic-upstream" {
			t.Error("digest or identity leaked into provider request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"fixture-response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()
	run := newCredentialRun(t, 200)
	store := &protocolErrorStore{candidate: Candidate{ChannelID: "upstream", OwnerID: "provider", BaseURL: upstream.URL, UpstreamModel: "upstream-model", MultiplierNano: ledger.UnitRate, Credential: run.seal("upstream", "sk-synthetic-upstream")}, fallbackCredential: run.seal("zz-fallback", "sk-synthetic-upstream")}
	store.candidate = quotedCandidate(t, store.candidate, catalog.Model{ID: "gpt-test"})
	store.candidate.Evidence = map[string]bool{"upstream_protocol/codex-special.RL14/public_zstd_request": true, "upstream_protocol/openai-responses.reasoning/complete_opaque_signature_or_encrypted_replay": true}
	store.candidate.Advanced.HeaderRules.Set = []channel.HeaderSet{{Name: "Content-Encoding", Value: "gzip"}, {Name: "Digest", Value: "stale"}}
	e := newServeFixture(t, 10, defaultRouting{}).engine
	e.Store = store
	e.Keyring = run.keyring
	e.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{AccountMaxInflightCalls: 10, DefaultMaxAttempts: 2}, outbound: fakeOutbound{transport: http.DefaultTransport}})
	wire, err := nativeencoding.Encode(context.Background(), "zstd", []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(wire))
	r.Header.Set("Authorization", "Bearer "+apikey.Prefix+"synthetic")
	r.Header.Set("Content-Encoding", "zstd")
	r.Header.Set("Digest", "invalid-after-splice")
	w := httptest.NewRecorder()
	e.Serve(w, r, channel.FormatOpenAIResponses, "", false)
	if w.Code != 200 || seen != 1 || len(store.finished) != 1 {
		t.Fatalf("status=%d upstream=%d finishes=%d body=%s", w.Code, seen, len(store.finished), w.Body.String())
	}
}
