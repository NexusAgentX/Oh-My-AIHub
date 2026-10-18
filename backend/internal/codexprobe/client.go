// Package codexprobe drives the pinned official CLI in a parent-supplied,
// isolated process environment. It emits only bounded semantic metadata.
package codexprobe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

const Version = "0.163.0"
const Protocol = "aihub-codex-probe.v1"
const Marker = "AIHUB-CODEX-READY-6309"

var ErrClient = errors.New("isolated Codex client failed")

type versionOutput struct{ bytes.Buffer }

func (w *versionOutput) Write(p []byte) (int, error) {
	if len(p) > 1024-w.Len() {
		return 0, ErrClient
	}
	return w.Buffer.Write(p)
}

type Config struct {
	Binary  string
	Origin  string
	Token   string
	Model   string
	Profile string
	CA      string
}

type Result struct {
	Protocol      string `json:"protocol"`
	Version       string `json:"version"`
	Profile       string `json:"profile"`
	Completed     bool   `json:"completed"`
	MarkerMatched bool   `json:"marker_matched"`
	PlatformAuth  bool   `json:"platform_auth"`
	Stage         string `json:"stage"`
}

func CheckVersion(ctx context.Context, binary string) error {
	if !filepath.IsAbs(binary) {
		return ErrClient
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "CODEX_HOME=" + os.Getenv("CODEX_HOME")}
	var output versionOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	if err := cmd.Run(); err != nil || strings.TrimSpace(output.String()) != "codex-cli "+Version {
		return ErrClient
	}
	return nil
}

