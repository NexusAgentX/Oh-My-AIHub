package api

import (
	"net/http"
	"testing"
)

func TestModelConfigurationRejectsRetiredPricingFields(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	admin := p.admin(t)
	prices := map[string]any{"input": "1", "output": "2", "cache_read": "0", "cache_write": "0"}
	admin.expect(t, http.StatusCreated, http.MethodPost, "/api/admin/models", map[string]any{"id": "pricing-cleanup", "display_name": "Pricing cleanup", "base_prices": prices})
	for _, tc := range []struct {
		body map[string]any
		code int
	}{
		{map[string]any{"input_modalities": []string{"file"}}, http.StatusUnprocessableEntity},
		{map[string]any{"output_modalities": []string{"text", "file"}}, http.StatusUnprocessableEntity},
		{map[string]any{"base_prices": map[string]any{"input": "1", "output": "2", "cache_read": "0", "cache_write": "0", "geo_multipliers": map[string]string{"us": "1.1"}}}, http.StatusBadRequest},
		{map[string]any{"price_tiers": []any{map[string]any{"thinking_mode": "qwen_thinking", "prices": prices}}}, http.StatusBadRequest},
		{map[string]any{"price_tiers": []any{map[string]any{"service_tier": "openai:priority", "prices": prices}}}, http.StatusUnprocessableEntity},
	} {
		if rec := admin.call(t, http.MethodPatch, "/api/admin/models/pricing-cleanup", tc.body); rec.Code != tc.code {
			t.Fatalf("body=%v status=%d want=%d response=%s", tc.body, rec.Code, tc.code, rec.Body.String())
		}
	}
	// Removing file must not reject custom modality labels accepted before.
	admin.expect(t, http.StatusOK, http.MethodPatch, "/api/admin/models/pricing-cleanup", map[string]any{
		"input_modalities": []string{"document"}, "output_modalities": []string{"custom"},
	})
	for _, serviceTier := range []string{"openai:fast", "openai:ultrafast", "anthropic:priority", "gemini:priority"} {
		model := admin.expect(t, http.StatusOK, http.MethodPatch, "/api/admin/models/pricing-cleanup", map[string]any{
			"price_tiers":      []any{map[string]any{"service_tier": serviceTier, "prices": prices}},
			"input_modalities": []string{"text", "image", "audio", "video"},
		})["model"].(map[string]any)
		tier := model["price_tiers"].([]any)[0].(map[string]any)
		if tier["service_tier"] != serviceTier {
			t.Fatalf("service tier did not round trip: %v", tier)
		}
		if _, found := tier["thinking_mode"]; found {
			t.Fatal("retired thinking condition returned")
		}
		if _, found := model["base_prices"].(map[string]any)["geo_multipliers"]; found {
			t.Fatal("retired regional pricing returned")
		}
	}
}
