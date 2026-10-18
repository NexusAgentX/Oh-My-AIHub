package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
)

func codexSelectedHandler(handler http.Handler, selected codexTestSnapshot) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), codexTestSnapshotKey{}, selected)))
	})
}

// These are platform mechanism regressions with synthetic approved objects.
// No result from this fixture is a supplier capability report.
func TestCodexOpaqueContinuationCrossesChannelsAndCredentials(t *testing.T) {
	for _, relation := range []string{"same_group", "different_group"} {
		t.Run(relation, func(t *testing.T) {
			var received []string
			var credentials []string
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				received = append(received, string(raw))
				credential := r.Header.Get("Authorization")
				credentials = append(credentials, credential)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/messages" {
					_, _ = io.WriteString(w, `{"id":"msg_synthetic","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
				} else {
					_, _ = io.WriteString(w, `{"id":"resp_synthetic","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
				}
			}))
			defer up.Close()
			handler, store, records := newCodexContextFixture(t, up)
			for i := range store.rows {
				store.rows[i].Formats = append(store.rows[i].Formats, channel.FormatAnthropic)
				store.rows[i].Candidate.Evidence["upstream_protocol/native-messages.reasoning/complete_opaque_signature_or_encrypted_replay"] = true
				store.rows[i].Candidate.VerifiedCapacity = map[string]int64{"native-messages.length_search@upstream_protocol/max_output": 32}
				store.rows[i].Candidate.Advanced.HeaderRules.Set = append(store.rows[i].Candidate.Advanced.HeaderRules.Set, channel.HeaderSet{Name: "Anthropic-Version", Value: "2023-06-01"})
			}
			if relation == "same_group" {
				store.rows[1].Candidate.GroupID = store.rows[0].Candidate.GroupID
				book := *store.rows[1].Candidate.Quotes
				book.GroupID = store.rows[0].Candidate.GroupID
				store.rows[1].Candidate.Quotes, _ = quotepricing.Freeze(book)
			}
			cases := []struct{ name, path, body string }{
				{"encrypted_reasoning_cross_channel", "/backend-api/codex/responses", `{"model":"platform/model:2026","store":false,"input":[{"type":"reasoning","id":"unregistered","summary":[],"encrypted_content":"cipher+/=="},{"role":"user","content":"continue"}]}`},
				{"compaction_cross_channel", "/backend-api/codex/responses", `{"model":"platform/model:2026","store":false,"input":[{"role":"developer","content":"retained before"},{"type":"compaction","id":"unregistered","encrypted_content":"whole-window+/=="},{"role":"user","content":"retained after"}]}`},
				{"signature_cross_channel", "/v1/messages", `{"model":"platform/model:2026","max_tokens":16,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"synthetic","signature":"signature+/=="}]},{"role":"user","content":"continue"}]}`},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					before := len(received)
					for _, id := range []string{"channel-a", "channel-b"} {
						w := codexRequest(codexSelectedHandler(handler, codexTestSnapshot{ChannelID: id}), "POST", test.path, test.body)
						if w.Code != 200 {
							t.Fatal("self-contained continuation was bound to its source", w.Code, w.Body.String())
						}
					}
					want := strings.Replace(test.body, `"platform/model:2026"`, `"native/model"`, 1)
					if len(received) != before+2 || received[before] != want || received[before+1] != want || credentials[before] == credentials[before+1] {
						t.Fatal("opaque continuation changed or did not cross credential boundaries")
					}
				})
			}
			if records.writes != 0 || len(records.rows) != 0 || len(store.finished) != 6 {
				t.Fatal("opaque content registered as persistent resources or settlement missing")
			}
		})
	}
}