func (c Config) validate() error {
	u, err := url.Parse(c.Origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !net.ParseIP(u.Hostname()).IsLoopback() || u.Port() == "" || len(c.Token) < 16 || len(c.Token) > 256 || strings.ContainsAny(c.Token, "\r\n\x00") || !gatewaycontract.ValidModelID(c.Model) {
		return ErrClient
	}
	if c.Profile != "standard" && c.Profile != "notes" && c.Profile != "image" {
		return ErrClient
	}
	// The caller must be RunControlledProcess, which makes these private roots.
	// Refuse to use a user's ordinary login directory even when invoked by hand.
	home, codexHome := os.Getenv("HOME"), os.Getenv("CODEX_HOME")
	if !strings.HasPrefix(filepath.Base(home), "aihub-detection-client-") || codexHome != filepath.Join(home, "codex") {
		return ErrClient
	}
	if c.CA != "" && (filepath.Base(c.CA) != c.CA || c.CA == "." || c.CA == "..") {
		return ErrClient
	}
	return nil
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Params json.RawMessage `json:"params"`
	Error  json.RawMessage `json:"error"`
}

type dialogue struct {
	in        io.Writer
	lines     <-chan []byte
	next      int
	completed bool
	matched   bool
}

func (d *dialogue) receive(ctx context.Context) (rpcMessage, error) {
	select {
	case <-ctx.Done():
		return rpcMessage{}, ErrClient
	case line, ok := <-d.lines:
		if !ok {
			return rpcMessage{}, ErrClient
		}
		var msg rpcMessage
		if json.Unmarshal(line, &msg) != nil {
			return rpcMessage{}, ErrClient
		}
		return msg, nil
	}
}

func (d *dialogue) notification(msg rpcMessage) error {
	if len(msg.ID) > 0 && msg.Method != "" {
		// No approvals or arbitrary client RPCs are part of this fixed probe.
		return ErrClient
	}
	if msg.Method == "item/completed" {
		var p struct {
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(msg.Params, &p) != nil {
			return ErrClient
		}
		if p.Item.Type == "agentMessage" && strings.TrimSpace(p.Item.Text) == Marker {
			d.matched = true
		}
	}
	if msg.Method == "turn/completed" {
		var p struct {
			Turn struct {
				Status string `json:"status"`
			} `json:"turn"`
		}
		if json.Unmarshal(msg.Params, &p) != nil || p.Turn.Status != "completed" {
			return ErrClient
		}
		d.completed = true
	}
	return nil
}

func (d *dialogue) call(ctx context.Context, method string, params any, result any) error {
	d.next++
	if err := json.NewEncoder(d.in).Encode(map[string]any{"id": d.next, "method": method, "params": params}); err != nil {
		return ErrClient
	}
	for {
		msg, err := d.receive(ctx)
		if err != nil {
			return err
		}
		if len(msg.ID) > 0 && string(msg.ID) == strconv.Itoa(d.next) {
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				return ErrClient
			}
			if result != nil && json.Unmarshal(msg.Result, result) != nil {
				return ErrClient
			}
			return nil
		}
		if err = d.notification(msg); err != nil {
			return err
		}
	}
}

func Run(ctx context.Context, c Config) (Result, error) {
	r := Result{Protocol: Protocol, Version: Version, Stage: "setup"}
	if c.validate() != nil || CheckVersion(ctx, c.Binary) != nil {
		return r, ErrClient
	}
	r.Profile = c.Profile
	workspace := filepath.Join(os.Getenv("HOME"), "workspace")
	if os.Mkdir(workspace, 0700) != nil {
		return r, ErrClient
	}
	config := fmt.Sprintf(`model = %q
model_provider = "openai"
openai_base_url = %q
chatgpt_base_url = %q
web_search = "disabled"
approval_policy = "never"
sandbox_mode = "read-only"
cli_auth_credentials_store = "file"
[analytics]
enabled = false
[features]
plugins = false
apps = false
shell_tool = false
enable_request_compression = false
[features.token_budget]
enabled = %t
use_history_notes_extension = %t
`, c.Model, c.Origin+"/backend-api/codex", c.Origin+"/backend-api/", c.Profile != "standard", c.Profile != "standard")
	root := os.Getenv("CODEX_HOME")
	auth, _ := json.Marshal(map[string]string{"personal_access_token": c.Token})
	if os.WriteFile(filepath.Join(root, "config.toml"), []byte(config), 0600) != nil || os.WriteFile(filepath.Join(root, "auth.json"), auth, 0600) != nil {
		return r, ErrClient
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Binary, "app-server")
	cmd.Dir = workspace
	// A blocked proxy prevents unintended provider, telemetry or discovery
	// traffic. Only the private loopback bridge is reachable without it.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "CODEX_HOME=" + root, "TMPDIR=" + os.Getenv("TMPDIR"), "CODEX_AUTHAPI_BASE_URL=" + c.Origin,
		"NO_PROXY=127.0.0.1,localhost,::1", "HTTP_PROXY=" + c.Origin, "HTTPS_PROXY=" + c.Origin, "ALL_PROXY=" + c.Origin, "RUST_LOG=off", "NO_COLOR=1", "CI=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=false", "GIT_SSH_COMMAND=false"}
	if c.CA != "" {
		ca := filepath.Join(os.Getenv("HOME"), c.CA)
		if _, err := os.ReadFile(ca); err != nil {
			return r, ErrClient
		}
		cmd.Env = append(cmd.Env, "CODEX_CA_CERTIFICATE="+ca, "SSL_CERT_FILE="+ca, "NODE_EXTRA_CA_CERTS="+ca)
	}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		return r, ErrClient
	}
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return r, ErrClient
	}
	defer func() { _ = in.Close(); cancel(); _ = cmd.Wait() }()
	lines := make(chan []byte, 8)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		total := 0
		for scanner.Scan() {
			total += len(scanner.Bytes())
			if total > 4<<20 {
				cancel()
				return
			}
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	d := dialogue{in: in, lines: lines}
	r.Stage = "initialize"
	if d.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "aihub-codex-probe", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}, nil) != nil {
		return r, ErrClient
	}
	if json.NewEncoder(in).Encode(map[string]string{"method": "initialized"}) != nil {
		return r, ErrClient
	}
	var account struct {
		Account struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	r.Stage = "account"
	if d.call(ctx, "account/read", map[string]any{}, &account) != nil {
		return r, ErrClient
	}
	r.PlatformAuth = account.Account.Type == "chatgpt"
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	r.Stage = "thread"
	if d.call(ctx, "thread/start", map[string]any{"model": c.Model, "cwd": workspace, "approvalPolicy": "never", "sandbox": "read-only", "baseInstructions": "Isolated synthetic protocol probe. Use only the model-declared synthetic tools."}, &thread) != nil || thread.Thread.ID == "" {
		return r, ErrClient
	}
	r.Stage = "turn_start"
	if d.call(ctx, "turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []any{map[string]string{"type": "text", "text": "Reply with exactly " + Marker + "."}}}, nil) != nil {
		return r, ErrClient
	}
	r.Stage = "turn"
	for !d.completed {
		msg, e := d.receive(ctx)
		if e != nil || d.notification(msg) != nil {
			return r, ErrClient
		}
	}
	r.Completed, r.MarkerMatched = d.completed, d.matched
	r.Stage = "completed"
	return r, nil
}
