package channel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoutingLeaseJSONExcludesSensitiveRoutingMaterial(t *testing.T) {
	encoded, err := json.Marshal(RoutingLease{
		OfferID: "offer-id", NormalizedBaseURL: "https://private.example/prefix",
		UpstreamModelID: "private-upstream-model", Credential: "secret-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private.example", "private-upstream-model", "secret-key"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("routing lease JSON leaked %q: %s", forbidden, encoded)
		}
	}
}
