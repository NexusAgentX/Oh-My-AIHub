package gatewaycontract

import (
	"fmt"
	"strings"
	"testing"
)

func TestManifestTraceabilityAndScope(t *testing.T) {
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	mapped := map[string]bool{}
	for _, suite := range m.Suites {
		for _, r := range suite.Requirements {
			counts[suite.Protocol] += len(r.SourceIDs) + len(r.Conditional)
			for _, id := range append(append([]string{}, r.SourceIDs...), r.Conditional...) {
				mapped[suite.Protocol+"/"+id] = true
			}
		}
	}
	for _, e := range m.Exclusions {
		counts[e.Protocol] += len(e.SourceIDs)
		for _, id := range e.SourceIDs {
			mapped[e.Protocol+"/"+id] = true
		}
	}
	// Original ID ranges are an external traceability baseline, not an execution
	// denominator. Equal counts alone would allow an omission plus an invented ID.
	for protocol, ranges := range map[string]map[string]int{
		"openai-chat-completions": {"B": 8, "S": 10, "T": 8, "J": 9, "M": 6, "R": 10},
		"openai-responses":        {"R": 8, "O": 8, "S": 13, "T": 10, "C": 8, "F": 8, "M": 9, "A": 8, "E": 8},
		"native-messages":         {"A-B": 9, "A-O": 10, "A-S": 13, "A-T": 10, "A-F": 8, "A-M": 9, "A-E": 9},
		"native-generate-content": {"G-B": 9, "G-O": 10, "G-S": 11, "G-T": 12, "G-F": 10, "G-M": 10, "G-C": 9, "G-E": 9},
		"codex-special":           {"RL": 26},
	} {
		for prefix, last := range ranges {
			for i := 1; i <= last; i++ {
				id := fmt.Sprintf("%s/%s%02d", protocol, prefix, i)
				if !mapped[id] {
					t.Errorf("missing original ID %s", id)
				}
			}
		}
	}
	for protocol, want := range map[string]int{
		"openai-chat-completions": 51, "openai-responses": 80,
		"native-messages": 68, "native-generate-content": 80, "codex-special": 26,
	} {
		if got := counts[protocol]; got != want {
			t.Errorf("%s: trace IDs %d want %d", protocol, got, want)
		}
	}
	for _, e := range m.Endpoints {
		for _, resource := range []string{"/files", "/uploads", "/vector_stores", "/containers"} {
			if strings.Contains(e.Path, resource) {
				t.Errorf("scope restored by endpoint %s", e.ID)
			}
		}
		if e.ID == "identity.whoami" || e.ID == "identity.accounts" {
			if e.Model != "none_local" || e.Billing != "none" {
				t.Errorf("identity must be local: %+v", e)
			}
		}
		if strings.HasPrefix(e.ID, "codex.notes.") || strings.HasPrefix(e.ID, "codex.history.") {
			if e.Scope != "context_acl_only" {
				t.Errorf("context acquired stickiness: %s", e.ID)
			}
		}
	}
}

func TestManifestRejectsBrokenHandoffs(t *testing.T) {
	for _, name := range []string{"missing_endpoint", "duplicate_id", "missing_source", "wrong_feature", "self_aggregate", "aggregate_cycle"} {
		t.Run(name, func(t *testing.T) {
			m, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "missing_endpoint":
				m.Suites[0].Requirements[0].Endpoints = []string{"not_registered"}
			case "duplicate_id":
				m.Endpoints = append(m.Endpoints, m.Endpoints[0])
			case "missing_source":
				m.Endpoints[0].Source = "not_registered"
			case "wrong_feature":
				m.Endpoints[0].Feature = "F99"
			case "self_aggregate":
				r := &m.Suites[0].Requirements[0]
				r.AggregateOf = []string{r.ID}
			case "aggregate_cycle":
				a, b := &m.Suites[0].Requirements[0], &m.Suites[0].Requirements[1]
				a.AggregateOf = []string{b.ID}
				b.AggregateOf = []string{a.ID}
			}
			if m.Validate() == nil {
				t.Fatal("broken manifest accepted")
			}
		})
	}
}
