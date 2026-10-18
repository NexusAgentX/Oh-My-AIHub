package codexprobe

import (
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
