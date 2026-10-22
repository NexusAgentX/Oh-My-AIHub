package gatewaycontract

import (
	"strings"
	"testing"
)

// TestManifestTraceabilityAndScope pins the endpoint table: the eight
// inference/image entrances and the seven model catalog ones, each with its
// source, feature and policy. The count alone would allow a swap.
func TestManifestTraceabilityAndScope(t *testing.T) {
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"chat.create":                  "F7",
		"responses.create":             "F7",
		"images.generations":           "F7",
		"images.edits":                 "F7",
		"images.variations":            "F7",
		"messages.create":              "F7",
		"gemini.generateContent":       "F7",
		"gemini.streamGenerateContent": "F7",
		"models.openai.list":           "F11",
		"models.openai.get":            "F11",
		"models.anthropic.list":        "F11",
		"models.anthropic.get":         "F11",
		"models.gemini.list":           "F11",
		"models.gemini.get":            "F11",
		"models.gemini.v1.list":        "F11",
	}
	got := map[string]string{}
	for _, e := range m.Endpoints {
		got[e.ID] = e.Feature
	}
	if len(got) != len(want) {
		t.Errorf("endpoints = %d, want %d", len(got), len(want))
	}
	for id, feature := range want {
		if got[id] != feature {
			t.Errorf("endpoint %s = %q, want %q", id, got[id], feature)
		}
	}
	// The resource entrances stay out: they were retired with the server-side
	// state the gateway no longer keeps.
	for _, e := range m.Endpoints {
		for _, resource := range []string{"/files", "/uploads", "/vector_stores", "/containers"} {
			if strings.Contains(e.Path, resource) {
				t.Errorf("scope restored by endpoint %s", e.ID)
			}
		}
	}
}

func TestManifestRejectsBrokenHandoffs(t *testing.T) {
	for _, name := range []string{"duplicate_id", "missing_source", "unused_source", "wrong_feature", "pending_identity"} {
		t.Run(name, func(t *testing.T) {
			m, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "duplicate_id":
				m.Endpoints = append(m.Endpoints, m.Endpoints[0])
			case "missing_source":
				m.Endpoints[0].Source = "not_registered"
			case "unused_source":
				m.Sources = append(m.Sources, Source{ID: "unsolicited", URL: "https://example.com/docs", Version: "v0"})
			case "wrong_feature":
				m.Endpoints[0].Feature = "F99"
			case "pending_identity":
				m.Endpoints[0].Auth = "identity_decision_pending"
			}
			if m.Validate() == nil {
				t.Fatal("broken manifest accepted")
			}
		})
	}
}