func TestCodexTrueReferencesKeepOwnerAndCredentialScope(t *testing.T) {
	seen := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seen++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_new","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer up.Close()
	handler, store, records := newCodexContextFixture(t, up)
	c := store.rows[0].Candidate
	if err := records.Observe(context.Background(), []resourcestate.Record{{Resource: gatewaycontract.Resource{Kind: "response", ID: "resp_saved", AccountID: store.key.OwnerID, KeyID: store.key.KeyID, Binding: gatewaycontract.Binding{GroupID: c.GroupID, ChannelID: c.ChannelID, CredentialVersion: "1"}, CredentialScope: c.CredentialScope, CreationID: "synthetic-creation"}, ModelID: "platform/model:2026"}}); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"platform/model:2026","store":false,"previous_response_id":"resp_saved","input":"continue"}`
	for _, change := range []string{"different_channel", "different_key", "different_account", "revoked_key", "rotated_credential", "original"} {
		t.Run(change, func(t *testing.T) {
			key := store.key
			selected := codexTestSnapshot{ChannelID: "channel-a", Key: &key}
			switch change {
			case "different_channel":
				selected.ChannelID = "channel-b"
			case "different_key":
				key.KeyID = "foreign-key"
			case "different_account":
				key.OwnerID = "foreign-account"
			case "revoked_key":
				key.Enabled = false
			case "rotated_credential":
				rotated := c.Credential
				selected.Rotated = &rotated
			}
			before := seen
			w := codexRequest(codexSelectedHandler(handler, selected), "POST", "/backend-api/codex/responses", body)
			if change == "original" {
				if w.Code != 200 || seen != before+1 {
					t.Fatal("owned original resource unavailable", w.Code, w.Body.String())
				}
			} else if w.Code < 400 || seen != before {
				t.Fatal("remote reference escaped original ownership", w.Code, seen)
			}
		})
	}
}

func TestCodexCollidingContextsRemainIsolatedAfterCredentialRotation(t *testing.T) {
	values := map[string]string{}
	var credentials []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Context struct {
				Session string `json:"session_id"`
			} `json:"context"`
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		credentials = append(credentials, r.Header.Get("Authorization"))
		if strings.HasSuffix(r.URL.Path, "/write_file") {
			values[body.Context.Session] = body.Text
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": values[body.Context.Session]})
	}))
	defer up.Close()
	handler, store, _ := newCodexContextFixture(t, up)
	body := `{"context":{"session_id":"same-session","current_agent_name":"/root"},"path":"same-path","window_id":"same-window","item_id":"same-item","text":"synthetic-private-value"}`
	base := codexTestSnapshot{ChannelID: "channel-a"}
	if w := codexRequest(codexSelectedHandler(handler, base), "POST", "/backend-api/codex/alpha/notes/v2/write_file", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	keyring, _ := channel.ParseKeyring("test=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "test")
	sealed, err := keyring.Encrypt("channel-a", 1, "rotated-synthetic-upstream")
	if err != nil {
		t.Fatal(err)
	}
	base.Rotated = &sealed
	for _, actor := range []string{"other_key", "other_account", "forged_mapped_session", "owner"} {
		key := store.key
		selected := base
		selected.Key = &key
		requestBody := body
		switch actor {
		case "other_key":
			key.KeyID = "other-key"
		case "other_account":
			key.OwnerID = "other-account"
		case "forged_mapped_session":
			key.KeyID = "other-key"
			for mapped := range values {
				requestBody = strings.Replace(body, "same-session", mapped, 1)
			}
		}
		w := codexRequest(codexSelectedHandler(handler, selected), "POST", "/backend-api/codex/alpha/notes/v2/read_file", requestBody)
		found := bytes.Contains(w.Body.Bytes(), []byte("synthetic-private-value"))
		if w.Code != 200 || found != (actor == "owner") {
			t.Fatal("colliding context escaped platform namespace", actor, w.Code)
		}
	}
	if len(credentials) != 5 || credentials[0] == credentials[4] {
		t.Fatal("credential rotation did not reach the configured upstream")
	}
}

func TestCodexFixedEgressAndFailureSecretsUseProductionHandlers(t *testing.T) {
	var escaped atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		escaped.Add(1)
		w.WriteHeader(200)
	}))
	defer foreign.Close()
	seen := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.Header.Get("Authorization") != "Bearer shared-synthetic-upstream" || r.Header.Get("ChatGPT-Account-Id") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("platform/client identity leaked to the fixed upstream")
		}
		w.Header().Set("Location", foreign.URL+"/?key=shared-synthetic-upstream")
		w.Header().Set("WWW-Authenticate", `Bearer error_description="shared-synthetic-upstream"`)
		w.Header().Set("Set-Cookie", "upstream-private=shared-synthetic-upstream")
		w.Header().Set("ChatGPT-Account-Id", "upstream-account")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(302)
		_, _ = io.WriteString(w, `{"error":{"type":"upstream_error","message":"shared-synthetic-upstream"}}`)
	}))
	defer up.Close()
	handler, store, _ := newCodexContextFixture(t, up)
	for _, path := range []string{"/backend-api/codex/responses", "/backend-api/codex/alpha/notes/v2/read_file"} {
		body := `{"model":"platform/model:2026","store":false,"input":"synthetic","context":{"session_id":"same-session","current_agent_name":"/root"},"path":"note","unknown_url":"` + foreign.URL + `"}`
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer sk-aih-synthetic")
		r.Header.Set("ChatGPT-Account-Id", "forged-client-account")
		r.Header.Set("X-Forwarded-Host", foreign.URL)
		w := httptest.NewRecorder()
		codexSelectedHandler(handler, codexTestSnapshot{ChannelID: "channel-a"}).ServeHTTP(w, r)
		if w.Code < 300 || w.Header().Get("Set-Cookie") != "" || w.Header().Get("ChatGPT-Account-Id") != "" || strings.Contains(w.Body.String(), "shared-synthetic-upstream") {
			t.Fatal("upstream failure leaked credential/identity", path, w.Code)
		}
		for _, values := range w.Header() {
			for _, v := range values {
				if strings.Contains(v, "shared-synthetic-upstream") {
					t.Fatal("credential leaked through response header")
				}
			}
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	persisted, _ := json.Marshal(store.finished)
	if seen != 2 || escaped.Load() != 0 || strings.Contains(string(persisted), "shared-synthetic-upstream") || strings.Contains(string(persisted), "sk-aih-synthetic") {
		t.Fatal("redirect escaped fixed egress or call metadata retained a secret", seen, escaped.Load())
	}
}
