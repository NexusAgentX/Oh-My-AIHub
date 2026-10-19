package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexsession"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type notesRouteRepository struct {
	mu   sync.Mutex
	rows map[codexsession.Identity]codexsession.Route
}

func (m *notesRouteRepository) LookupSession(_ context.Context, id codexsession.Identity) (codexsession.Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok {
		return r, nil
	}
	return codexsession.Route{}, gatewaycontract.ErrNotFound
}
func (m *notesRouteRepository) ClaimSession(_ context.Context, id codexsession.Identity, r codexsession.Route) (codexsession.Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.rows[id]; ok {
		return old, nil
	}
	m.rows[id] = r
	return r, nil
}

func TestExplicitNotesDeclarationBindsBeforeFirstToolAndNeverChangesCredential(t *testing.T) {
	for _, first := range []string{"inference", "thread_hint"} {
		t.Run(first, func(t *testing.T) {
			seen := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen++
				if r.Header.Get("Authorization") != "Bearer shared-synthetic-upstream" {
					t.Error("notes escaped original credential")
				}
				if strings.HasSuffix(r.URL.Path, "/responses") {
					codexPlatformStream(w, []any{codexPlatformText("OK")})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"text":""}`))
			}))
			defer up.Close()
			handler, store, _ := newCodexContextFixture(t, up)
			store.key.CodexNotes = true
			invoke := func(path, body, only string, rotate bool) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer sk-aih-synthetic")
				r.Header.Set("Content-Type", "application/json")
				selected := codexTestSnapshot{ChannelID: only}
				if rotate {
					keyring, _ := channel.ParseKeyring("test=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "test")
					c, _ := keyring.Encrypt("channel-a", 1, "rotated-synthetic")
					selected.Rotated = &c
				}
				r = r.WithContext(context.WithValue(r.Context(), codexTestSnapshotKey{}, selected))
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			infer := `{"model":"platform/model:2026","stream":true,"store":false,"client_metadata":{"session_id":"same-session"},"input":[{"role":"user","content":"OK"}]}`
			note := `{"context":{"session_id":"same-session","current_agent_name":"/root"},"path":"synthetic-note"}`
			path, body := "/backend-api/codex/responses", infer
			if first == "thread_hint" {
				path, body = "/backend-api/codex/alpha/notes/v2/thread_hint", note
			}
			if w := invoke(path, body, "channel-a", false); w.Code != 200 {
				t.Fatal("first request", w.Code, w.Body.String())
			}
			// The tool may first appear many turns later. Removing the original
			// channel must fail before sending, rather than reading B's empty notes.
			if w := invoke("/backend-api/codex/alpha/notes/v2/read_file", note, "channel-b", false); w.Code != 503 || seen != 1 {
				t.Fatal("notes moved to another channel", w.Code, seen)
			}
			if w := invoke("/backend-api/codex/responses", infer, "channel-b", false); w.Code != 503 || seen != 1 {
				t.Fatal("inference escaped session", w.Code, seen)
			}
			if w := invoke("/backend-api/codex/alpha/notes/v2/read_file", note, "channel-a", true); w.Code != 503 || seen != 1 {
				t.Fatal("rotated credential reused session", w.Code, seen)
			}
			if w := invoke("/backend-api/codex/alpha/notes/v2/read_file", note, "channel-a", false); w.Code != 200 || seen != 2 {
				t.Fatal("original binding could not resume", w.Code, seen)
			}
		})
	}
}

func TestNotesDeclarationIsAnOwnerControlledKeySetting(t *testing.T) {
	p := newPlatform(t)
	owner, _ := p.member(t, "notes-owner")
	other, _ := p.member(t, "notes-other")
	created := owner.expect(t, 201, "POST", "/api/keys", map[string]any{"name": "Codex"})["key"].(map[string]any)
	if created["codex_notes"] != false {
		t.Fatal("notes enabled without declaration")
	}
	path := "/api/keys/" + created["id"].(string)
	if got := owner.expect(t, 200, "PATCH", path, map[string]any{"codex_notes": true})["key"].(map[string]any); got["codex_notes"] != true {
		t.Fatal("declaration was not saved")
	}
	other.expect(t, 404, "PATCH", path, map[string]any{"codex_notes": false})
	owner.expect(t, 422, "PATCH", path, map[string]any{"codex_notes": nil})
	if got := owner.expect(t, 200, "PATCH", path, map[string]any{"codex_notes": false})["key"].(map[string]any); got["codex_notes"] != false {
		t.Fatal("explicit false was ignored")
	}
}
