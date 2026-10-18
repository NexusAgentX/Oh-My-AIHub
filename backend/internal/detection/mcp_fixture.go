package detection

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

const mcpFixturePath = "/api/detection/mcp"
const mcpTool = "aihub_test_marker"
const mcpServer = "aihub_synthetic"

// MCPFixture serves one fixed, read-only synthetic tool. No client can create
// a probe, select a URL, execute code, or obtain application credentials here.
// Tokens and results live only until the owning worker finishes the scenario.
type MCPFixture struct {
	origin string
	mu     sync.Mutex
	probes map[string]*mcpProbe
}
type mcpProbe struct {
	token    string
	marker   string
	requests atomic.Int32
	calls    atomic.Int32
}

func NewMCPFixture(origin string) (*MCPFixture, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, ErrInvalid
	}
	return &MCPFixture{origin: strings.TrimSuffix(origin, "/"), probes: map[string]*mcpProbe{}}, nil
}
func randomMCPValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (f *MCPFixture) open() (*mcpProbe, func(), error) {
	token, err := randomMCPValue()
	if err != nil {
		return nil, nil, err
	}
	marker, err := randomMCPValue()
	if err != nil {
		return nil, nil, err
	}
	p := &mcpProbe{token: token, marker: "AIHub-MCP-" + marker}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.probes) >= 16 {
		return nil, nil, ErrBusy
	}
	f.probes[token] = p
	return p, func() { f.mu.Lock(); delete(f.probes, token); f.mu.Unlock() }, nil
}
func (f *MCPFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "POST" || r.URL.Path != mcpFixturePath || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	p := f.probes[token]
	f.mu.Unlock()
	if p == nil {
		http.NotFound(w, r)
		return
	}
	if p.requests.Add(1) > 64 {
		w.WriteHeader(429)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 16<<10+1))
	if err != nil || len(raw) > 16<<10 {
		w.WriteHeader(413)
		return
	}
	o, err := decodeNative(raw)
	if err != nil || str(o["jsonrpc"]) != "2.0" {
		w.WriteHeader(400)
		return
	}
	method := str(o["method"])
	if method == "notifications/initialized" && o["id"] == nil {
		w.WriteHeader(202)
		return
	}
	if _, ok := o["id"].(string); !ok {
		if _, ok := o["id"].(json.Number); !ok {
			w.WriteHeader(400)
			return
		}
	}
	var result any
	switch method {
	case "initialize":
		// This fixed profile supports the two mutually compatible JSON
		// Streamable HTTP protocol revisions. It never opens an SSE session.
		version := str(object(o["params"])["protocolVersion"])
		if version != "2025-03-26" && version != "2025-06-18" {
			version = "2025-06-18"
		}
		result = nativeObject{"protocolVersion": version, "capabilities": nativeObject{"tools": nativeObject{}}, "serverInfo": nativeObject{"name": "aihub-synthetic", "version": "1"}}
	case "ping":
		result = nativeObject{}
	case "tools/list":
		result = nativeObject{"tools": []any{nativeObject{"name": mcpTool, "description": "Return a synthetic random marker. Call with an empty object. This tool reads no files or user data.", "inputSchema": nativeObject{"type": "object", "properties": nativeObject{}, "additionalProperties": false}, "annotations": nativeObject{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}}}}
	case "tools/call":
		params := object(o["params"])
		args := object(params["arguments"])
		if str(params["name"]) != mcpTool || args == nil || len(args) != 0 || p.calls.Add(1) > 4 {
			w.WriteHeader(400)
			return
		}
		result = nativeObject{"content": []any{nativeObject{"type": "text", "text": p.marker}}, "isError": false}
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nativeObject{"jsonrpc": "2.0", "id": o["id"], "error": nativeObject{"code": -32601, "message": "Method not found"}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(nativeObject{"jsonrpc": "2.0", "id": o["id"], "result": result})
}
