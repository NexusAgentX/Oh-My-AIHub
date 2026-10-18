package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestNativeGroupCatalogSharesReadWithNegativeMatrix(t *testing.T) {
	for _, protocol := range []string{"openai-chat-completions", "openai-responses", "native-messages", "native-generate-content"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				key := r.Header.Get("Authorization")
				if protocol == "native-messages" {
					key = r.Header.Get("x-api-key")
				} else if protocol == "native-generate-content" {
					key = r.Header.Get("x-goog-api-key")
				}
				status, kind, message := 0, "", ""
				if key == "" || strings.Contains(key, "aihub-invalid-") {
					status, kind, message = 401, "authentication_error", "Invalid synthetic API key"
				} else if r.Method == "GET" {
					if strings.HasSuffix(r.URL.Path, "/synthetic-model") {
						if protocol == "native-generate-content" {
							_ = json.NewEncoder(w).Encode(nativeObject{"name": "models/synthetic-model"})
						} else {
							_ = json.NewEncoder(w).Encode(nativeObject{"id": "synthetic-model"})
						}
					} else {
						switch protocol {
						case "native-messages":
							_ = json.NewEncoder(w).Encode(nativeObject{"data": []any{nativeObject{"id": "synthetic-model"}}, "has_more": false, "first_id": "synthetic-model", "last_id": "synthetic-model"})
						case "native-generate-content":
							_ = json.NewEncoder(w).Encode(nativeObject{"models": []any{nativeObject{"name": "models/synthetic-model"}}})
						default:
							_ = json.NewEncoder(w).Encode(nativeObject{"object": "list", "data": []any{nativeObject{"id": "synthetic-model"}}})
						}
					}
					return
				} else {
					body, _ := io.ReadAll(r.Body)
					if strings.Contains(string(body), "aihub-unknown-") || strings.Contains(r.URL.Path, "aihub-unknown-") {
						status, kind, message = 404, "model_not_found", "The requested model was not found"
					} else {
						status, kind, message = 400, "invalid_request_error", "Invalid JSON request"
					}
				}
				w.WriteHeader(status)
				root := nativeObject{"error": nativeObject{"type": kind, "code": kind, "message": message}}
				if protocol == "native-messages" {
					root["type"] = "error"
				} else if protocol == "native-generate-content" {
					root = nativeObject{"error": nativeObject{"code": status, "status": "INVALID_ARGUMENT", "message": message}}
				}
				_ = json.NewEncoder(w).Encode(root)
			}))
			defer up.Close()
			r, session, service := nativeTestSession(t, up.URL)
			session.Target.Binding.TargetKind = "group"
			session.Target.ModelType = ""
			session.Target.Model = ""
			plan, err := r.Plan(session.Target)
			if err != nil {
				t.Fatal(err)
			}
			for _, family := range []string{"errors", "catalog"} {
				var item Planned
				for _, p := range plan.Items {
					if slices.Contains(p.Requirements, protocol+"."+family) {
						item = p
						break
					}
				}
				result := service.run(context.Background(), session, item, nil)
				if result.State != "pass" || result.Source != "synthetic" {
					t.Fatalf("group %s: %+v", family, result)
				}
			}
			if calls != 6 || session.usage.HTTP != 6 || session.usage.Query != 2 {
				t.Fatalf("catalog not shared: calls=%d usage=%+v", calls, session.usage)
			}
		})
	}
}

func TestSchemaVerificationChecksGeneratedValues(t *testing.T) {
	valid, _ := decodeNative([]byte(`{"label":"violet","meta":{"ok":true},"items":[{"value":7}]}`))
	if !matchesNativeSchema(valid) {
		t.Fatal("fixed valid schema example rejected")
	}
	for _, raw := range []string{`{"label":"blue","meta":{"ok":true},"items":[{"value":7}]}`, `{"label":"violet","items":[{"value":7}]}`, `{"label":"violet","meta":{"ok":"true"},"items":[{"value":7}]}`, `{"label":"violet","meta":{"ok":true},"items":[{"value":8}]}`, `{"label":"violet","meta":{"ok":true},"items":[{"value":7,"extra":true}]}`} {
		v, _ := decodeNative([]byte(raw))
		if matchesNativeSchema(v) {
			t.Fatalf("schema accepted invalid generated values: %s", raw)
		}
	}
}
