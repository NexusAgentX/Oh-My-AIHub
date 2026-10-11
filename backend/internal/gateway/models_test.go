package gateway

import (
	"fmt"
	"net/url"
	"testing"
	"time"
)

func TestAnthropicModelPageFollowsLimitAndCursors(t *testing.T) {
	names := []listedModel{{"a", "A"}, {"b", "B"}, {"c", "C"}, {"d", "D"}, {"e", "E"}}
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
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
		page := anthropicModelPage(names, query, created)
		if got := ids(page); got != want {
			t.Fatalf("%q = %s, want %s", raw, got, want)
		}
	}
	first := anthropicModelPage(names, url.Values{}, created)["data"].([]map[string]any)[0]
	if first["type"] != "model" || first["display_name"] != "A" || first["created_at"] != "2026-01-01T00:00:00Z" {
		t.Fatalf("item = %v", first)
	}
}
