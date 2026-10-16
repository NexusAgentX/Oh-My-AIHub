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

// Immutable one-cell requirements keep ordinary requests allocation-free. Any
// extension append exceeds capacity and gets a request-owned backing array.
var responsesInferenceRequirements = []string{"responses/inference", "responses/nonstream"}
var codexInferenceRequirements = []string{"codex/inference", "codex/nonstream"}
var responsesStreamRequirements = []string{"responses/inference", "responses/stream"}
var codexStreamRequirements = []string{"codex/inference", "codex/stream"}

// identifyCapabilities scans only structural request fields. Prompt strings never
// establish capabilities. All slices borrow the bounded incoming request body.
func identifyCapabilities(path string, format channel.Format, body []byte, scan objectScan, tier string) (string, []string) {
	protocol := "responses"
	if strings.HasPrefix(path, "/backend-api/codex/") {
		protocol = "codex"
	}
	op := "inference"
	var needed []string
	add := func(feature string) {
		value := protocol + "/" + feature
		if !slices.Contains(needed, value) {
			needed = append(needed, value)
		}
	}
	if format != channel.FormatOpenAIResponses && format != channel.FormatOpenAIImages {
		return op, nil
	}
	rawField := func(name string) []byte {
		if member, ok := scan.last(name); ok {
			return body[member.start:member.end]
		}
		return nil
	}
	switch operationOf(path) {
	case operationCompact:
		op = "compact_v1"
		add("compact")
		add("compact_v1")
	case operationCount:
		op = "input_tokens"
		add("input_tokens")
	case operationConversation:
		op = "conversation"
		add("conversations")
	case operationSearch:
		op = "codex_search"
		add("search")
	default:
		if format == channel.FormatOpenAIImages {
			op = "image_generation"
			if strings.HasSuffix(path, "/edits") {
				add("images_edit")
			} else {
				add("images_generate")
			}
		} else {
			needed = responsesInferenceRequirements
			if protocol == "codex" {
				needed = codexInferenceRequirements
			}
			if bytes.Equal(rawField("stream"), []byte("true")) {
				needed = responsesStreamRequirements
				if protocol == "codex" {
					needed = codexStreamRequirements
				}
			}

		}
	}
	if bytes.Equal(rawField("stream"), []byte("true")) {
		add("stream")
	}
	if bytes.Equal(rawField("background"), []byte("true")) {
		add("background")
		add("response_read")
	}
	if bytes.Equal(rawField("store"), []byte("true")) {
		add("response_read")
	}
	if len(rawField("conversation")) > 0 && !bytes.Equal(rawField("conversation"), []byte("null")) {
		add("conversations")
	}
	if len(rawField("previous_response_id")) > 0 && !bytes.Equal(rawField("previous_response_id"), []byte("null")) {
		add("response_read")
	}
	if tier == "priority" || tier == "fast" {
		add("fast")
	} else if tier == "ultrafast" {
		add("ultrafast")
	}
	if bytes.Equal(rawField("parallel_tool_calls"), []byte("true")) {
		add("parallel_tools")
	}
	stringField := func(raw []byte, name string) string {
		var value string
		_, _ = walkObject(raw, 0, func(key []byte, start, end int) {
			if string(key) == name {
				_ = json.Unmarshal(raw[start:end], &value)
			}
		})
		return value
	}
	if raw := rawField("reasoning"); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		add("reasoning")
		if stringField(raw, "context") == "all_turns" {
			add("reasoning_all_turns")
		}
	}
	eachElement(rawField("include"), func(item []byte) {
		var value string
		_ = json.Unmarshal(item, &value)
		if value == "reasoning.encrypted_content" {
			add("encrypted_reasoning")
		}
	})
	if raw := rawField("text"); len(raw) > 0 {
		_, _ = walkObject(raw, 0, func(key []byte, start, end int) {
			if string(key) == "format" {
				kind := stringField(raw[start:end], "type")
				if kind == "json_schema" || kind == "json_object" {
					add("structured_output")
				}
			}
		})
	}
	var visit func([]byte, int, bool)
	visit = func(raw []byte, depth int, toolsContext bool) {
		if depth > 8 {
			add("unsupported_structure")
			return
		}
		eachElement(raw, func(item []byte) {
			var kind string
			var tools, content []byte
			encrypted := false
			_, err := walkObject(item, 0, func(key []byte, start, end int) {
				switch string(key) {
				case "type":
					_ = json.Unmarshal(item[start:end], &kind)
				case "tools":
					tools = item[start:end]
				case "content":
					content = item[start:end]
				case "encrypted_content":
					encrypted = !bytes.Equal(item[start:end], []byte("null")) && !bytes.Equal(item[start:end], []byte(`""`))
				}
			})
			if err != nil {
				return
			}
			if toolsContext {
				switch kind {
				case "function":
					add("function_tools")
				case "custom":
					add("custom_tools")
				case "namespace":
					add("tool_namespaces")
					visit(tools, depth+1, true)
				case "tool_search":
					add("tool_search")
				case "web_search", "web_search_preview", "web_search_preview_2025_03_11":
					add("web_search")
				case "image_generation":
					add("image_generation")
				case "file_search", "code_interpreter", "mcp", "computer", "computer_use_preview", "shell", "local_shell", "apply_patch":
					feature := kind
					if kind == "computer_use_preview" {
						feature = "computer"
					}
					if kind == "local_shell" {
						feature = "shell"
					}
					add(feature)
				default:
					add("unsupported_tool")
				}
				return
			}
			switch kind {
			case "compaction_trigger":
				add("compact_v2")
				op = "compact_v2"
			case "additional_tools":
				add("responses_lite")
				visit(tools, depth+1, true)
			case "function_call", "function_call_output":
				add("function_tools")
			case "custom_tool_call", "custom_tool_call_output":
				add("custom_tools")
			case "tool_search_call", "tool_search_output":
				add("tool_search")
			case "reasoning":
				if encrypted {
					add("encrypted_reasoning")
				}
			case "compaction":
				if encrypted {
					add("compaction_input")
				}
			case "input_image":
				add("vision")
			}
			if len(content) > 0 {
				visit(content, depth+1, false)
			}
		})
	}
	visit(rawField("tools"), 0, true)
	visit(rawField("input"), 0, false)
	return op, needed
}

