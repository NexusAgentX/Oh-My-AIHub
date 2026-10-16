package postgres_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

func TestModelListsUseChannelProtocolsAndKeyPermissions(t *testing.T) {
	e := newEnv(t)
	consumer, sharer := e.member("consumer", "100"), e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "responses", upstream: relay, model: "gpt-test", formats: []string{"openai_responses"}})
	e.createChannel(sharer, channelSpec{name: "codex", upstream: relay, model: "claude-test", formats: []string{"codex"}})
	e.apiJSON(sharer, 201, http.MethodPost, "/api/channels", map[string]any{"name": "custom", "base_url": relay.server.URL, "api_key": "synthetic-upstream", "advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream"}}}, "models": []any{map[string]any{"model_id": "foo-1", "custom_prices": map[string]string{"input": "1", "output": "1", "cache_read": "0", "cache_write": "0"}}}})
	keyID, secret := e.defaultKey(consumer)
	e.apiJSON(consumer, 200, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"model_aliases": map[string]string{"my-gpt": "gpt-test", "my-codex": "claude-test"}})
	read := func(headers map[string]string) []string {
		result := e.call(secret, http.MethodGet, "/v1/models", nil, headers)
		if result.Code != 200 {
			t.Fatal(result.Code, result.Body.String())
		}
		var body struct {
			Object string `json:"object"`
			Data   []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(result.Body.Bytes(), &body) != nil || body.Object != "list" {
			t.Fatal(result.Body.String())
		}
		var ids []string
		for _, item := range body.Data {
			ids = append(ids, item.ID)
		}
		return ids
	}
	want := []string{"claude-test", "foo-1", "gpt-test", "my-codex", "my-gpt"}
	if got := read(nil); !slices.Equal(got, want) {
		t.Fatal(got)
	}
	if got := read(map[string]string{"anthropic-version": "2023-06-01"}); !slices.Equal(got, want) {
		t.Fatal("legacy header changed representation", got)
	}
	e.apiJSON(consumer, 200, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"allowed_models": []string{"gpt-test"}})
	if got := read(nil); !slices.Equal(got, []string{"gpt-test", "my-gpt"}) {
		t.Fatal(got)
	}
	if result := e.call(secret, http.MethodGet, "/v1beta/models", nil, nil); result.Code != 404 {
		t.Fatal("retired endpoint available")
	}
}
