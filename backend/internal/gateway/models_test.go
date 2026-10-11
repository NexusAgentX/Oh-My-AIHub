package gateway

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
)

func listed(ids ...string) []listedModel {
	names := make([]listedModel, 0, len(ids))
	for _, id := range ids {
		names = append(names, listedModel{id: id, model: catalog.Model{ID: id, DisplayName: "Model " + id}})
	}
	return names
}

func TestAnthropicModelPageFollowsLimitAndCursors(t *testing.T) {
	names := listed("a", "b", "c", "d", "e")
	ids := func(page map[string]any) string {
		var out []string
		for _, item := range page["data"].([]map[string]any) {
			out = append(out, item["id"].(string))
		}
		return fmt.Sprintf("%v %v %v %v", out, page["has_more"], page["first_id"], page["last_id"])
	}
	cases := map[string]string{
		"":                    "[a b c d e] false a e",
		"limit=2":             "[a b] true a b",
		"limit=2&after_id=b":  "[c d] true c d",
		"limit=2&after_id=d":  "[e] false e e",
		"limit=2&before_id=e": "[c d] true c d",
		"limit=2&before_id=b": "[a] false a a",
		"after_id=missing":    "[] false <nil> <nil>",
		"limit=0":             "[a] true a a",
		"limit=not-a-number":  "[a b c d e] false a e",
	}
	for raw, want := range cases {
		query, _ := url.ParseQuery(raw)
		if got := ids(anthropicModelPage(names, query)); got != want {
			t.Fatalf("%q = %s, want %s", raw, got, want)
		}
	}
}

func TestAnthropicModelInfoFillsEveryField(t *testing.T) {
	window, output := int64(200000), int64(64000)
	names := []listedModel{{id: "claude-x", model: catalog.Model{ID: "claude-x", DisplayName: "Claude X", ContextWindow: &window, MaxOutputTokens: &output}}}
	item := anthropicModelPage(names, url.Values{})["data"].([]map[string]any)[0]
	if item["type"] != "model" || item["display_name"] != "Claude X" || item["created_at"] != "1970-01-01T00:00:00Z" || item["lifecycle"] != "active" ||
		*item["max_input_tokens"].(*int64) != window || *item["max_tokens"].(*int64) != output {
		t.Fatalf("item = %v", item)
	}
	for _, field := range []string{"deprecated_at", "retires_at", "line", "capabilities"} {
		if value, has := item[field]; !has || value != nil {
			t.Fatalf("%s must be present and null: %v", field, item)
		}
	}
}

func TestGeminiModelPageFollowsPageSizeAndTokens(t *testing.T) {
	names := listed("a", "b", "c")
	token := func(id string) string { return base64.RawURLEncoding.EncodeToString([]byte(id)) }
	describe := func(page map[string]any) string {
		var out []string
		for _, item := range page["models"].([]map[string]any) {
			out = append(out, item["baseModelId"].(string))
		}
		return fmt.Sprintf("%v %v", out, page["nextPageToken"])
	}
	cases := map[string]string{
		"":                                   "[a b c] <nil>",
		"pageSize=2":                         "[a b] " + token("c"),
		"pageSize=2&pageToken=" + token("c"): "[c] <nil>",
		"pageSize=0":                         "[a b c] <nil>",
	}
	for raw, want := range cases {
		query, _ := url.ParseQuery(raw)
		page, ok := geminiModelPage(names, query)
		if !ok || describe(page) != want {
			t.Fatalf("%q = %v %s, want %s", raw, ok, describe(page), want)
		}
	}
	for _, bad := range []string{"pageToken=" + token("missing"), "pageToken=!!!"} {
		query, _ := url.ParseQuery(bad)
		if _, ok := geminiModelPage(names, query); ok {
			t.Fatalf("%q must be rejected", bad)
		}
	}
}

func TestGeminiModelFillsRequiredAndKnownLimits(t *testing.T) {
	window := int64(1048576)
	names := []listedModel{
		{id: "gemini-x", model: catalog.Model{ID: "gemini-x", DisplayName: "Gemini X", ContextWindow: &window}},
	}
	page, _ := geminiModelPage(names, url.Values{})
	item := page["models"].([]map[string]any)[0]
	if item["name"] != "models/gemini-x" || item["baseModelId"] != "gemini-x" || item["version"] != "gemini-x" || item["displayName"] != "Gemini X" ||
		item["inputTokenLimit"] != window || fmt.Sprint(item["supportedGenerationMethods"]) != "[generateContent streamGenerateContent]" {
		t.Fatalf("item = %v", item)
	}
	if _, has := item["outputTokenLimit"]; has {
		t.Fatalf("an unknown limit is omitted: %v", item)
	}
}

func TestOpenAIModelListOwnerFallsBackToPlatform(t *testing.T) {
	names := []listedModel{{id: "gpt-x", model: catalog.Model{ID: "gpt-x", Provider: "OpenAI"}}, {id: "local", model: catalog.Model{ID: "local"}}}
	data := openAIModelList(names)["data"].([]map[string]any)
	if data[0]["owned_by"] != "OpenAI" || data[1]["owned_by"] != "oh-my-aihub" || data[0]["object"] != "model" || data[0]["created"] != int64(0) {
		t.Fatalf("data = %v", data)
	}
}
