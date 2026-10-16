package postgres_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

func TestModelListsOnlyShowModelsServedInTheEndpointFormat(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	// One catalog model per format family; gemini-test has two channels so
	// the union of their formats counts.
	e.createChannel(sharer, channelSpec{name: "chat", upstream: relay, model: "gpt-test", formats: []string{"openai_chat"}})
	e.createChannel(sharer, channelSpec{name: "claude", upstream: relay, model: "claude-test", formats: []string{"anthropic"}})
	e.createChannel(sharer, channelSpec{name: "gemini", upstream: relay, model: "gemini-test", formats: []string{"gemini"}})
	e.createChannel(sharer, channelSpec{name: "gemini-responses", upstream: relay, model: "gemini-test", formats: []string{"openai_responses"}})
	custom := e.apiJSON(sharer, http.StatusCreated, http.MethodPost, "/api/channels", map[string]any{
		"name": "custom", "base_url": relay.server.URL, "api_key": "upstream-key-custom",
		"models": []any{map[string]any{
			"model_id": "foo-1", "formats": []string{"anthropic"},
			"custom_prices": map[string]string{"input": "1", "output": "1", "cache_write": "0", "cache_read": "0"},
		}},
	})
	if custom["channel"] == nil {
		t.Fatalf("custom channel = %v", custom)
	}
	keyID, secret := e.defaultKey(consumer)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{
		"model_aliases": map[string]string{"my-claude": "claude-test", "my-gpt": "gpt-test"},
	})

	anthropicHeaders := map[string]string{"x-api-key": secret, "anthropic-version": "2023-06-01"}
	cases := []struct {
		name string
		path string
		auth string
		head map[string]string
		want []string
	}{
		{"openai", "/v1/models", secret, nil, []string{"gemini-test", "gpt-test", "my-gpt"}},
		{"anthropic", "/v1/models", "", anthropicHeaders, []string{"claude-test", "foo-1", "my-claude"}},
		{"gemini", "/v1beta/models?key=" + secret, "", nil, []string{"gemini-test"}},
	}
	for _, c := range cases {
		listed := e.call(c.auth, http.MethodGet, c.path, nil, c.head)
		if listed.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", c.name, listed.Code, listed.Body.String())
		}
		var got []string
		if c.name == "gemini" {
			var page struct {
				Models []struct {
					BaseModelID string `json:"baseModelId"`
				} `json:"models"`
			}
			if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			for _, model := range page.Models {
				got = append(got, model.BaseModelID)
			}
		} else {
			got = modelIDsOf(t, listed.Body.Bytes())
		}
		if !slices.Equal(got, c.want) {
			t.Fatalf("%s list = %v, want %v", c.name, got, c.want)
		}
	}
}
