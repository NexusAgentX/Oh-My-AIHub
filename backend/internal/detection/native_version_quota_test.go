package detection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestNativeQuotaUsesObservedHeadersWithoutGuessingBalanceOrWaiting(t *testing.T) {
	for _, value := range []string{"123", "", "-1", "NaN"} {
		t.Run(value, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if value != "" {
					w.Header().Set("X-RateLimit-Remaining-Tokens", value)
				}
				_ = json.NewEncoder(w).Encode(nativeObject{"data": []any{nativeObject{"id": "synthetic-model"}}})
			}))
			defer up.Close()
			r, s, service := nativeTestSession(t, up.URL)
			s.Target.Binding.TargetKind = "group"
			s.Target.ModelType = ""
			result := service.run(context.Background(), s, nativeItem(t, r, s, "openai-chat-completions.quota"), nil)
			want := "pass"
			if value == "-1" || value == "NaN" {
				want = "fail"
			}
			if result.State != want || calls != 1 {
				t.Fatalf("quota state %s calls %d", result.State, calls)
			}
			if value == "123" && (len(result.Quotas) != 1 || result.Quotas[0].Remaining != 123) {
				t.Fatal("observed numeric quota missing")
			}
			if value == "" && result.Assertions[0].State != "unobserved" || result.Assertions[1].State != "unobserved" {
				t.Fatal("missing quota or native balance was invented")
			}
		})
	}
}
func TestGeminiVersionsRemainIndependent(t *testing.T) {
	paths := []string{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("X-Goog-API-Key") == "" {
			t.Error("native authentication lost")
		}
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(nativeObject{"error": nativeObject{"code": 404, "status": "NOT_FOUND", "message": "unsupported version"}})
			return
		}
		_ = json.NewEncoder(w).Encode(nativeObject{"models": []any{nativeObject{"name": "models/synthetic-model"}}})
	}))
	defer up.Close()
	r, s, service := nativeTestSession(t, up.URL)
	s.Target.Binding.TargetKind = "group"
	s.Target.ModelType = ""
	result := service.run(context.Background(), s, nativeItem(t, r, s, "native-generate-content.versions"), nil)
	if !slices.Equal(paths, []string{"/v1beta/models", "/v1/models"}) || result.Assertions[0].State != "pass" || result.Assertions[1].State == "pass" {
		t.Fatalf("version proof collapsed: %+v", result)
	}
}
