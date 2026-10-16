package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// ServeCodexConfig returns administrator-owned documents, never upstream or
// platform identity. Authentication remains the platform key's real identity.
func (e *Engine) ServeCodexConfig(w http.ResponseWriter, r *http.Request) {
	key, _, ok := e.authenticate(w, r)
	if !ok {
		return
	}
	if !key.AccountActive || !keyUsable(key, e.Now()) {
		WriteError(w, DialectOpenAI, 401, "invalid_api_key", "API Key 或账号已停用或过期", "")
		return
	}
	value, _, err := e.Settings(r.Context())
	if err != nil {
		WriteError(w, DialectOpenAI, 503, "settings_unavailable", "配置暂不可用", "")
		return
	}
	var raw string
	switch r.URL.Path {
	case "/v1/user-auth-credential/whoami":
		raw = value.Codex.Whoami
	case "/backend-api/wham/accounts/check":
		raw = value.Codex.Accounts
	case "/backend-api/codex/models":
		raw = value.Codex.Models
	}
	if raw == "" {
		WriteError(w, DialectOpenAI, 503, "codex_not_configured", "尚未配置 Codex 兼容响应", "")
		return
	}
	e.touchKey(r.Context(), key.KeyID)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(raw))
}

// identifyCapabilities only decodes tool/input arrays when they can contain
// extensions. It never retains prompt content in diagnostics.
func identifyCapabilities(path string, format channel.Format, body []byte, scan objectScan, tier string) (string, []string) {
	op := "inference"
	var needed []string
	add := func(v string) {
		if !slices.Contains(needed, v) {
			needed = append(needed, v)
		}
	}
	if strings.HasPrefix(path, "/backend-api/codex/") {
		add("codex_backend")
	}
	switch operationOf(path) {
	case operationCompact:
		op = "compact_v1"
		add("compact_v1")
	case operationCount:
		op = "input_tokens"
	case operationConversation:
		op = "conversation"
	case operationSearch:
		op = "codex_search"
		add("codex_search")
	}
	if format == channel.FormatOpenAIImages {
		op = "image_generation"
	}
	if format != channel.FormatOpenAIResponses {
		return op, needed
	}
	if tier == "priority" || tier == "ultrafast" {
		add(tier)
	}
	var visit func(json.RawMessage, int)
	visit = func(raw json.RawMessage, depth int) {
		if depth > 4 {
			return
		}
		eachElement(raw, func(item []byte) {
			var kind string
			var tools []byte
			_, err := walkObject(item, 0, func(key []byte, start, end int) {
				switch string(key) {
				case "type":
					value := item[start:end]
					if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' && bytes.IndexByte(value, '\\') < 0 {
						kind = string(value[1 : len(value)-1])
					} else {
						_ = json.Unmarshal(value, &kind)
					}
				case "tools":
					tools = item[start:end]
				}
			})
			if err != nil {
				return
			}
			switch kind {
			case "web_search", "web_search_preview", "web_search_preview_2025_03_11":
				add("web_search")
			case "image_generation":
				add("image_generation")
			case "compaction_trigger":
				add("compact_v2")
				op = "compact_v2"
			case "additional_tools":
				add("responses_lite")
				visit(tools, depth+1)
			}
		})
	}
	for _, field := range []string{"tools", "input"} {
		if member, ok := scan.last(field); ok {
			raw := body[member.start:member.end]
			if field == "tools" || bytes.Contains(raw, []byte("compaction_trigger")) || bytes.Contains(raw, []byte("additional_tools")) || bytes.Contains(raw, []byte(`\u`)) {
				visit(raw, 0)
			}
		}
	}
	return op, needed
}
func supportsCapabilities(c Candidate, needed []string) bool {
	for _, capability := range needed {
		if !slices.Contains(c.Advanced.Capabilities, capability) && !slices.Contains(c.Capabilities, capability) {
			return false
		}
	}
	return true
}
