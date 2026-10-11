package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDialectOfFollowsPathThenAnthropicVersion(t *testing.T) {
	cases := []struct {
		path, version string
		want          Dialect
	}{
		{"/v1/chat/completions", "", DialectOpenAI}, {"/v1/responses", "", DialectOpenAI}, {"/v1/models", "", DialectOpenAI},
		{"/v1/messages", "", DialectAnthropic}, {"/v1/messages/count_tokens", "", DialectAnthropic}, {"/v1/models", "2023-06-01", DialectAnthropic},
		{"/v1beta/models", "", DialectGemini}, {"/v1beta/models/x:generateContent", "2023-06-01", DialectGemini},
	}
	for _, c := range cases {
		request := httptest.NewRequest(http.MethodGet, c.path, nil)
		if c.version != "" {
			request.Header.Set("anthropic-version", c.version)
		}
		if got := DialectOf(request); got != c.want {
			t.Fatalf("%s (%q) = %d, want %d", c.path, c.version, got, c.want)
		}
	}
}

func TestWriteErrorUsesEachVendorShape(t *testing.T) {
	decode := func(dialect Dialect, status int, requestID string) (map[string]any, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		WriteError(recorder, dialect, status, "insufficient_balance", "积分余额不足", requestID)
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body, recorder
	}

	openAI, recorder := decode(DialectOpenAI, http.StatusPaymentRequired, "call-1")
	inner := openAI["error"].(map[string]any)
	if len(openAI) != 1 || inner["message"] != "积分余额不足" || inner["type"] != "insufficient_quota" || inner["code"] != "insufficient_balance" ||
		inner["param"] != nil || recorder.Code != http.StatusPaymentRequired || recorder.Header().Get("X-AIHub-Request-Id") != "call-1" {
		t.Fatalf("openai = %d %v", recorder.Code, openAI)
	}
	if _, has := inner["param"]; !has {
		t.Fatal("openai error must carry param")
	}

	anthropic, _ := decode(DialectAnthropic, http.StatusPaymentRequired, "call-1")
	inner = anthropic["error"].(map[string]any)
	if anthropic["type"] != "error" || inner["type"] != "billing_error" || inner["message"] != "积分余额不足" || anthropic["request_id"] != "call-1" || len(inner) != 2 {
		t.Fatalf("anthropic = %v", anthropic)
	}
	if withoutID, _ := decode(DialectAnthropic, http.StatusUnauthorized, ""); withoutID["request_id"] != nil {
		t.Fatalf("anthropic without id = %v", withoutID)
	}

	gemini, _ := decode(DialectGemini, http.StatusPaymentRequired, "call-1")
	inner = gemini["error"].(map[string]any)
	info := inner["details"].([]any)[0].(map[string]any)
	if inner["code"] != float64(402) || inner["status"] != "RESOURCE_EXHAUSTED" || inner["message"] != "积分余额不足" ||
		info["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" || info["reason"] != "INSUFFICIENT_BALANCE" || info["metadata"].(map[string]any)["request_id"] != "call-1" {
		t.Fatalf("gemini = %v", gemini)
	}
}

func TestErrorTypesFollowTheVendorTables(t *testing.T) {
	anthropic := map[int]string{400: "invalid_request_error", 401: "authentication_error", 402: "billing_error", 403: "permission_error", 404: "not_found_error",
		413: "request_too_large", 429: "rate_limit_error", 500: "api_error", 502: "api_error", 503: "overloaded_error", 504: "timeout_error"}
	google := map[int]string{400: "INVALID_ARGUMENT", 401: "UNAUTHENTICATED", 402: "RESOURCE_EXHAUSTED", 403: "PERMISSION_DENIED", 404: "NOT_FOUND",
		413: "INVALID_ARGUMENT", 429: "RESOURCE_EXHAUSTED", 500: "INTERNAL", 502: "UNAVAILABLE", 503: "UNAVAILABLE", 504: "DEADLINE_EXCEEDED"}
	openAI := map[int]string{400: "invalid_request_error", 401: "invalid_request_error", 402: "insufficient_quota", 404: "invalid_request_error",
		429: "rate_limit_error", 500: "server_error", 502: "server_error", 503: "service_unavailable_error"}
	for status, want := range anthropic {
		if got := anthropicErrorType(status); got != want {
			t.Fatalf("anthropic %d = %s, want %s", status, got, want)
		}
	}
	for status, want := range google {
		if got := googleStatus(status); got != want {
			t.Fatalf("google %d = %s, want %s", status, got, want)
		}
	}
	for status, want := range openAI {
		if got := openAIErrorType(status); got != want {
			t.Fatalf("openai %d = %s, want %s", status, got, want)
		}
	}
}
