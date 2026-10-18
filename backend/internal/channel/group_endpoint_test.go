package channel

import (
	"strings"
	"testing"
)

func TestGroupProbeEndpointPreservesOneEncodedModelIdentity(t *testing.T) {
	u, err := BuildEndpoint("https://synthetic.example/api", FormatGemini, "vendor/model:2026", true)
	if err != nil || u.Path != "/api/v1beta/models/vendor/model:2026:streamGenerateContent" || !strings.Contains(u.String(), "vendor%2Fmodel:2026:streamGenerateContent") {
		t.Fatal("model identity was split or double encoded", u, err)
	}
}
