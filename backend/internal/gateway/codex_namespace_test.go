package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func TestCodexNamespaceIsolatesOwnersAndPreservesNativeContents(t *testing.T) {
	root := bytes.Repeat([]byte{1}, 32)
	namespace, err := gatewaycontract.NewContextNamespace(root)
	if err != nil {
		t.Fatal(err)
	}
	actor := gatewaycontract.Principal{AccountID: "a", KeyID: "k", Active: true}
	body := []byte(`{ "prompt_cache_key":"client-session","context":{"session_id":"client-session","current_agent_name":"root"},"client_metadata":{"session_id":"client-session","thread_id":"client-thread","x-codex-turn-metadata":"{\"session_id\":\"client-session\",\"thread_id\":\"client-thread\",\"parent_thread_id\":null,\"extra\":7}"},"input":[{"type":"reasoning","id":"do-not-map","encrypted_content":"opaque+/=="},{"type":"compaction","id":"also-unchanged","encrypted_content":"full-window"}],"text":"client-session is ordinary content","unknown":{"session_id":"client-session"} }`)
	headers := http.Header{"session-id": {"client-session"}, "Thread-Id": {"client-thread"}, "X-Client-Request-Id": {"client-thread"}, "X-Codex-Turn-Metadata": {`{"session_id":"client-session","thread_id":"client-thread"}`}}
	got, next, err := RewriteCodexContext(body, headers, actor, namespace)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := namespace.Identifier(actor, "client-session")
	thread, _ := namespace.Identifier(actor, "client-thread")
	var parsed struct {
		Cache   string `json:"prompt_cache_key"`
		Context struct {
			Session string `json:"session_id"`
		}
		Client map[string]string `json:"client_metadata"`
	}
	if json.Unmarshal(got, &parsed) != nil || parsed.Cache != session || parsed.Context.Session != session || parsed.Client["session_id"] != session || parsed.Client["thread_id"] != thread || next.Get("Session-Id") != session || next.Get("Thread-Id") != thread || next.Get("X-Client-Request-Id") != thread {
		t.Fatal("context fields and headers disagree")
	}
	for _, raw := range []string{`"encrypted_content":"opaque+/=="`, `"id":"do-not-map"`, `"encrypted_content":"full-window"`, `"text":"client-session is ordinary content"`, `"unknown":{"session_id":"client-session"}`} {
		if !bytes.Contains(got, []byte(raw)) {
			t.Fatal("native content changed", raw)
		}
	}
	//lint:ignore SA1008 Deliberately inspect the noncanonical input key to verify it was not mutated.
	if headers["session-id"][0] != "client-session" {
		t.Fatal("input headers mutated")
	}
	// The root is copied and kept independent of encryption-key rotation and
	// directory updates. There is intentionally no channel/group argument.
	root[0] = 9
	restored, _ := gatewaycontract.NewContextNamespace(bytes.Repeat([]byte{1}, 32))
	again, _ := restored.Identifier(actor, "client-session")
	if again != session {
		t.Fatal("restart changed namespace")
	}
	for _, other := range []gatewaycontract.Principal{{AccountID: "b", KeyID: "k", Active: true}, {AccountID: "a", KeyID: "other-key", Active: true}} {
		foreign, _ := namespace.Identifier(other, "client-session")
		if foreign == session {
			t.Fatal("shared upstream could mix two owners")
		}
		// Submitting another owner's already-mapped ID is mapped again inside
		// this owner's namespace; it cannot be used to escape to that owner.
		guess, _ := namespace.Identifier(other, session)
		if guess == session {
			t.Fatal("guessed protected ID escaped namespace")
		}
	}
}

