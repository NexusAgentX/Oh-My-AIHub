// Package gatewaycontract defines versioned gateway discovery contracts.
// Loading a manifest alone neither registers a route nor supplies upstream
// capability evidence; live features explicitly register their endpoint subset.
package gatewaycontract

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	apicontract "github.com/NexusAgentX/Oh-My-AIHub/backend/api"
	"go.yaml.in/yaml/v3"
)

// Manifest is the endpoint table of the gateway: which native entrances it
// proxies and where each one is documented.
type Manifest struct {
	Version   string     `yaml:"version"`
	Stage     string     `yaml:"stage"`
	Sources   []Source   `yaml:"sources"`
	Endpoints []Endpoint `yaml:"endpoints"`
}

type Source struct {
	ID      string `yaml:"id"`
	URL     string `yaml:"url"`
	Version string `yaml:"version"`
}

type Endpoint struct {
	ID       string `yaml:"id"`
	Protocol string `yaml:"protocol"`
	Method   string `yaml:"method"`
	Path     string `yaml:"path"`
	Feature  string `yaml:"feature"`
	Auth     string `yaml:"auth"`
	Scope    string `yaml:"scope"`
	Model    string `yaml:"model"`
	Billing  string `yaml:"billing"`
	Replay   string `yaml:"replay"`
	Source   string `yaml:"source"`
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
	if m.Version == "" || m.Stage != "discovery" || len(m.Endpoints) == 0 {
		return fmt.Errorf("invalid discovery version/stage/endpoints")
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
		endpoints[e.ID] = true
	}
	// A source that no endpoint cites would document an entrance the gateway
	// does not have.
	for _, s := range m.Sources {
		used := false
		for _, e := range m.Endpoints {
			used = used || e.Source == s.ID
		}
		if !used {
			return fmt.Errorf("source %s cites no endpoint", s.ID)
		}
	}
	return nil
}
