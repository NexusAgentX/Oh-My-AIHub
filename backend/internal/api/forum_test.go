package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/forum"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
)

func TestForumUploadBoundsAndConcurrency(t *testing.T) {
	spec := loadOpenAPI(t)
	cases := []struct {
		name  string
		size  int
		field string
		busy  bool
		want  int
	}{{"oversize", forum.MaxFileSize + 1, "file", false, 413}, {"wrong field", 1, "other", false, 400}, {"busy", 1, "file", true, 429}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &app{rateLimits: newRateLimits()}
			if c.busy {
				a.forumUploadSlots <- struct{}{}
				a.forumUploadSlots <- struct{}{}
			}
			var buf bytes.Buffer
			writer := multipart.NewWriter(&buf)
			part, _ := writer.CreateFormFile(c.field, "a.txt")
			part.Write(bytes.Repeat([]byte{'a'}, c.size))
			writer.Close()
			req := httptest.NewRequest("POST", "https://hub.example/api/forum/attachments", &buf)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req = req.WithContext(context.WithValue(req.Context(), accountContextKey, identity.Account{ID: "owner"}))
			rec := httptest.NewRecorder()
			a.forumUpload(rec, req)
			if rec.Code != c.want {
				t.Fatal(rec.Code, rec.Body.String())
			}
			spec.assertResponse(t, "POST", "/api/forum/attachments", rec)
		})
	}
}
func TestForumUploadAccountRateLimit(t *testing.T) {
	a := &app{rateLimits: newRateLimits()}
	for range 30 {
		if !a.forumUploads.take("owner") {
			t.Fatal("early rate limit")
		}
	}
	req := httptest.NewRequest(http.MethodPost, "https://hub.example/api/forum/attachments", nil)
	req = req.WithContext(context.WithValue(req.Context(), accountContextKey, identity.Account{ID: "owner"}))
	rec := httptest.NewRecorder()
	a.forumUpload(rec, req)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "60" {
		t.Fatal(rec.Code, rec.Header())
	}
}