func TestCodexNamespaceRejectsAmbiguousOrInvalidContext(t *testing.T) {
	n, _ := gatewaycontract.NewContextNamespace(make([]byte, 32))
	actor := gatewaycontract.Principal{AccountID: "a", KeyID: "k", Active: true}
	for _, body := range []string{`{"context":{"session_id":"x","session_id":"y"}}`, `{"context":{"session_id":1}}`, `{"context":{"session_id":""}}`, `{"client_metadata":{"x-codex-turn-metadata":"not-json"}}`, `{"prompt_cache_key":"x","prompt_cache_key":"y"}`, `{"prompt_cache_key":1}`, `[]`} {
		if _, _, err := RewriteCodexContext([]byte(body), nil, actor, n); err == nil {
			t.Fatal("invalid namespace input accepted", body)
		}
	}
	for _, h := range []http.Header{{"Session-Id": {"x", "y"}}, {"Session-Id": {"x"}, "session-id": {"y"}}} {
		if _, _, err := RewriteCodexContext([]byte(`{}`), h, actor, n); err == nil {
			t.Fatal("ambiguous header accepted")
		}
	}
	actor.Active = false
	if _, _, err := RewriteCodexContext([]byte(`{}`), nil, actor, n); err == nil {
		t.Fatal("revoked actor could namespace context")
	}
}

func TestCodexLogicalWindowUsesSameProtectedThreadAndPreservesResourceIDs(t *testing.T) {
	n, _ := gatewaycontract.NewContextNamespace(bytes.Repeat([]byte{11}, 32))
	const thread = "019abcde-1234-7abc-8123-123456789abc"
	const contextWindow = "01234567-89ab-4cde-8123-0123456789ab"
	const window = thread + ":12"
	metadata := `{"session_id":"` + thread + `","thread_id":"` + thread + `","window_id":"` + window + `","context_window_id":"` + contextWindow + `"}`
	body, _ := json.Marshal(map[string]any{"client_metadata": map[string]string{"session_id": thread, "thread_id": thread, "x-codex-window-id": window, "x-codex-turn-metadata": metadata}, "window_id": window, "input": []any{map[string]string{"type": "compaction", "id": window, "encrypted_content": window}}, "notes": map[string]string{"path": window}})
	seen := map[string]bool{}
	for _, actor := range []gatewaycontract.Principal{{AccountID: "a", KeyID: "k", Active: true}, {AccountID: "b", KeyID: "k", Active: true}, {AccountID: "a", KeyID: "other", Active: true}} {
		mapped, _ := n.Identifier(actor, thread)
		got, headers, err := RewriteCodexContext(body, http.Header{"Session-Id": {thread}, "X-Codex-Window-Id": {window}, "X-Codex-Turn-Metadata": {metadata}}, actor, n)
		if err != nil {
			t.Fatal(err)
		}
		var root struct {
			Client map[string]string   `json:"client_metadata"`
			Window string              `json:"window_id"`
			Input  []map[string]string `json:"input"`
			Notes  map[string]string   `json:"notes"`
		}
		if json.Unmarshal(got, &root) != nil {
			t.Fatal("invalid rewritten native body")
		}
		var turn map[string]any
		_ = json.Unmarshal([]byte(root.Client["x-codex-turn-metadata"]), &turn)
		if root.Client["x-codex-window-id"] != mapped+":12" || turn["window_id"] != mapped+":12" || turn["thread_id"] != mapped || turn["context_window_id"] != contextWindow || headers.Get("X-Codex-Window-Id") != mapped+":12" || headers.Get("Session-Id") != mapped {
			t.Fatal("derived transport window broke protected thread identity")
		}
		if root.Window != window || root.Input[0]["id"] != window || root.Input[0]["encrypted_content"] != window || root.Notes["path"] != window {
			t.Fatal("opaque resource/content identifiers were rewritten")
		}
		if seen[headers.Get("X-Codex-Window-Id")] {
			t.Fatal("different owners could collide on the logical window")
		}
		seen[headers.Get("X-Codex-Window-Id")] = true
		out := http.Header{"X-Codex-Window-Id": {"shared-override"}}
		contextWireHeaders(out, headers)
		if out.Get("X-Codex-Window-Id") != mapped+":12" {
			t.Fatal("channel header rule undid logical window isolation")
		}
	}
	actor := gatewaycontract.Principal{AccountID: "a", KeyID: "k", Active: true}
	for _, opaque := range []string{"opaque-window", thread + ":future", contextWindow} {
		encoded, _ := json.Marshal(map[string]any{"client_metadata": map[string]string{"x-codex-window-id": opaque}})
		got, _, err := RewriteCodexContext(encoded, nil, actor, n)
		if err != nil || !bytes.Equal(got, encoded) {
			t.Fatal("nonlogical opaque window changed", err)
		}
	}
	if _, _, err := RewriteCodexContext([]byte(`{}`), http.Header{"X-Codex-Window-Id": {window, window}}, actor, n); err == nil {
		t.Fatal("ambiguous logical-window headers accepted")
	}
}

