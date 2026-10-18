// Package gatewaycontract defines discovery-stage gateway contracts. It is not
// registered in the server: loading a manifest never opens a proxy endpoint or
// supplies upstream capability evidence.
package gatewaycontract

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	apicontract "github.com/NexusAgentX/Oh-My-AIHub/backend/api"
	"go.yaml.in/yaml/v3"
)

type Manifest struct {
	Version     string            `yaml:"version"`
	Stage       string            `yaml:"stage"`
	Sources     []Source          `yaml:"sources"`
	Endpoints   []Endpoint        `yaml:"endpoints"`
	Suites      []Suite           `yaml:"suites"`
	Exclusions  []Exclusion       `yaml:"exclusions"`
	Adjustments map[string]string `yaml:"adjustments"`
}

type Source struct {
	ID      string `yaml:"id"`
	URL     string `yaml:"url"`
	Version string `yaml:"version"`
}

type Endpoint struct {
	ID       string   `yaml:"id"`
	Protocol string   `yaml:"protocol"`
	Method   string   `yaml:"method"`
	Path     string   `yaml:"path"`
	Feature  string   `yaml:"feature"`
	Auth     string   `yaml:"auth"`
	Scope    string   `yaml:"scope"`
	Model    string   `yaml:"model"`
	Billing  string   `yaml:"billing"`
	Replay   string   `yaml:"replay"`
	Source   string   `yaml:"source"`
	Headers  []string `yaml:"headers,omitempty"`
	Fields   []string `yaml:"fields,omitempty"`
	Required []string `yaml:"required_fields,omitempty"`
	Returns  []string `yaml:"return_fields,omitempty"`
}

type Suite struct {
	ID           string        `yaml:"id"`
	Protocol     string        `yaml:"protocol"`
	Target       string        `yaml:"target"`
	Feature      string        `yaml:"feature"`
	Requirements []Requirement `yaml:"requirements"`
}

type Requirement struct {
	ID          string   `yaml:"id"`
	Endpoints   []string `yaml:"endpoints"`
	SourceIDs   []string `yaml:"source_ids"`
	Conditional []string `yaml:"conditional_source_ids"`
	Assertions  []string `yaml:"assertions"`
	AggregateOf []string `yaml:"aggregate_of,omitempty"`
}

type Exclusion struct {
	Protocol  string   `yaml:"protocol"`
	SourceIDs []string `yaml:"source_ids"`
	Reason    string   `yaml:"reason"`
}

// Load reads discovery metadata from the same document as the live API. The
// extension is deliberately outside paths; it must not advertise unimplemented
// routes as live or weaken the existing route/OpenAPI equality test.
func Load() (Manifest, error) {
	var doc struct {
		Contract yaml.Node `yaml:"x-aihub-gateway-contract"`
	}
	if err := yaml.Unmarshal([]byte(apicontract.Document()), &doc); err != nil {
		return Manifest{}, err
	}
	if doc.Contract.Kind != yaml.MappingNode {
		return Manifest{}, fmt.Errorf("gateway contract missing")
	}
	data, err := yaml.Marshal(&doc.Contract)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, manifest.Validate()
}

func (m Manifest) Validate() error {
	if m.Version == "" || m.Stage != "discovery" || len(m.Suites) == 0 {
		return fmt.Errorf("invalid discovery version/stage/suites")
	}
	sources := map[string]bool{}
	for _, s := range m.Sources {
		if s.ID == "" || sources[s.ID] || !strings.HasPrefix(s.URL, "https://") || s.Version == "" {
			return fmt.Errorf("invalid source %q", s.ID)
		}
		sources[s.ID] = true
	}
	endpoints := map[string]bool{}
	for _, e := range m.Endpoints {
		if e.ID == "" || endpoints[e.ID] || !sources[e.Source] || e.Protocol == "" {
			return fmt.Errorf("invalid endpoint %q", e.ID)
		}
		if e.Method != http.MethodGet && e.Method != http.MethodPost && e.Method != http.MethodPatch && e.Method != http.MethodDelete {
			return fmt.Errorf("invalid method for %s", e.ID)
		}
		if !strings.HasPrefix(e.Path, "/v1/") && !strings.HasPrefix(e.Path, "/v1beta/") && !strings.HasPrefix(e.Path, "/backend-api/") {
			return fmt.Errorf("invalid path for %s", e.ID)
		}
		if e.Feature != "F7" && e.Feature != "F8" && e.Feature != "F11" {
			return fmt.Errorf("invalid feature for %s", e.ID)
		}
		if e.Auth != "platform_key" && e.Auth != "owner_or_admin" {
			return fmt.Errorf("invalid auth for %s", e.ID)
		}
		if e.Scope != "stateless" && e.Scope != "remote_resource" && e.Scope != "context_acl_only" {
			return fmt.Errorf("invalid scope for %s", e.ID)
		}
		if e.Model == "" || e.Billing == "" || (e.Replay != "read" && e.Replay != "write" && e.Replay != "inference") {
			return fmt.Errorf("incomplete policy for %s", e.ID)
		}
		fields := map[string]bool{}
		for _, field := range e.Fields {
			if field == "" || fields[field] {
				return fmt.Errorf("invalid field for %s", e.ID)
			}
			fields[field] = true
		}
		for _, field := range e.Required {
			if !fields[field] {
				return fmt.Errorf("missing required field %s for %s", field, e.ID)
			}
		}
		endpoints[e.ID] = true
	}
	seenSuites, seenRequirements, seenSources := map[string]bool{}, map[string]bool{}, map[string]bool{}
	claim := func(protocol, id string) error {
		key := protocol + "/" + id
		if id == "" || seenSources[key] {
			return fmt.Errorf("duplicate/empty source mapping %s", key)
		}
		seenSources[key] = true
		return nil
	}
	for _, s := range m.Suites {
		if s.ID == "" || seenSuites[s.ID] || s.Protocol == "" || (s.Target != "group" && s.Target != "group_model" && s.Target != "gateway") || (s.Feature != "F4" && s.Feature != "F8") {
			return fmt.Errorf("invalid suite %q", s.ID)
		}
		seenSuites[s.ID] = true
		for _, r := range s.Requirements {
			if r.ID == "" || seenRequirements[r.ID] || len(r.Endpoints) == 0 || len(r.Assertions) == 0 {
				return fmt.Errorf("invalid requirement %q", r.ID)
			}
			seenRequirements[r.ID] = true
			for _, endpoint := range r.Endpoints {
				if !endpoints[endpoint] {
					return fmt.Errorf("missing endpoint %s", endpoint)
				}
			}
			for _, id := range append(append([]string{}, r.SourceIDs...), r.Conditional...) {
				if err := claim(s.Protocol, id); err != nil {
					return err
				}
			}
		}
	}
	for _, excluded := range m.Exclusions {
		if excluded.Protocol == "" || excluded.Reason == "" {
			return fmt.Errorf("invalid exclusion")
		}
		for _, id := range excluded.SourceIDs {
			if err := claim(excluded.Protocol, id); err != nil {
				return err
			}
		}
	}
	aggregates := map[string][]string{}
	for _, suite := range m.Suites {
		for _, r := range suite.Requirements {
			aggregates[r.ID] = r.AggregateOf
			for _, dependency := range r.AggregateOf {
				if dependency == r.ID || !seenRequirements[dependency] {
					return fmt.Errorf("invalid aggregate dependency %s", dependency)
				}
			}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("aggregate cycle at %s", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range aggregates[id] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for id := range aggregates {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
