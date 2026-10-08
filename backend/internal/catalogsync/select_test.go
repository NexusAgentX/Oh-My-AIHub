package catalogsync

import (
	"encoding/json"
	"testing"
)

func TestProviderSelectionNamesAndOrder(t *testing.T) {
	entries := []Entry{}
	for key, provider := range map[string]string{"z/shared:v1@date": "second", "b/shared:v1@date": "first", "a/shared:v1@date": "first", "ignored/extra": "other"} {
		raw, _ := json.Marshal(map[string]any{"mode": "chat", "provider": provider, "input_cost_per_token": 0, "output_cost_per_token": 0})
		entries = append(entries, Parse(key, raw, "1"))
	}
	chosen, report, options := Select(entries, []string{"first", "second"})
	if len(chosen) != 1 || chosen[0].Key != "a/shared:v1@date" || chosen[0].Model.ID != "shared:v1@date" || chosen[0].Model.DisplayName != "a/shared:v1@date" || len(report) != 2 || len(options) != 3 {
		t.Fatalf("selection %+v report %+v options %v", chosen, report, options)
	}
	chosen, _, _ = Select(entries, []string{"second", "first"})
	if chosen[0].Key != "z/shared:v1@date" {
		t.Fatal(chosen)
	}
	chosen, _, options = Select(entries, nil)
	if len(chosen) != 0 || len(options) != 3 {
		t.Fatal("empty list means all")
	}
}