func BenchmarkCodexLogicalWindowNamespace(b *testing.B) {
	n, _ := gatewaycontract.NewContextNamespace(bytes.Repeat([]byte{11}, 32))
	actor := gatewaycontract.Principal{AccountID: "a", KeyID: "k", Active: true}
	body := []byte(`{"model":"m","prompt_cache_key":"019abcde-1234-7abc-8123-123456789abc","client_metadata":{"session_id":"019abcde-1234-7abc-8123-123456789abc","thread_id":"019abcde-1234-7abc-8123-123456789abc","x-codex-window-id":"019abcde-1234-7abc-8123-123456789abc:1"},"input":[]}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := RewriteCodexContext(body, nil, actor, n); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodexContextNamespace(b *testing.B) {
	n, _ := gatewaycontract.NewContextNamespace(make([]byte, 32))
	actor := gatewaycontract.Principal{AccountID: "a", KeyID: "k", Active: true}
	body := []byte(`{"model":"m","context":{"session_id":"s"},"input":[{"type":"reasoning","encrypted_content":"opaque"}]}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := RewriteCodexContext(body, nil, actor, n); err != nil {
			b.Fatal(err)
		}
	}
}

func TestNamespacedContextReadWriteAcrossEntriesWithSharedCredential(t *testing.T) {
	n, _ := gatewaycontract.NewContextNamespace(bytes.Repeat([]byte{7}, 32))
	var mu sync.Mutex
	notes := map[string]string{} // synthetic upstream storage, never gateway storage
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-shared-upstream-key" {
			w.WriteHeader(401)
			return
		}
		var body struct {
			Context struct {
				Session string `json:"session_id"`
			}
			Text string `json:"text"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/alpha/notes/v2/write_file" {
			notes[body.Context.Session] = body.Text
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": notes[body.Context.Session]})
	})
	a, b := httptest.NewServer(handler), httptest.NewServer(handler)
	defer a.Close()
	defer b.Close()
	first := gatewaycontract.Principal{AccountID: "a", KeyID: "ka", Active: true}
	second := gatewaycontract.Principal{AccountID: "b", KeyID: "kb", Active: true}
	forward := func(actor gatewaycontract.Principal, entry, operation, text string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"context": map[string]string{"session_id": "same-client-id", "current_agent_name": "root"}, "path": "root/notes/synthetic", "text": text})
		mapped, headers, err := RewriteCodexContext(body, nil, actor, n)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, entry+"/alpha/notes/v2/"+operation, bytes.NewReader(mapped))
		req.Header = headers
		req.Header.Set("Authorization", "Bearer synthetic-shared-upstream-key")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result struct {
			Text string `json:"text"`
		}
		if json.NewDecoder(res.Body).Decode(&result) != nil {
			t.Fatal("invalid fixture response")
		}
		return result.Text
	}
	forward(first, a.URL, "write_file", "synthetic-A")
	forward(second, a.URL, "write_file", "synthetic-B")
	if forward(first, b.URL, "read_file", "") != "synthetic-A" || forward(second, b.URL, "read_file", "") != "synthetic-B" {
		t.Fatal("same upstream credential mixed owners or source pinned context")
	}
}
