package detection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type Registry struct {
	mu          sync.Mutex
	manifest    gatewaycontract.Manifest
	definitions map[string]Definition
	frozen      bool
}

func NewRegistry(m gatewaycontract.Manifest) (*Registry, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &Registry{manifest: m, definitions: map[string]Definition{}}, nil
}
func AssertionID(requirement string, index int) string {
	return requirement + "/a" + strconv.Itoa(index+1)
}
func (r *Registry) Register(d Definition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen || d.ID == "" || d.CodeVersion == "" || d.Run == nil || len(d.Requirements) == 0 || r.definitions[d.ID].ID != "" || !slices.Contains([]string{"upstream_protocol", "gateway_proxy", "official_cli"}, d.Layer) || (d.EstimatedHTTP != nil && *d.EstimatedHTTP < 0) {
		return ErrInvalid
	}
	var suite *gatewaycontract.Suite
	for i := range r.manifest.Suites {
		if r.manifest.Suites[i].ID == d.SuiteID {
			suite = &r.manifest.Suites[i]
			break
		}
	}
	if suite == nil {
		return ErrInvalid
	}
	assertions := map[string]bool{}
	seen := map[string]bool{}
	for _, id := range d.Requirements {
		if seen[id] {
			return ErrInvalid
		}
		seen[id] = true
		found := false
		for _, req := range suite.Requirements {
			if req.ID == id && len(req.AggregateOf) == 0 {
				found = true
				for i := range req.Assertions {
					assertions[AssertionID(id, i)] = true
				}
			}
		}
		if !found {
			return ErrInvalid
		}
		for _, prior := range r.definitions {
			if slices.Contains(prior.Requirements, id) {
				return ErrInvalid
			}
		}
	}
	for _, id := range d.Conditional {
		if !assertions[id] {
			return ErrInvalid
		}
	}
	d.Requirements = slices.Clone(d.Requirements)
	d.Dependencies = slices.Clone(d.Dependencies)
	d.Conditional = slices.Clone(d.Conditional)
	r.definitions[d.ID] = d
	return nil
}
func applies(s gatewaycontract.Suite, t Target) bool {
	if s.Target != t.Binding.TargetKind {
		return false
	}
	// OpenAI image endpoints apply to image offerings. Responses/Gemini keep
	// their own image-tool scenarios for text offerings; their suite registration
	// cannot disappear merely because an upstream says a protocol is unsupported.
	if s.Protocol == "openai-images" {
		return t.ModelType == "image"
	}
	if s.Target == "group_model" && t.ModelType == "image" {
		return s.Protocol == "native-generate-content" || s.Protocol == "openai-responses"
	}
	return true
}
func (r *Registry) Plan(t Target) (Plan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.Binding.TargetID == "" || (t.Binding.TargetKind != "group" && t.Binding.TargetKind != "group_model" && t.Binding.TargetKind != "gateway") || (t.Binding.TargetKind == "group_model" && t.ModelType != "text" && t.ModelType != "image") {
		return Plan{}, ErrInvalid
	}
	r.frozen = true
	p := Plan{EngineVersion: engineVersion, ManifestVersion: r.manifest.Version, Items: []Planned{}}
	claimed := map[string]string{}
	for id, d := range r.definitions {
		for _, req := range d.Requirements {
			claimed[req] = id
		}
	}
	included := map[string]bool{}
	reqOwner := map[string]string{}
	for _, suite := range r.manifest.Suites {
		if !applies(suite, t) {
			continue
		}
		for _, req := range suite.Requirements {
			id := claimed[req.ID]
			if id == "" {
				id = req.ID
			}
			reqOwner[req.ID] = id
			if included[id] {
				continue
			}
			included[id] = true
			d, exists := r.definitions[id]
			item := Planned{ID: id, SuiteID: suite.ID, Requirements: []string{req.ID}, Assertions: []string{}, Conditional: []string{}, Dependencies: []string{}, Aggregate: len(req.AggregateOf) > 0, Implemented: exists || len(req.AggregateOf) > 0, Layer: "upstream_protocol"}
			if exists {
				item.Requirements = slices.Clone(d.Requirements)
				item.Conditional = slices.Clone(d.Conditional)
				item.Dependencies = slices.Clone(d.Dependencies)
				item.CodeVersion = d.CodeVersion
				item.Layer = d.Layer
				item.EstimatedHTTP = d.EstimatedHTTP
			}
			for _, required := range item.Requirements {
				for _, member := range suite.Requirements {
					if member.ID == required {
						for i := range member.Assertions {
							item.Assertions = append(item.Assertions, AssertionID(member.ID, i))
						}
					}
				}
			}
			if item.Aggregate {
				item.Dependencies = slices.Clone(req.AggregateOf)
				item.CodeVersion = "aggregate.v1"
			}
			p.Items = append(p.Items, item)
		}
	}
	for i := range p.Items {
		item := &p.Items[i]
		if item.Aggregate {
			for j, req := range item.Dependencies {
				if owner := reqOwner[req]; owner != "" {
					item.Dependencies[j] = owner
				}
			}
		}
	}
	// Validate the entire graph even when dependencies or adapters are missing;
	// an unavailable suite is pending, while an invalid dependency is an error.
	byID := map[string]Planned{}
	for _, item := range p.Items {
		byID[item.ID] = item
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("detection dependency cycle: %s", id)
		}
		item, ok := byID[id]
		if !ok {
			return fmt.Errorf("unknown detection dependency: %s", id)
		}
		visiting[id] = true
		for _, dep := range item.Dependencies {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, item := range p.Items {
		if err := visit(item.ID); err != nil {
			return Plan{}, err
		}
	}
	encoded, _ := json.Marshal(p)
	sum := sha256.Sum256(encoded)
	p.RegistryVersion = hex.EncodeToString(sum[:])
	return p, nil
}
