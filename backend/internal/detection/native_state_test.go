package detection

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeStoredStateAndBackgroundCleanOnlyOwnResources(t *testing.T) {
	for _, family := range []string{"stored", "state", "background"} {
		t.Run(family, func(t *testing.T) {
			responses, conversations, chats := map[string]nativeObject{}, map[string]nativeObject{}, map[string]nativeObject{}
			items := map[string]nativeObject{}
			created, deleted, inferences := 0, 0, 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
				failure := func() {
					w.WriteHeader(400)
					write(nativeObject{"error": nativeObject{"type": "invalid_request_error", "code": "invalid_request", "param": "previous_response_id", "message": "Invalid previous_response_id or conflicting conversation"}})
				}
				page := func(data ...any) {
					write(nativeObject{"object": "list", "data": data, "has_more": false, "first_id": "first", "last_id": "last"})
				}
				if r.Method == "POST" && r.URL.Path == "/v1/responses" {
					if previous := str(body["previous_response_id"]); previous != "" && (responses[previous] == nil || body["conversation"] != nil) {
						failure()
						return
					}
					inferences++
					text := replayCode
					if strings.Contains(string(raw), nativeMarker) {
						text = nativeMarker
					}
					if strings.Contains(string(raw), "Reply only OK.") && !strings.Contains(string(raw), "Reply only with the synthetic code I asked") {
						text = "OK"
					}
					o := nativeFixtureWithCalls("openai-responses", text, nil)
					id := fmt.Sprintf("resp_%d", inferences)
					o["id"] = id
					object(list(o["output"])[0])["id"] = fmt.Sprintf("msg_%d", inferences)
					if body["store"] == true {
						responses[id] = cloneNative(o)
						created++
					}
					if body["background"] == true {
						o["status"] = "queued"
						o["output"] = []any{}
						delete(o, "usage")
					}
					write(o)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/responses/") {
					pieces := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/responses/"), "/")
					id := pieces[0]
					o := responses[id]
					if o == nil {
						t.Errorf("touched unknown response %s", id)
						failure()
						return
					}
					if len(pieces) > 1 && pieces[1] == "cancel" {
						o["status"] = "cancelled"
						o["output"] = []any{}
						o["usage"] = nativeObject{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
						write(o)
						return
					}
					if len(pieces) > 1 && pieces[1] == "input_items" {
						page(nativeObject{"id": "input_1", "type": "message"})
						return
					}
					if r.Method == "DELETE" {
						deleted++
						delete(responses, id)
						write(nativeObject{"id": id, "deleted": true})
						return
					}
					if r.URL.Query().Get("stream") == "true" {
						if r.URL.Query().Get("starting_after") != "0" {
							t.Error("missing resume cursor")
						}
						w.Header().Set("Content-Type", "text/event-stream")
						event, _ := json.Marshal(nativeObject{"type": "response.completed", "sequence_number": 1, "response": o})
						_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", event)
						return
					}
					write(o)
					return
				}
				if r.URL.Path == "/v1/conversations" && r.Method == "POST" {
					created++
					o := nativeObject{"id": "conv_owned", "object": "conversation", "metadata": body["metadata"]}
					conversations["conv_owned"] = o
					write(o)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/conversations/") {
					pieces := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/conversations/"), "/")
					id := pieces[0]
					o := conversations[id]
					if o == nil {
						t.Error("unknown conversation touched")
						failure()
						return
					}
					if len(pieces) > 1 {
						if len(pieces) == 2 && r.Method == "POST" {
							item := object(list(body["items"])[0])
							item["id"] = "item_owned"
							items["item_owned"] = item
							page(item)
							return
						}
						if len(pieces) == 2 {
							page(items["item_owned"])
							return
						}
						if pieces[2] != "item_owned" {
							t.Error("unknown item touched")
							failure()
							return
						}
						if r.Method == "DELETE" {
							delete(items, "item_owned")
							write(nativeObject{"id": "item_owned", "deleted": true})
							return
						}
						write(items["item_owned"])
						return
					}
					if r.Method == "DELETE" {
						deleted++
						delete(conversations, id)
						write(nativeObject{"id": id, "deleted": true})
						return
					}
					if r.Method == "POST" {
						o["metadata"] = body["metadata"]
					}
					write(o)
					return
				}
				if r.URL.Path == "/v1/chat/completions" {
					if r.Method == "POST" {
						inferences++
						created++
						o := nativeFixtureWithCalls("openai-chat-completions", nativeMarker, nil)
						o["id"], o["metadata"] = "chat_owned", body["metadata"]
						chats["chat_owned"] = o
						write(o)
						return
					}
					page(nativeObject{"id": "chat_foreign", "private": "another-users-private-body"}, chats["chat_owned"])
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/chat/completions/") {
					pieces := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/chat/completions/"), "/")
					id := pieces[0]
					o := chats[id]
					if o == nil {
						t.Error("foreign chat touched")
						failure()
						return
					}
					if len(pieces) > 1 {
						page(nativeObject{"id": "chat_input", "role": "user", "content": nativeMarker})
						return
					}
					if r.Method == "DELETE" {
						deleted++
						delete(chats, id)
						write(nativeObject{"id": id, "deleted": true})
						return
					}
					if r.Method == "POST" {
						o["metadata"] = body["metadata"]
					}
					write(o)
					return
				}
				t.Errorf("unrecognized native operation %s %s", r.Method, r.URL.Path)
				failure()
			}))
			defer up.Close()
			r, s, service := nativeTestSession(t, up.URL)
			protocol := "openai-responses"
			if family == "stored" {
				protocol = "openai-chat-completions"
			}
			result := service.run(context.Background(), s, nativeItem(t, r, s, protocol+"."+family), nil)
			if result.State != "pass" {
				t.Fatalf("native %s: %+v", family, result)
			}
			if err := s.Cleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if created != deleted || len(responses) != 0 || len(conversations) != 0 || len(chats) != 0 {
				t.Fatalf("own resources leaked: created=%d deleted=%d", created, deleted)
			}
			if family == "stored" && (s.usage.InputTokens != 10 || s.usage.OutputTokens != 4 || inferences != 1) {
				t.Fatalf("stored reads recounted inference: %+v", s.usage)
			}
			if family == "background" && (s.usage.InputTokens != 10 || s.usage.OutputTokens != 4 || inferences != 2) {
				t.Fatalf("background terminal/resume recounted usage: %+v", s.usage)
			}
			stored, _ := json.Marshal(result)
			for _, secret := range []string{replayCode, nativeMarker, "another-users-private-body"} {
				if strings.Contains(string(stored), secret) {
					t.Fatal("resource or foreign body persisted")
				}
			}
		})
	}
}

