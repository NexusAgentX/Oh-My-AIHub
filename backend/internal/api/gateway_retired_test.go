package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

// retiredKeyStore authenticates one usable platform key and nothing else: every
// retired path must be refused before any other store call happens.
type retiredKeyStore struct{ gateway.Store }

func (retiredKeyStore) LookupKey(context.Context, []byte) (gateway.KeyAuth, error) {
	return gateway.KeyAuth{KeyID: "key-retired", OwnerID: "account-retired", Enabled: true, AccountActive: true, ExpiresAt: nil}, nil
}

func (retiredKeyStore) TouchKey(context.Context, string, time.Time) error { return nil }

// TestRetiredGatewayEndpointsAnswerNotFound covers the endpoints removed from
// the gateway surface: resource reads/writes, resource lists, caches and token
// counting. Each must answer the protocol's unsupported_endpoint error instead
// of proxying, billing or keeping state.
func TestRetiredGatewayEndpointsAnswerNotFound(t *testing.T) {
	handler := NewHandler(Dependencies{Gateway: gateway.NewEngine(gateway.Dependencies{Store: retiredKeyStore{}})})
	retired := []struct{ method, path, want string }{
		{http.MethodGet, "/v1/responses/resp_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodPost, "/v1/responses/resp_synthetic/cancel", `"code":"unsupported_endpoint"`},
		{http.MethodGet, "/v1/responses/resp_synthetic/input_items", `"code":"unsupported_endpoint"`},
		{http.MethodDelete, "/v1/responses/resp_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodPost, "/v1/responses/compact", `"code":"unsupported_endpoint"`},
		{http.MethodPost, "/v1/responses/input_tokens", `"code":"unsupported_endpoint"`},
		{http.MethodGet, "/v1/chat/completions", `"code":"unsupported_endpoint"`},
		{http.MethodGet, "/v1/chat/completions/chat_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodPost, "/v1/chat/completions/chat_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodDelete, "/v1/chat/completions/chat_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodGet, "/v1/chat/completions/chat_synthetic/messages", `"code":"unsupported_endpoint"`},
		{http.MethodPost, "/v1/conversations", `"code":"unsupported_endpoint"`},
		{http.MethodGet, "/v1/conversations/conv_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodPost, "/v1/conversations/conv_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodDelete, "/v1/conversations/conv_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodPost, "/v1/conversations/conv_synthetic/items", `"code":"unsupported_endpoint"`},
		{http.MethodGet, "/v1/conversations/conv_synthetic/items", `"code":"unsupported_endpoint"`},
		{http.MethodGet, "/v1/conversations/conv_synthetic/items/item_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodDelete, "/v1/conversations/conv_synthetic/items/item_synthetic", `"code":"unsupported_endpoint"`},
		{http.MethodPost, "/v1beta/cachedContents", `"reason":"UNSUPPORTED_ENDPOINT"`},
		{http.MethodGet, "/v1beta/cachedContents", `"reason":"UNSUPPORTED_ENDPOINT"`},
		{http.MethodGet, "/v1beta/cachedContents/cache_synthetic", `"reason":"UNSUPPORTED_ENDPOINT"`},
		{http.MethodPatch, "/v1beta/cachedContents/cache_synthetic", `"reason":"UNSUPPORTED_ENDPOINT"`},
		{http.MethodDelete, "/v1beta/cachedContents/cache_synthetic", `"reason":"UNSUPPORTED_ENDPOINT"`},
		{http.MethodPost, "/v1/messages/count_tokens", `"type":"not_found_error"`},
		{http.MethodPost, "/v1beta/models/gemini-synthetic:countTokens", `"reason":"UNSUPPORTED_ENDPOINT"`},
	}
	for _, target := range retired {
		t.Run(target.method+" "+target.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(target.method, target.path, strings.NewReader(`{"model":"synthetic","input":"x"}`))
			request.Header.Set("Authorization", "Bearer "+apikey.Prefix+"retired-synthetic")
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), target.want) {
				t.Fatalf("%s %s = %d %s", target.method, target.path, recorder.Code, recorder.Body.String())
			}
		})
	}
}

// TestBackgroundResponseIsRejected keeps background:true from degrading into a
// synchronous call that quietly keeps upstream state.
func TestBackgroundResponseIsRejected(t *testing.T) {
	store := &backgroundRejectStore{retiredKeyStore: retiredKeyStore{}}
	handler := NewHandler(Dependencies{Gateway: gateway.NewEngine(gateway.Dependencies{Store: store})})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"platform/model","background":true,"input":"x"}`))
	request.Header.Set("Authorization", "Bearer "+apikey.Prefix+"retired-synthetic")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"background_unsupported"`) || !strings.Contains(recorder.Body.String(), "后台响应") {
		t.Fatalf("background request = %d %s", recorder.Code, recorder.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("rejected background call recorded %d calls, want 1", store.calls)
	}
}

type backgroundRejectStore struct {
	retiredKeyStore
	calls int
}

func (s *backgroundRejectStore) InsertCall(context.Context, gateway.CallInsert) (string, error) {
	s.calls++
	return "call-background-rejected", nil
}

func (s *backgroundRejectStore) FinishCall(context.Context, gateway.CallFinish) (gateway.FinishResult, error) {
	return gateway.FinishResult{Finished: true}, nil
}
