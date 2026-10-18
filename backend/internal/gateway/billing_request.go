package gateway

import (
	"context"
	"encoding/json"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type billingHints struct {
	Background              bool
	ContainerMemory         string
	ImageModel              string
	Submodels               []string
	GeminiToolInputBillable bool
}

func (scan objectScan) billingHints(body []byte, format channel.Format) billingHints {
	var hints billingHints
	if member, ok := scan.last("background"); ok {
		hints.Background = string(body[member.start:member.end]) == "true"
	}
	if member, ok := scan.last("tools"); ok {
		if format == channel.FormatGemini {
			hints.GeminiToolInputBillable = geminiBillableTools(body[member.start:member.end])
			return hints
		}
		var tools []struct {
			Type      string          `json:"type"`
			Model     string          `json:"model"`
			Container json.RawMessage `json:"container"`
		}
		// tools is bounded independently; an oversized configuration still forwards.
		if member.end-member.start > 1<<20 {
			return hints
		}
		if json.Unmarshal(body[member.start:member.end], &tools) != nil || len(tools) > 128 {
			return hints
		}
		for _, tool := range tools {
			switch tool.Type {
			case "code_interpreter":
				var container struct {
					Type   string `json:"type"`
					Memory string `json:"memory_limit"`
				}
				if json.Unmarshal(tool.Container, &container) == nil && container.Type == "auto" {
					switch container.Memory {
					case "":
						hints.ContainerMemory = "1g"
					case "1g", "1gb", "4g", "4gb", "16g", "16gb", "64g", "64gb":
						hints.ContainerMemory = container.Memory
					}
				}
			case "image_generation":
				if gatewaycontract.ValidModelID(tool.Model) {
					hints.ImageModel = tool.Model
				}
			case "advisor_20260301":
				if gatewaycontract.ValidModelID(tool.Model) {
					hints.Submodels = append(hints.Submodels, tool.Model)
				}
			}
		}
	}
	return hints
}

// Only explicitly configured, exclusively non-grounding server tools establish
// a billable source. Unknown, mixed and oversized configurations still forward.
func geminiBillableTools(raw []byte) bool {
	if len(raw) > 1<<20 {
		return false
	}
	var tools []map[string]json.RawMessage
	if json.Unmarshal(raw, &tools) != nil || len(tools) == 0 || len(tools) > maxBillingItems {
		return false
	}
	for _, tool := range tools {
		if len(tool) != 1 {
			return false
		}
		for name, config := range tool {
			switch name {
			case "code_execution", "codeExecution", "url_context", "urlContext", "file_search", "fileSearch":
				if len(config) == 0 || config[0] != '{' {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

func (e *Engine) captureSubmodels(ctx context.Context, hints billingHints) ([]catalog.Model, error) {
	if len(hints.Submodels) == 0 {
		return nil, nil
	}
	models, err := e.Catalog.List(ctx, true)
	if err != nil {
		return nil, err
	}
	result := []catalog.Model{}
	for _, model := range models {
		match := false
		for _, name := range hints.Submodels {
			if model.ID == name {
				match = true
			}
		}
		if match && len(result) < maxBillingItems {
			result = append(result, model)
		}
	}
	return result, nil
}
