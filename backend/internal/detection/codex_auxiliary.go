package detection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

func (n *nativeScene) codexImages(ctx context.Context) {
	variants := slices.Clone(n.s.Target.Profile.Images)
	if len(variants) == 0 {
		variants = []ImageVariant{{}}
	}
	for _, endpoint := range []string{"codex.images", "codex.images.edit"} {
		for _, variant := range variants {
			out, image := n.imageRequest(ctx, endpoint, "json", variant)
			n.result.Images = append(n.result.Images, image)
			label := "native_generation"
			if endpoint == "codex.images.edit" {
				label = "native_edit"
			}
			n.check(label, out, image.Verified == 1 && image.OptionsConfirmed)
			n.check("reliable_products", out, image.Verified == 1 && image.Returned == 1)
		}
	}
}

func (n *nativeScene) codexSearch(ctx context.Context) {
	query := nativeObject{"search_query": []any{nativeObject{"q": "OpenAI API documentation", "domains": []any{"platform.openai.com"}}}, "response_length": "short"}
	body := nativeObject{"id": "aihub-synthetic-search", "model": n.s.Target.Model, "input": "Find the official OpenAI API documentation.", "commands": query, "max_output_tokens": 512}
	raw, _ := json.Marshal(body)
	search := n.call(ctx, Request{EndpointID: "codex.search", Path: "/backend-api/codex/alpha/search", Headers: http.Header{"Content-Type": {"application/json"}}, Body: raw, Category: "query"}, true)
	_, hasOutput := search.Reply.Object["output"].(string)
	results := list(search.Reply.Object["results"])
	valid := hasOutput && len(results) > 0 && len(results) <= 256
	firstURL := ""
	for _, result := range results {
		value := str(object(result)["url"])
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			valid = false
		} else if firstURL == "" {
			firstURL = value
		}
	}
	valid = valid && firstURL != ""
	n.check("native_search", search, hasOutput)
	n.check("search_result_structure", search, valid)
	if search.State != "pass" || !valid {
		n.uncertain("search_result_continuation", search)
		return
	}
	// Consume the actual output using the same native encrypted tool-result
	// shape as the fixed CLI. URLs are inspected, never fetched by the gateway.
	var output any = str(search.Reply.Object["output"])
	if encrypted := str(search.Reply.Object["encrypted_output"]); encrypted != "" {
		output = []any{nativeObject{"type": "encrypted_content", "encrypted_content": encrypted}}
	}
	next := codexBase(n.s.Target.Model)
	next["input"] = []any{
		nativeObject{"role": "user", "content": "Search for the official OpenAI API documentation. After receiving the actual search result, return only the first result URL."},
		nativeObject{"type": "function_call", "call_id": "aihub-search-result", "namespace": "web", "name": "run", "arguments": string(mustNativeJSON(query))},
		nativeObject{"type": "function_call_output", "call_id": "aihub-search-result", "output": output},
	}
	out := n.codexInfer(ctx, next, "")
	n.linkedCheck("search_result_continuation", search, out, nativeEndedNormally(out.Reply) && strings.TrimSpace(out.Reply.Text) == firstURL)
}

func mustNativeJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}
