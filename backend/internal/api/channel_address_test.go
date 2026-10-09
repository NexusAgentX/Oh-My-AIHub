package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestChannelAddressErrorResponse(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    string
		message string
	}{
		{channel.ErrInvalidBaseURL, "invalid_base_url", "Base URL 无效：请填写 HTTPS 中转站根地址，可带 /v1 后缀，不要包含具体接口路径、查询参数或片段"},
		{channel.ErrUnsafeUpstream, "unsafe_upstream", "上游地址未通过出站安全校验"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeDomainError(recorder, errors.Join(errors.New("context"), tc.err))
			var body struct {
				Error   string
				Message string
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusUnprocessableEntity || body.Error != tc.code || body.Message != tc.message {
				t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
