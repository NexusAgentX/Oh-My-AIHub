package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/supplier"
)

type fakeSupplierStore struct {
	mu       sync.Mutex
	values   []supplier.Supplier
	channels *fakeChannelStore
}

func (s *fakeSupplierStore) List(context.Context) ([]supplier.Supplier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]supplier.Supplier{}, s.values...), nil
}
func (s *fakeSupplierStore) Save(_ context.Context, _ string, value supplier.Supplier, create bool) (supplier.Supplier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, other := range s.values {
		if other.ID != value.ID {
			for _, a := range other.Endpoints {
				for _, b := range value.Endpoints {
					if a.BaseURL == b.BaseURL {
						return value, supplier.ErrConflict
					}
				}
			}
		}
	}
	value.UpdatedAt = time.Now()
	if create {
		s.values = append(s.values, value)
		return value, nil
	}
	for i, other := range s.values {
		if other.ID == value.ID {
			s.values[i] = value
			return value, nil
		}
	}
	return value, supplier.ErrNotFound
}
func (s *fakeSupplierStore) Listings(context.Context) ([]supplier.Listing, error) {
	s.channels.mu.Lock()
	defer s.channels.mu.Unlock()
	result := []supplier.Listing{}
	for _, c := range s.channels.channels {
		if c.Status != channel.StatusListed {
			continue
		}
		result = append(result, supplier.Listing{ID: c.ID, Name: c.Name, Summary: c.Summary, Description: c.Description, BaseURL: c.BaseURL, OwnerID: c.Owner.ID, OwnerName: c.Owner.DisplayName, Advanced: c.Advanced})
	}
	return result, nil
}
func TestSupplierDirectoryAndHall(t *testing.T) {
	p := newPlatform(t)
	admin := p.admin(t)
	owner, _ := p.member(t, "owner")
	viewer, _ := p.member(t, "viewer")
	seedModel(t, admin, "deepseek-chat")
	body := map[string]any{"name": "示例供应商", "kind": "relay", "summary": "介绍", "website_url": "https://supplier.example", "visible": true, "endpoints": []map[string]string{{"base_url": "https://RELAY.example:443/v1/", "label": "主站"}, {"base_url": "https://backup.example", "label": "备用"}}}
	for _, path := range []string{"/api/suppliers", "/api/channel-hall", "/api/suppliers/00000000-0000-4000-8000-000000000001", "/api/channel-hall/00000000-0000-4000-8000-000000000001"} {
		viewer.expect(t, 401, http.MethodGet, path, nil, withoutCookie)
	}
	viewer.expect(t, 403, http.MethodPost, "/api/admin/suppliers", body)
	created := admin.expect(t, 201, http.MethodPost, "/api/admin/suppliers", body)
	id := created["id"].(string)
	admin.expect(t, 409, http.MethodPost, "/api/admin/suppliers", body)
	admin.expect(t, 200, http.MethodGet, "/api/admin/suppliers", nil)
	public := viewer.expect(t, 200, http.MethodGet, "/api/suppliers", nil)["items"].([]any)[0].(map[string]any)
	if public["endpoints"] != nil || public["visible"] != nil {
		t.Fatal("private supplier configuration exposed")
	}
	for _, address := range []string{"https://relay.example", "https://relay.example/v1", "https://backup.example/"} {
		recognized := viewer.expect(t, 200, http.MethodPost, "/api/suppliers/recognize", map[string]string{"base_url": address})
		if recognized["supplier"].(map[string]any)["id"] != id {
			t.Fatal("recognition mismatch")
		}
	}
	for _, address := range []string{"https://sub.relay.example", "https://relay.example/other"} {
		if viewer.expect(t, 200, http.MethodPost, "/api/suppliers/recognize", map[string]string{"base_url": address})["supplier"] != nil {
			t.Fatal("inexact match")
		}
	}
	ch := owner.expect(t, 201, http.MethodPost, "/api/channels", map[string]any{"name": "共享测试", "summary": "简短介绍", "description": "详细 **说明**", "base_url": "https://relay.example/v1", "api_key": "sk-secret-never-expose", "models": []map[string]any{{"model_id": "deepseek-chat", "multiplier": "1.2", "formats": []string{"openai_chat"}}}})["channel"].(map[string]any)
	channelID := ch["id"].(string)
	impact := admin.expect(t, 200, http.MethodPost, "/api/admin/suppliers/preview", map[string]any{"endpoints": body["endpoints"]})
	if impact["linked_channels"] != float64(1) {
		t.Fatal("incorrect impact preview")
	}
	viewer.expect(t, 403, http.MethodPost, "/api/admin/suppliers/preview", map[string]any{"endpoints": body["endpoints"]})
	if ch["summary"] != "简短介绍" {
		t.Fatal("summary not persisted")
	}
	hall := viewer.expect(t, 200, http.MethodGet, "/api/channel-hall?model=deepseek-chat&supplier="+id, nil)
	if len(hall["items"].([]any)) != 1 {
		t.Fatal("hall filters mismatch")
	}
	detail := viewer.expect(t, 200, http.MethodGet, "/api/channel-hall/"+channelID, nil)
	encoded, _ := json.Marshal(detail)
	for _, private := range []string{"base_url", "api_key", "header_rules", "revenue", "sk-secret", "relay.example"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("public response leaked %s", private)
		}
	}
	supplierDetail := viewer.expect(t, 200, http.MethodGet, "/api/suppliers/"+id, nil)
	if len(supplierDetail["models"].([]any)) != 1 || len(supplierDetail["channels"].([]any)) != 1 {
		t.Fatal("aggregation mismatch")
	}
	owner.expect(t, 200, http.MethodPatch, "/api/channels/"+channelID, map[string]string{"summary": "修改简介", "description": ""})
	if viewer.expect(t, 200, http.MethodGet, "/api/channel-hall/"+channelID, nil)["summary"] != "修改简介" {
		t.Fatal("stale metadata")
	}
	seedModel(t, admin, "other-model")
	owner.expect(t, 200, http.MethodPatch, "/api/channels/"+channelID, map[string]any{"models": []map[string]any{
		{"model_id": "deepseek-chat", "enabled": true, "multiplier": "1.2", "formats": []string{"openai_chat"}},
		{"model_id": "other-model", "enabled": true, "multiplier": "1", "formats": []string{"gemini"}},
	}})
	for query, count := range map[string]int{"model=deepseek-chat&format=gemini": 0, "model=other-model&format=gemini": 1, "format=gemini": 1} {
		if result := viewer.expect(t, 200, http.MethodGet, "/api/channel-hall?"+query, nil); len(result["items"].([]any)) != count {
			t.Fatalf("model and format must match the same offer: %s", query)
		}
	}
	body["endpoints"] = []map[string]string{{"base_url": "https://moved.example", "label": "新站"}}
	admin.expect(t, 200, http.MethodPut, "/api/admin/suppliers/"+id, body)
	if viewer.expect(t, 200, http.MethodGet, "/api/channel-hall/"+channelID, nil)["supplier"] != nil {
		t.Fatal("stale recognition")
	}
	body["visible"] = false
	admin.expect(t, 200, http.MethodPut, "/api/admin/suppliers/"+id, body)
	viewer.expect(t, 404, http.MethodGet, "/api/suppliers/"+id, nil)
	owner.expect(t, 200, http.MethodPatch, "/api/channels/"+channelID, map[string]string{"status": "unlisted"})
	viewer.expect(t, 404, http.MethodGet, "/api/channel-hall/"+channelID, nil)
	icon := admin.expect(t, 200, http.MethodPost, "/api/admin/suppliers/icon", map[string]string{"website_url": "https://supplier.example"})
	if !strings.HasPrefix(icon["icon_data"].(string), "data:image/png;base64,") {
		t.Fatal("icon is not a local raster")
	}
	viewer.expect(t, 403, http.MethodPost, "/api/admin/suppliers/icon", map[string]string{"website_url": "https://supplier.example"})
}

