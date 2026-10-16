package gatewaybench

import (
	"encoding/json"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"testing"
)

func TestPrimingDoesNotRequestStoredResponses(t *testing.T) {
	for _, stateless := range []bool{false, true} {
		req := (RequestSpec{Format: channel.FormatOpenAIResponses, StoreFalse: stateless}).Build("https://example.test")
		var body map[string]any
		if err := json.Unmarshal(req.Body, &body); err != nil {
			t.Fatal(err)
		}
		stored, ok := body["store"]
		if stateless && (!ok || stored != false) || !stateless && ok {
			t.Fatalf("store setting %v: %s", stateless, req.Body)
		}
	}
}
