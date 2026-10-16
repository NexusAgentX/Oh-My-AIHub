package gateway

import (
	"context"
	"slices"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestOpenAIModelListOwnerFallsBackToPlatform(t *testing.T) {
	names := []listedModel{{id: "gpt-x", model: catalog.Model{ID: "gpt-x", Provider: "OpenAI"}}, {id: "local", model: catalog.Model{ID: "local"}}}
	data := openAIModelList(names)["data"].([]map[string]any)
	if data[0]["owned_by"] != "OpenAI" || data[1]["owned_by"] != "oh-my-aihub" || data[0]["object"] != "model" || data[0]["created"] != int64(0) {
		t.Fatalf("data = %v", data)
	}
}

type listingStore struct {
	Store
	online []OnlineModel
}

func (s listingStore) OnlineModels(context.Context) ([]OnlineModel, error) { return s.online, nil }

type listingCatalog struct {
	catalog.Store
	models []catalog.Model
}

func (c listingCatalog) ListModels(context.Context, bool) ([]catalog.Model, error) {
	return c.models, nil
}

func TestAvailableModelsAreFilteredByFormat(t *testing.T) {
	models := []catalog.Model{{ID: "gpt-x"}, {ID: "claude-x"}, {ID: "gemini-x"}, {ID: "both-x"}, {ID: "offline-x"}}
	engine := NewEngine(Dependencies{
		Catalog: catalog.NewService(listingCatalog{models: models}),
		Store: listingStore{online: []OnlineModel{
			{ID: "gpt-x", Formats: []channel.Format{channel.FormatOpenAIResponses}},
			{ID: "claude-x", Formats: []channel.Format{channel.FormatCodex}},
			{ID: "gemini-x", Formats: []channel.Format{channel.FormatGemini}},
			{ID: "both-x", Formats: []channel.Format{channel.FormatOpenAIResponses, channel.FormatCodex}},
			{ID: "custom-claude", Custom: true, Formats: []channel.Format{channel.FormatCodex}},
			{ID: "custom-gpt", Custom: true, Formats: []channel.Format{channel.FormatOpenAIResponses}},
			{ID: "no-format-x"},
		}},
	})
	key := KeyAuth{Aliases: map[string]string{"a-claude": "claude-x", "a-gpt": "gpt-x", "a-both": "both-x", "a-offline": "offline-x", "a-custom": "custom-claude"}}
	cases := map[string]struct {
		formats []channel.Format
		want    []string
	}{
		"openai":    {[]channel.Format{channel.FormatOpenAIResponses}, []string{"a-both", "a-gpt", "both-x", "custom-gpt", "gpt-x"}},
		"anthropic": {[]channel.Format{channel.FormatCodex}, []string{"a-both", "a-claude", "a-custom", "both-x", "claude-x", "custom-claude"}},
		"gemini":    {[]channel.Format{channel.FormatGemini}, []string{"gemini-x"}},
	}
	for name, c := range cases {
		names, err := engine.availableModels(context.Background(), key, c.formats)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, listed := range names {
			got = append(got, listed.id)
		}
		if !slices.Equal(got, c.want) {
			t.Fatalf("%s = %v, want %v", name, got, c.want)
		}
	}
	// A key limited to some models still sees them only in the formats served.
	limited := KeyAuth{AllowedModels: []string{"claude-x", "gpt-x"}}
	names, err := engine.availableModels(context.Background(), limited, []channel.Format{channel.FormatCodex})
	if err != nil || len(names) != 1 || names[0].id != "claude-x" {
		t.Fatalf("limited anthropic list = %v, %v", names, err)
	}
}