func TestSupplierAggregatesCustomPricesWithoutMixingChannels(t *testing.T) {
	p := newPlatform(t)
	admin := p.admin(t)
	owner, _ := p.member(t, "offerowner")
	created := admin.expect(t, 201, http.MethodPost, "/api/admin/suppliers", map[string]any{"name": "Test", "kind": "relay", "website_url": "https://supplier.example", "visible": true, "endpoints": []map[string]string{{"base_url": "https://relay.example"}}})
	id := created["id"].(string)
	var channelIDs []string
	for i, price := range []map[string]string{{"input": "1", "output": "9", "cache_read": "4", "cache_write": "5"}, {"input": "2", "output": "1", "cache_read": "8", "cache_write": "7"}} {
		c := owner.expect(t, 201, http.MethodPost, "/api/channels", map[string]any{"name": "Custom offer", "base_url": "https://relay.example", "api_key": "sk-only-fixture-key", "models": []map[string]any{{"model_id": "custom-one", "formats": []string{[]string{"openai_chat", "anthropic"}[i]}, "custom_prices": price}}})["channel"].(map[string]any)
		channelIDs = append(channelIDs, c["id"].(string))
	}
	page := owner.expect(t, 200, http.MethodGet, "/api/suppliers/"+id, nil)
	models := page["models"].([]any)
	if len(models) != 1 {
		t.Fatal("model not deduplicated")
	}
	model := models[0].(map[string]any)
	prices := model["lowest_prices"].(map[string]any)
	if prices["input"] != "2" || prices["output"] != "1" || prices["cache_read"] != "8" || model["online_channels"] != float64(2) || len(model["formats"].([]any)) != 2 {
		t.Fatalf("aggregation mixed prices: %+v", model)
	}
	first := owner.expect(t, 200, http.MethodGet, "/api/channel-hall?limit=1", nil)
	next := first["next_cursor"].(string)
	second := owner.expect(t, 200, http.MethodGet, "/api/channel-hall?limit=1&cursor="+next, nil)
	if second["next_cursor"] != nil || second["items"].([]any)[0].(map[string]any)["id"] == first["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("invalid pagination")
	}
	owner.expect(t, 200, http.MethodPatch, "/api/channels/"+channelIDs[1], map[string]any{"models": []map[string]any{{"model_id": "custom-one", "enabled": false, "formats": []string{"anthropic"}, "custom_prices": map[string]string{"input": "2", "output": "1", "cache_read": "8", "cache_write": "7"}}}})
	page = owner.expect(t, 200, http.MethodGet, "/api/suppliers/"+id, nil)
	model = page["models"].([]any)[0].(map[string]any)
	if model["online_channels"] != float64(1) || model["lowest_prices"].(map[string]any)["input"] != "1" {
		t.Fatal("disabled offer remained public")
	}
}

func TestSupplierIconErrorMessages(t *testing.T) {
	for _, tc := range []struct {
		err          error
		code, phrase string
	}{
		{supplier.ErrIconFormat, "icon_unsupported", "格式"},
		{supplier.ErrIconTimeout, "icon_timeout", "超时"},
		{supplier.ErrIconDownload, "icon_download_failed", "下载失败"},
		{supplier.ErrIconRedirect, "icon_redirect", "跳转"},
		{supplier.ErrIconTooLarge, "icon_too_large", "大小限制"},
		{&supplier.IconHTTPError{Status: 403}, "icon_http_error", "HTTP 403"},
		{supplier.ErrIconNotFound, "icon_unavailable", "未找到"},
		{supplier.ErrInvalid, "icon_invalid_url", "HTTPS"},
	} {
		code, message := supplierIconError(tc.err)
		if code != tc.code || !strings.Contains(message, tc.phrase) {
			t.Fatalf("%v: %s %s", tc.err, code, message)
		}
	}
}
