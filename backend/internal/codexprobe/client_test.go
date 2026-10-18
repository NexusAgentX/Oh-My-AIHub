package codexprobe

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeRejectsHostLoginAndExternalBridge(t *testing.T) {
	home := filepath.Join(t.TempDir(), "aihub-detection-client-synthetic")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	base := Config{Origin: "https://127.0.0.1:1234", Token: "synthetic-bridge-token", Model: "synthetic/model", Profile: "notes", CA: "bridge-ca.pem"}
	if base.validate() != nil {
		t.Fatal("valid isolated bridge refused")
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Origin = "https://provider.example:443" },
		func(c *Config) { c.Origin = "https://127.0.0.1:1234/other" },
		func(c *Config) { c.Origin = "https://username@127.0.0.1:1234" },
		func(c *Config) { c.Model = "model\nmalformed = true" },
		func(c *Config) { c.Profile = "arbitrary-command" },
		func(c *Config) { c.CA = "../host.pem" },
		func(c *Config) { c.CA = "/private/host.pem" },
	} {
		c := base
		mutate(&c)
		if c.validate() == nil {
			t.Fatal("unsafe bridge configuration accepted")
		}
	}
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	if base.validate() == nil {
		t.Fatal("ordinary host login profile accepted")
	}
}

func TestDynamicProbeToolRejectsArbitraryInputsAndReturnsOnlyFixedResult(t *testing.T) {
	var output bytes.Buffer
	d := dialogue{in: &output, tools: true}
	for _, params := range []string{
		`{"tool":"shell_command","arguments":{"command":"anything"}}`,
		`{"tool":"synthetic_echo","arguments":{"label":"violet","meta":{"ok":true},"items":[{"value":8}]}}`,
	} {
		if d.notification(rpcMessage{ID: json.RawMessage(`91`), Method: "item/tool/call", Params: json.RawMessage(params)}) == nil || output.Len() != 0 {
			t.Fatal("uncontrolled tool or wrong semantic arguments accepted")
		}
	}
	params := json.RawMessage(`{"tool":"synthetic_echo","arguments":{"label":"violet","meta":{"ok":true},"items":[{"value":7}]}}`)
	if d.notification(rpcMessage{ID: json.RawMessage(`91`), Method: "item/tool/call", Params: params}) != nil || d.toolCalls != 1 || !strings.Contains(output.String(), Marker) {
		t.Fatal("fixed synthetic callback did not complete")
	}
}

func TestDialogueCannotUseAnotherThreadOrOldTurnAsProof(t *testing.T) {
	d := dialogue{thread: "own-thread", turn: "current-turn"}
	for _, scope := range [][2]string{{"other-thread", "current-turn"}, {"own-thread", "past-turn"}} {
		params, _ := json.Marshal(map[string]any{"threadId": scope[0], "turnId": scope[1], "item": map[string]string{"type": "agentMessage", "text": Marker}})
		if err := d.notification(rpcMessage{Method: "item/completed", Params: params}); err != nil || d.matched {
			t.Fatal("foreign context completion entered this turn's evidence", err)
		}
	}
	params, _ := json.Marshal(map[string]any{"threadId": "own-thread", "turnId": "current-turn", "item": map[string]string{"type": "agentMessage", "text": Marker}})
	if err := d.notification(rpcMessage{Method: "item/completed", Params: params}); err != nil || !d.matched {
		t.Fatal("matching turn result was discarded", err)
	}
	if err := d.notification(rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"own-thread","turn":{"id":"past-turn","status":"completed"}}`)}); err != nil || d.completed {
		t.Fatal("old turn completed the current probe", err)
	}
}

func TestDialogueRejectsClientSideCommandsAndKeepsOnlySemanticFacts(t *testing.T) {
	d := dialogue{}
	if d.notification(rpcMessage{ID: json.RawMessage(`7`), Method: "item/commandExecution/requestApproval"}) == nil {
		t.Fatal("unexpected command approval allowed")
	}
	params, _ := json.Marshal(map[string]any{"item": map[string]string{"type": "agentMessage", "text": Marker}})
	if d.notification(rpcMessage{Method: "item/completed", Params: params}) != nil || !d.matched {
		t.Fatal("semantic marker not recognized")
	}
	if d.notification(rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"turn":{"status":"failed","error":{"message":"private upstream content"}}}`)}) == nil {
		t.Fatal("failed official turn accepted")
	}
	metadata, _ := json.Marshal(Result{Completed: true, MarkerMatched: d.matched, Stage: "completed"})
	if strings.Contains(string(metadata), Marker) || strings.Contains(string(metadata), "private upstream content") {
		t.Fatal("raw client content entered probe metadata")
	}
	var out versionOutput
	if _, err := out.Write(make([]byte, 1025)); err == nil || out.Len() != 0 {
		t.Fatal("version output exceeded its allocation bound")
	}
}
