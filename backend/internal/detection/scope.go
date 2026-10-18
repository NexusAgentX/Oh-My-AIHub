package detection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

// Scope fingerprints only this scenario's declared protocol contract. A CLI
// artifact or unrelated suite cannot erase a still-current native assertion;
// changing its endpoint, fields, source version or assertion does invalidate it.
// The full manifest/registry version still governs creation completeness.
func (r *Registry) scopeVersion(suite gatewaycontract.Suite, item Planned) string {
	requirements := []gatewaycontract.Requirement{}
	endpoints := []gatewaycontract.Endpoint{}
	sources := []gatewaycontract.Source{}
	endpointIDs, sourceIDs := map[string]bool{}, map[string]bool{}
	for _, req := range suite.Requirements {
		if !slices.Contains(item.Requirements, req.ID) {
			continue
		}
		labels := req.Assertions
		req.Assertions = nil
		for i, label := range labels {
			if slices.Contains(item.Assertions, AssertionID(req.ID, i)) {
				req.Assertions = append(req.Assertions, label)
			}
		}
		req.ConditionalAssertions = slices.Clone(item.Conditional)
		req.AssertionLayers = nil
		req.EvidenceLayers = []string{item.Layer}
		requirements = append(requirements, req)
		for _, id := range req.Endpoints {
			endpointIDs[id] = true
		}
	}
	for _, ep := range r.manifest.Endpoints {
		if endpointIDs[ep.ID] {
			endpoints = append(endpoints, ep)
			sourceIDs[ep.Source] = true
		}
	}
	for _, src := range r.manifest.Sources {
		if sourceIDs[src.ID] {
			sources = append(sources, src)
		}
	}
	encoded, _ := json.Marshal(struct {
		Version                               string
		SuiteID, Protocol, Target, Layer      string
		ModelTypes                            []string
		Requirements                          []gatewaycontract.Requirement
		Endpoints                             []gatewaycontract.Endpoint
		Sources                               []gatewaycontract.Source
		Assertions, Conditional, Dependencies []string
	}{"scenario-scope.v1", suite.ID, suite.Protocol, suite.Target, item.Layer, suite.ModelTypes, requirements, endpoints, sources, item.Assertions, item.Conditional, item.Dependencies})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
