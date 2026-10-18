package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func codexProof(c Candidate, requirement, operation string) bool {
	return c.Evidence["upstream_protocol/codex-special."+requirement+"/"+operation]
}

func codexTierProof(c Candidate, wire string) bool {
	if wire == "priority" {
		wire = "fast"
	}
	switch wire {
	case "default", "fast", "flex", "ultrafast", "scale":
		return c.Evidence["upstream_protocol/codex-special.RL24/service_tier/openai:"+wire]
	}
	return false
}

// This is a scoped view of the selected authorized provider's native catalog.
// It retains no catalog or model instructions after the request. Only the
// pinned ModelInfo fields are exposed; account-level metadata is never copied.
func filterCodexCatalog(payload []byte, records []CandidateRecord, selected Candidate, key KeyAuth) ([]byte, error) {
	if len(payload) > 4<<20 {
		return nil, errors.New("catalog response exceeds limit")
	}
	var envelope struct {
		Models []json.RawMessage `json:"models"`
	}
	if json.Unmarshal(payload, &envelope) != nil || envelope.Models == nil || len(envelope.Models) > 10000 {
		return nil, errors.New("invalid native model catalog")
	}
	allowed := map[string][]CandidateRecord{}
	for _, r := range records {
		c := r.Candidate
		if c.ChannelID != selected.ChannelID || c.GroupID != selected.GroupID || c.CredentialScope != selected.CredentialScope || !c.ValidOffering(r.ModelID) || !slices.Contains(r.Formats, channel.FormatOpenAIResponses) || len(key.AllowedModels) > 0 && !slices.Contains(key.AllowedModels, r.ModelID) {
			continue
		}
		allowed[c.UpstreamModel] = append(allowed[c.UpstreamModel], r)
	}
	// ModelInfo, rust-v0.163.0. Entitlement and model-policy overrides are not
	// platform privileges; the client keeps its own configured policies.
	fields := strings.Fields("slug display_name description default_reasoning_level supported_reasoning_levels shell_type visibility supported_in_api priority additional_speed_tiers service_tiers default_service_tier model_messages base_instructions include_skills_usage_instructions include_plugin_usage_instructions include_apps_usage_instructions supports_reasoning_summary_parameter default_reasoning_summary support_verbosity default_verbosity apply_patch_tool_type web_search_tool_type truncation_policy supports_image_detail_original context_window max_context_window auto_compact_token_limit comp_hash effective_context_window_percent experimental_supported_tools input_modalities supports_search_tool supports_experimental_context use_responses_lite supports_reasoning_effort_updates node_repl_auto_review_required node_repl_disabled model_specialty tool_mode multi_agent_version multi_agent_reasoning_effort")
	models := map[string]map[string]json.RawMessage{}
	remaining := (4 << 20) - 128
	for _, raw := range envelope.Models {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			return nil, errors.New("invalid model metadata")
		}
		var slug string
		_ = json.Unmarshal(item["slug"], &slug)
		for _, r := range allowed[slug] {
			names := []string{r.ModelID}
			for alias, target := range key.Aliases {
				if target == r.ModelID {
					names = append(names, alias)
				}
			}
			for _, name := range names {
				if target, aliased := key.Aliases[name]; aliased && target != r.ModelID {
					continue
				}
				if _, exists := models[name]; exists {
					continue
				}
				view := map[string]json.RawMessage{}
				for _, field := range fields {
					if value, ok := item[field]; ok {
						view[field] = value
					}
				}
				set := func(field string, value any) { encoded, _ := json.Marshal(value); view[field] = encoded }
				set("slug", name)
				set("display_name", name)
				c := r.Candidate
				flag := func(field string, supported bool) {
					var declared bool
					_ = json.Unmarshal(item[field], &declared)
					set(field, declared && supported)
				}
				flag("use_responses_lite", codexProof(c, "RL01", "native_lite_request") && codexProof(c, "RL03", "lite_tools_roundtrip") && codexProof(c, "RL08", "sse_lifecycle"))
				flag("supports_search_tool", codexProof(c, "RL16", "native_search"))
				contextOK := true
				for _, op := range []string{"thread_hint"} {
					for _, required := range codexContextRequirements("codex.notes." + op) {
						contextOK = contextOK && c.Evidence[required.Layer+"/"+required.ID]
					}
				}
				for _, op := range []string{"write_file", "read_file", "list_files_by_prefix", "search_contents", "append_to_file"} {
					for _, required := range codexContextRequirements("codex.notes." + op) {
						contextOK = contextOK && c.Evidence[required.Layer+"/"+required.ID]
					}
				}
				for _, op := range []string{"list_windows", "list_items", "read_item", "search_contents"} {
					for _, required := range codexContextRequirements("codex.history." + op) {
						contextOK = contextOK && c.Evidence[required.Layer+"/"+required.ID]
					}
				}
				flag("supports_experimental_context", contextOK)
				var modalities []string
				_ = json.Unmarshal(item["input_modalities"], &modalities)
				filtered := []string{}
				for _, modality := range modalities {
					if modality == "text" || modality == "image" && c.Evidence["upstream_protocol/openai-responses.vision/inline_content_recognition"] {
						filtered = append(filtered, modality)
					}
				}
				set("input_modalities", filtered)
				if !c.Evidence["upstream_protocol/openai-responses.tools/call_result_association"] && !codexProof(c, "RL02", "tool_result_continuation") {
					set("experimental_supported_tools", []string{})
				}
				lower := int64(0)
				for id, n := range c.VerifiedCapacity {
					if (strings.HasPrefix(id, "openai-responses.") || strings.HasPrefix(id, "codex-special.")) && strings.HasSuffix(id, "/context") && n > 0 && (lower == 0 || n < lower) {
						lower = n
					}
				}
				for _, field := range []string{"context_window", "max_context_window", "auto_compact_token_limit"} {
					var declared int64
					_ = json.Unmarshal(item[field], &declared)
					if lower > 0 && declared > 0 {
						set(field, min(lower, declared))
					} else {
						delete(view, field)
					}
				}
				var tiers []json.RawMessage
				_ = json.Unmarshal(item["service_tiers"], &tiers)
				acceptedTiers := []json.RawMessage{}
				for _, tier := range tiers {
					var value struct {
						ID string `json:"id"`
					}
					if json.Unmarshal(tier, &value) == nil && codexTierProof(c, value.ID) {
						acceptedTiers = append(acceptedTiers, tier)
					}
				}
				set("service_tiers", acceptedTiers)
				var speeds []string
				_ = json.Unmarshal(item["additional_speed_tiers"], &speeds)
				acceptedSpeeds := []string{}
				for _, speed := range speeds {
					if codexTierProof(c, speed) {
						acceptedSpeeds = append(acceptedSpeeds, speed)
					}
				}
				set("additional_speed_tiers", acceptedSpeeds)
				var defaultTier string
				_ = json.Unmarshal(item["default_service_tier"], &defaultTier)
				if !codexTierProof(c, defaultTier) {
					delete(view, "default_service_tier")
				}
				size := 4
				for field, value := range view {
					size += len(field) + len(value) + 5
				}
				if size > remaining {
					return nil, errors.New("filtered catalog exceeds limit")
				}
				remaining -= size
				models[name] = view
			}
		}
	}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]map[string]json.RawMessage, 0, len(names))
	for _, name := range names {
		result = append(result, models[name])
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{"models": result}); err != nil {
		return nil, err
	}
	if output.Len() > 4<<20 {
		return nil, errors.New("filtered catalog exceeds limit")
	}
	return output.Bytes(), nil
}
