package gateway

import (
	"bytes"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func TestContextEndpointGatesUseDeclaredOperationAndIsolationProof(t *testing.T) {
	m, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, s := range m.Suites {
		for _, r := range s.Requirements {
			for i, label := range r.Assertions {
				if r.AssertionInLayer(r.ID+"/a"+strconv.Itoa(i+1), "upstream_protocol") {
					known[r.ID+"/"+label] = true
				}
			}
		}
	}
	count := 0
	for _, ep := range m.Endpoints {
		if ep.Feature != "F8" || ep.Scope != "context_acl_only" {
			continue
		}
		count++
		required := endpointRequirement(ep.ID)
		if len(required) != 2 {
			t.Fatal("missing operation/isolation proof", ep.ID)
		}
		for _, cap := range required {
			if cap.Layer != "upstream_protocol" || !known[cap.ID] {
				t.Fatal("context gate cannot match current report projection", ep.ID, cap)
			}
		}
	}
	if count != 10 {
		t.Fatal("fixed context operation set changed", count)
	}
}

func TestOptionalNullContextContainersRetainNativeBytes(t *testing.T) {
	n, err := gatewaycontract.NewContextNamespace(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(Dependencies{ContextNamespace: n})
	body := []byte(`{"model":"fixture","context":null,"client_metadata":null,"input":"unchanged"}`)
	_, scan, err := ScanRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/backend-api/codex/responses", nil)
	_, out, _, err := e.isolateCodexRequest(r, body, scan, channel.FormatOpenAIResponses, KeyAuth{OwnerID: "owner", KeyID: "key", AccountActive: true})
	if err != nil || !bytes.Equal(out, body) {
		t.Fatal("optional null metadata changed or rejected", err)
	}
}