func supportsCapabilities(c Candidate, needed []string) bool {
	for _, capability := range needed {
		allowed := c.Capabilities
		if slices.Contains(channel.ChannelCapabilities, capability) {
			allowed = c.Advanced.Capabilities
		}
		if !slices.Contains(allowed, capability) {
			return false
		}
	}
	return true
}

// Resource operations preserve their original credential and never fail over,
// but still honor the current channel's declared HTTP compatibility. Candidate
// lookup is served by the existing immutable snapshot.
func (e *Engine) compatibleResource(w http.ResponseWriter, r *http.Request, row Resource, override ...string) bool {
	protocol := "responses"
	if strings.HasPrefix(r.URL.Path, "/backend-api/codex/") {
		protocol = "codex"
	}
	feature := "response_read"
	switch {
	case strings.Contains(r.URL.Path, "/conversations/"):
		feature = "conversations"
	case strings.HasSuffix(r.URL.Path, "/input_items"):
		feature = "input_items"
	case strings.HasSuffix(r.URL.Path, "/cancel"):
		feature = "response_cancel"
	case r.Method == http.MethodDelete:
		feature = "response_delete"
	}
	if len(override) > 0 {
		feature = override[0]
	}
	candidates, err := e.Store.Candidates(r.Context(), row.ModelID, channel.FormatOpenAIResponses)
	if err != nil {
		WriteError(w, DialectOpenAI, 503, "resource_unavailable", "资源暂不可用", row.CallID)
		return false
	}
	needed := []string{protocol + "/" + feature}
	if r.URL.Query().Get("stream") == "true" {
		needed = append(needed, protocol+"/stream")
	}
	for _, candidate := range candidates {
		if candidate.ChannelID == row.ChannelID && supportsCapabilities(candidate, needed) {
			return true
		}
	}
	WriteError(w, DialectOpenAI, 503, "capability_unavailable", "资源所属渠道不兼容此操作，请检查渠道兼容性配置", row.CallID)
	return false
}
