package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

func TestCustomModelsBillOwnPricesAndWaitForAMultiplierOnceOfficial(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	channel := func(name, input, output string) string {
		created := e.apiJSON(sharer, http.StatusCreated, http.MethodPost, "/api/channels", map[string]any{
			"name": name, "base_url": relay.server.URL, "api_key": "upstream-key-" + name,
			"advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}}},
			"models": []any{map[string]any{
				"model_id":      "foo-1",
				"custom_prices": map[string]string{"input": input, "output": output, "cache_write": "0", "cache_read": "0"},
			}},
		})
		return created["channel"].(map[string]any)["id"].(string)
	}
	// Blends (3 × input + output) / 4: cheap = 3, dear = 4.
	cheap := channel("cheap", "1", "9")
	dear := channel("dear", "4", "4")
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)

	listed := e.call(secret, http.MethodGet, "/v1/models", nil, nil)
	if listed.Code != http.StatusOK || !json.Valid(listed.Body.Bytes()) || !slices.Contains(modelIDsOf(t, listed.Body.Bytes()), "foo-1") {
		t.Fatalf("/v1/models = %d %s", listed.Code, listed.Body.String())
	}

	// 1000 input and 500 output tokens at 1 and 9 points per million: 0.0055 points.
	first := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"foo-1"}`), nil)
	if first.Code != http.StatusOK {
		t.Fatalf("call %d %s", first.Code, first.Body.String())
	}
	id := first.Header().Get("X-AIHub-Request-Id")
	if row := e.callRow(id); row.CostNano != 5_500_000 || row.FinalChannel != cheap || row.ModelID != "foo-1" {
		t.Fatalf("custom call = %+v", row)
	}
	var custom bool
	if err := e.pool.QueryRow(ctx, `SELECT (price_snapshot->>'custom')::boolean FROM calls WHERE id = $1`, id).Scan(&custom); err != nil || !custom {
		t.Fatalf("snapshot custom = %v %v", custom, err)
	}
	if unknown := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"foo-2"}`), nil); unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown model = %d", unknown.Code)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE channel_models SET multiplier_nano = 2000000000 WHERE channel_id = $1`, cheap); err == nil {
		t.Fatal("a custom model accepted a multiplier")
	}

	// The catalog takes the name over: both rows wait for a multiplier.
	e.apiJSON(e.admin, http.StatusCreated, http.MethodPost, "/api/admin/models", map[string]any{
		"id": "foo-1", "display_name": "Foo 1",
		"base_prices": map[string]string{"input": "2", "output": "2", "cache_write": "0", "cache_read": "0"},
	})
	for _, channelID := range []string{cheap, dear} {
		detail := e.apiJSON(sharer, http.StatusOK, http.MethodGet, "/api/channels/"+channelID, nil)
		model := detail["channel"].(map[string]any)["models"].([]any)[0].(map[string]any)
		if model["pending_multiplier"] != true || model["enabled"] != false || model["custom_prices"] != nil {
			t.Fatalf("converted model = %v", model)
		}
		if events := e.channelEvents(channelID); events[len(events)-1] != "model_pending_multiplier" {
			t.Fatalf("events = %v", events)
		}
	}
	var converted int
	if err := e.pool.QueryRow(ctx, `SELECT jsonb_array_length(detail->'converted_custom_channels') FROM audit_log WHERE action = 'model.created' AND target_id = 'foo-1'`).Scan(&converted); err != nil || converted != 2 {
		t.Fatalf("audit converted = %d %v", converted, err)
	}
	if paused := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"foo-1"}`), nil); paused.Code != http.StatusServiceUnavailable {
		t.Fatalf("pending call = %d %s", paused.Code, paused.Body.String())
	}
	e.apiJSON(e.admin, http.StatusConflict, http.MethodDelete, "/api/admin/models/foo-1", nil)
	recorder := e.api(sharer, http.MethodPost, "/api/channels", map[string]any{
		"name": "late", "base_url": relay.server.URL, "api_key": "upstream-key-late",
		"models": []any{map[string]any{"model_id": "foo-1",
			"custom_prices": map[string]string{"input": "1", "output": "1", "cache_write": "0", "cache_read": "0"}}},
	})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("custom model with a catalog name = %d %s", recorder.Code, recorder.Body.String())
	}

	// Saving without enabling keeps it pending; enabling with a multiplier resumes it.
	save := func(channelID string, enabled bool) map[string]any {
		updated := e.apiJSON(sharer, http.StatusOK, http.MethodPatch, "/api/channels/"+channelID, map[string]any{
			"models": []any{map[string]any{"model_id": "foo-1", "multiplier": "1.5", "enabled": enabled}},
		})
		return updated["channel"].(map[string]any)["models"].([]any)[0].(map[string]any)
	}
	if model := save(dear, false); model["pending_multiplier"] != true {
		t.Fatalf("saved disabled = %v", model)
	}
	if model := save(dear, true); model["pending_multiplier"] != false || model["enabled"] != true || model["multiplier"] != "1.5" {
		t.Fatalf("enabled = %v", model)
	}
	// 1000 × 2 + 500 × 2 points per million × 1.5 = 0.0045 points.
	resumed := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"foo-1"}`), nil)
	if resumed.Code != http.StatusOK {
		t.Fatalf("resumed call = %d %s", resumed.Code, resumed.Body.String())
	}
	if row := e.callRow(resumed.Header().Get("X-AIHub-Request-Id")); row.CostNano != 4_500_000 || row.FinalChannel != dear {
		t.Fatalf("resumed call = %+v", row)
	}
	e.assertZeroSum()
}

func modelIDsOf(t *testing.T, body []byte) []string {
	t.Helper()
	var listed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range listed.Data {
		ids = append(ids, item.ID)
	}
	return ids
}