func TestResumedResponseValidatesCursorIdentityAndTerminal(t *testing.T) {
	response, _ := decodeNative([]byte(nativeJSONFixtures["openai-responses"]))
	valid := eventJSON("response.completed", nativeObject{"type": "response.completed", "sequence_number": 3, "response": response})
	if _, ok := resumedResponse([]byte(valid), "resp_fixture", 2, 1<<20); !ok {
		t.Fatal("valid suffix rejected")
	}
	for _, input := range []struct {
		body, id string
		after    int64
	}{{valid, "other_response", 2}, {valid, "resp_fixture", 3}, {valid + valid, "resp_fixture", 2}, {strings.TrimSuffix(valid, "\n\n"), "resp_fixture", 2}} {
		if _, ok := resumedResponse([]byte(input.body), input.id, input.after, 1<<20); ok {
			t.Fatal("invalid cursor/suffix accepted")
		}
	}
}

func TestCleanupDoesNotTrustAnAcknowledgementForAnotherResource(t *testing.T) {
	deletes := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "DELETE" {
			_, _ = w.Write([]byte(`{"id":"resp_owned"}`))
			return
		}
		deletes++
		id := "resp_other"
		if deletes > 1 {
			id = "resp_owned"
		}
		_ = json.NewEncoder(w).Encode(nativeObject{"id": id, "deleted": true})
	}))
	defer up.Close()
	_, s, _ := nativeTestSession(t, up.URL)
	s.allowed = map[string]bool{"responses.create": true, "responses.delete": true}
	created, err := s.Do(context.Background(), Request{EndpointID: "responses.create", Path: "/v1/responses", Category: "generate"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.TrackResource(context.Background(), created.AttemptID, "response", "resp_owned", "responses.delete", "/v1/responses/resp_owned"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteTracked(context.Background(), "response", "resp_owned"); err != nil {
		t.Fatal(err)
	}
	artifacts, _ := s.store.Artifacts(context.Background(), s.jobID)
	if len(artifacts) != 1 || artifacts[0].Removed {
		t.Fatal("wrong-ID acknowledgement suppressed cleanup")
	}
	if err = s.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	artifacts, _ = s.store.Artifacts(context.Background(), s.jobID)
	if deletes != 2 || !artifacts[0].Removed {
		t.Fatal("owned resource did not remain recoverable")
	}
}
