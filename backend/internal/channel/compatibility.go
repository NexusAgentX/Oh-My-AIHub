package channel

import (
	"slices"
	"strings"
)

// Compatibility is the administrator/owner declaration of the upstream contract.
// Each protocol is independent; absent cells are unsupported. Flat cells are
// kept in immutable routing snapshots and the existing bounded text[] storage.
type Compatibility map[string][]string

var Protocols = []string{"responses", "codex"}
var ChannelFeatures = []string{"inference", "nonstream", "stream", "background", "response_read", "response_delete", "response_cancel", "input_items", "conversations", "input_tokens", "compact", "zstd"}
var CodexChannelFeatures = []string{"search", "images_generate", "images_edit"}
var ModelFeatures = []string{"function_tools", "custom_tools", "tool_namespaces", "tool_search", "parallel_tools", "structured_output", "reasoning", "encrypted_reasoning", "reasoning_all_turns", "vision", "web_search", "image_generation", "responses_lite", "compact_v1", "compact_v2", "compaction_input", "file_search", "code_interpreter", "mcp", "computer", "shell", "apply_patch", "fast", "ultrafast"}

func cells(features []string, codexOnly []string) []string {
	var result []string
	for _, protocol := range Protocols {
		for _, feature := range features {
			result = append(result, protocol+"/"+feature)
		}
		if protocol == "codex" {
			for _, feature := range codexOnly {
				result = append(result, protocol+"/"+feature)
			}
		}
	}
	return result
}

func (c Compatibility) Cells() []string {
	result := make([]string, 0)
	for protocol, features := range c {
		if !slices.Contains(Protocols, protocol) {
			result = append(result, protocol+"/unsupported_protocol")
			continue
		}
		for _, feature := range features {
			result = append(result, protocol+"/"+feature)
		}
	}
	slices.Sort(result)
	return result
}

func Matrix(values []string) Compatibility {
	result := Compatibility{"responses": {}, "codex": {}}
	for _, value := range values {
		protocol, feature, found := strings.Cut(value, "/")
		if found && slices.Contains(Protocols, protocol) {
			result[protocol] = append(result[protocol], feature)
		}
	}
	return result
}

// DeclaredFormats is derived exclusively from the channel matrix. Model rows
// never independently enable or disable an HTTP protocol.
func DeclaredFormats(values []string) []Format {
	result := make([]Format, 0, 2)
	if slices.Contains(values, "responses/inference") {
		result = append(result, FormatOpenAIResponses)
	}
	if slices.Contains(values, "codex/inference") {
		result = append(result, FormatCodex)
	}
	return result
}
