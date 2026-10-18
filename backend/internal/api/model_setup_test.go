package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
)

type fixtureHandler struct {
	http.Handler
	store *fakeStore
}

// modelSetup retains the isolated old inference fixtures while their handlers
// are converted. It is test-only storage setup, never an application endpoint.
func modelSetup(t *testing.T, client *client, body any) map[string]any {
	t.Helper()
	wrapped, ok := client.handler.(*fixtureHandler)
	if !ok {
		t.Fatal("model setup requires an isolated fixture handler")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var request modelRequest
	if err = json.Unmarshal(raw, &request); err != nil || request.ID == nil {
		t.Fatal("invalid model fixture", err)
	}
	patch, err := request.patch()
	if err != nil {
		t.Fatal(err)
	}
	actorID := client.expect(t, http.StatusOK, http.MethodGet, "/api/me", nil)["account"].(map[string]any)["id"].(string)
	model, err := catalog.NewService(wrapped.store).Create(context.Background(), actorID, patch.Apply(catalog.Model{ID: *request.ID, Enabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(adminModelEnvelopeJSON{Model: newAdminModelJSON(model)})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
