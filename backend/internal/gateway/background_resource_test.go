package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
)

type backgroundResources struct {
	resourcestate.Repository
	rows     []resourcestate.Record
	observed []resourcestate.Record
}

func (s *backgroundResources) Lookup(_ context.Context, ref gatewaycontract.Reference) ([]resourcestate.Record, error) {
	rows := []resourcestate.Record{}
	for _, r := range s.rows {
		if r.Kind == ref.Kind && r.ID == ref.ID {
			rows = append(rows, r)
		}
	}
	return rows, nil
}
func (s *backgroundResources) Observe(_ context.Context, rows []resourcestate.Record) error {
	s.observed = append(s.observed, rows...)
	return nil
}

func TestBackgroundResourceEntryUsesFrozenSnapshotAndCurrentKeyWithoutAnotherAdmission(t *testing.T) {
	for _, mode := range []string{"json", "sse", "cancel", "cancel-zstd", "foreign-key", "revoked-model", "quarantined", "wrong-scope"} {
		t.Run(mode, func(t *testing.T) {
			reached := 0
			output := `{"id":"resp_followup","object":"response","status":"completed","output":[{"id":"msg_owned","type":"message","role":"assistant","content":[{"type":"output_text","text":"private synthetic answer"}]}]}`
			e, store, job := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
				reached++
				if r.Header.Get("Authorization") != "Bearer synthetic-upstream-key" {
					t.Error("frozen credential changed")
				}
				if strings.HasPrefix(mode, "cancel") {
					raw, _ := io.ReadAll(r.Body)
					body, err := nativeencoding.Decode(r.Context(), r.Header.Get("Content-Encoding"), raw)
					if err != nil || string(body) != `{"reason":"synthetic stop"}` {
						t.Error("cancel body changed", err)
					}
				}
				if mode == "sse" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: response.completed\ndata: "+`{"type":"response.completed","response":`+output+"}\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, output)
				}
			})
			var metadata backgroundMetadata
			if err := json.Unmarshal(job.Metadata, &metadata); err != nil {
				t.Fatal(err)
			}
			metadata.Candidate.GroupID = "group"
			metadata.Candidate.GroupModelID = "group-main"
			metadata.Candidate.CredentialRevision = 1
			metadata.Candidate.CredentialScope = "original-credential"
			metadata.Candidate.Quotes = groupBook(t, "CNY", 1)
			metadata.Candidate.Evidence = map[string]bool{
				"upstream_protocol/openai-responses.state/owned_response_retrieve_input_items_delete": true,
				"upstream_protocol/openai-responses.background/owned_cancel":                          true,
			}
			owned := resourcestate.Record{Resource: gatewaycontract.Resource{Kind: "response", ID: job.ResponseID, AccountID: job.AccountID, KeyID: job.KeyID, CreationID: job.CallID, Binding: candidateBinding(metadata.Candidate), CredentialScope: metadata.Candidate.CredentialScope}, ModelID: metadata.ModelID}
			resources := &backgroundResources{rows: []resourcestate.Record{owned}}
			e.Resources = resources
			if mode == "foreign-key" {
				store.key.KeyID = "another-key"
			}
			if mode == "revoked-model" {
				store.key.AllowedModels = []string{"another-model"}
			}
			if mode == "quarantined" {
				resources.rows[0].Quarantined = true
			}
			if mode == "wrong-scope" {
				metadata.Candidate.CredentialScope = "replacement-credential"
			}
			job.Metadata, _ = json.Marshal(metadata)
			store.jobs[job.CallID] = job
			// No current candidate source is installed. The original task already
			// owns this admission; reads must neither require a replacement channel
			// nor acquire another account slot.
			release, ok := e.inflight.Acquire(job.AccountID, 1)
			if !ok {
				t.Fatal("admission fixture")
			}
			defer release()
			method, endpoint, query := "GET", "responses.get", ""
			if strings.HasPrefix(mode, "cancel") {
				method, endpoint = "POST", "responses.cancel"
			}
			if mode == "sse" {
				query = "?stream=true"
			}
			request := followupRequest(t.Context(), method, query)
			if strings.HasPrefix(mode, "cancel") {
				encoding := ""
				if mode == "cancel-zstd" {
					encoding = "zstd"
					request.Header.Set("Content-Encoding", encoding)
				}
				raw, err := nativeencoding.Encode(t.Context(), encoding, []byte(`{"reason":"synthetic stop"}`))
				if err != nil {
					t.Fatal(err)
				}
				request.Body = io.NopCloser(strings.NewReader(string(raw)))
				request.ContentLength = int64(len(raw))
			}
			response := httptest.NewRecorder()
			e.ServeAuxiliary(response, request, gatewaycontract.Endpoint{ID: endpoint, Protocol: "openai-responses"})
			want := 200
			switch mode {
			case "foreign-key", "quarantined", "wrong-scope":
				want = 404
			case "revoked-model":
				want = 403
			}
			if response.Code != want {
				t.Fatal("response", response.Code, response.Body.String())
			}
			if want != 200 {
				if reached != 0 || len(resources.observed) != 0 {
					t.Fatal("unauthorized frozen resource operation reached upstream")
				}
				return
			}
			if reached != 1 || response.Header().Get("X-AIHub-Request-Id") != job.CallID || e.inflight.Count(job.AccountID) != 1 || len(store.finished) != 0 {
				t.Fatal("follow-up replaced admission or settlement")
			}
			child := false
			for _, r := range resources.observed {
				if r.Kind == "item" && r.ID == "msg_owned" && r.ParentID == job.ResponseID && r.CreationID == job.CallID && r.CredentialScope == "original-credential" {
					child = true
				}
			}
			if !child {
				t.Fatal("frozen response read lost the trusted child-resource chain")
			}
			if !strings.Contains(response.Body.String(), "private synthetic answer") {
				t.Fatal("native response changed")
			}
		})
	}
}
