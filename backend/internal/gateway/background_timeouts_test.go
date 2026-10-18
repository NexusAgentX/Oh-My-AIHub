package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestBackgroundTimeoutsRetainCreatingModelRouteAndExplicitZero(t *testing.T) {
	override := int32(9000)
	metadata := backgroundMetadata{Settings: settings.Settings{DefaultTTFTTimeoutMS: 8000, DefaultIdleTimeoutMS: 8000, DefaultRequestTimeoutMS: 8000}, Candidate: Candidate{Advanced: channel.Advanced{RequestTimeoutMS: &override}}}
	if got := frozenBackgroundTimeouts(metadata); got != (attemptTimeouts{8 * time.Second, 8 * time.Second, 9 * time.Second}) {
		t.Fatal("older metadata lost its own defaults", got)
	}
	metadata.Attempts = []Attempt{{Timeouts: &AttemptTimeouts{FirstByteMS: 7000, IdleMS: 7000, TotalMS: 7000}}, {Timeouts: &AttemptTimeouts{FirstByteMS: 1200, IdleMS: 2300, TotalMS: 0}}}
	if got := frozenBackgroundTimeouts(metadata); got != (attemptTimeouts{1200 * time.Millisecond, 2300 * time.Millisecond, 0}) {
		t.Fatal("creating attempt's resolved model route or explicit zero was replaced", got)
	}
}

type backgroundDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline  time.Time
	protected bool
}

func (w *backgroundDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *backgroundDeadlineRecorder) Write(p []byte) (int, error) {
	w.protected = !w.deadline.IsZero()
	return w.ResponseRecorder.Write(p)
}

// These controlled stalls verify gateway timeout stages, not supplier TTL or
// performance behavior. No provider calls, price observations or A/B are run.
func TestBackgroundResourceTimeoutStagesAndAdmissionRelease(t *testing.T) {
	const output = `{"id":"resp_followup","object":"response","status":"completed","output":[]}`
	for _, tc := range []struct {
		name     string
		stream   bool
		timeouts AttemptTimeouts
		status   int
	}{
		{"nonstream body stalled", false, AttemptTimeouts{10, 1000, 60}, 502},
		{"nonstream ignores first byte clock", false, AttemptTimeouts{5, 5, 1000}, 200},
		{"stream headers are not a first byte", true, AttemptTimeouts{60, 1000, 10}, 502},
		{"stream idle after actual byte", true, AttemptTimeouts{1000, 60, 10}, 200},
		{"stream flow ignores request clock", true, AttemptTimeouts{1000, 1000, 10}, 200},
		{"stream error body uses request clock", true, AttemptTimeouts{1000, 1000, 60}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, store, job := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				switch tc.name {
				case "nonstream ignores first byte clock":
					select {
					case <-time.After(40 * time.Millisecond):
						_, _ = io.WriteString(w, output)
					case <-r.Context().Done():
					}
					return
				case "stream flow ignores request clock":
					for range 3 {
						_, _ = io.WriteString(w, ": flowing\n\n")
						w.(http.Flusher).Flush()
						select {
						case <-time.After(20 * time.Millisecond):
						case <-r.Context().Done():
							return
						}
					}
					_, _ = fmt.Fprint(w, "data: "+`{"type":"response.completed","response":`+output+"}\n\n")
					return
				case "nonstream body stalled":
					_, _ = io.WriteString(w, `{"id":"resp_`)
				case "stream idle after actual byte":
					_, _ = io.WriteString(w, ": started\n\n")
				case "stream error body uses request clock":
					w.WriteHeader(400)
				}
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			})
			var metadata backgroundMetadata
			if err := json.Unmarshal(job.Metadata, &metadata); err != nil {
				t.Fatal(err)
			}
			metadata.Candidate.CredentialRevision = 1
			metadata.Candidate.CredentialScope = "original"
			metadata.Candidate.Evidence = map[string]bool{"upstream_protocol/openai-responses.state/owned_response_retrieve_input_items_delete": true}
			metadata.Attempts[len(metadata.Attempts)-1].Timeouts = &tc.timeouts
			job.Metadata, _ = json.Marshal(metadata)
			store.jobs[job.CallID] = job
			resources := &backgroundResources{rows: []resourcestate.Record{{Resource: gatewaycontract.Resource{Kind: "response", ID: job.ResponseID, AccountID: job.AccountID, KeyID: job.KeyID, CreationID: job.CallID, Binding: candidateBinding(metadata.Candidate), CredentialScope: metadata.Candidate.CredentialScope}, ModelID: metadata.ModelID}}}
			e.Resources = resources
			e.CallTimeLimit = 2 * time.Second
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			query := ""
			if tc.stream {
				query = "?stream=true"
			}
			request := followupRequest(ctx, "GET", query)
			w := &backgroundDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			done := make(chan struct{})
			go func() {
				e.ServeAuxiliary(w, request, gatewaycontract.Endpoint{ID: "responses.get", Protocol: "openai-responses"})
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(700 * time.Millisecond):
				cancel()
				<-done
				t.Fatal("configured timeout stage did not release the follow-up")
			}
			if w.Code != tc.status || len(e.followupSlots) != 0 || len(store.finished) != 0 {
				t.Fatalf("status=%d want=%d slots=%d settlements=%d body=%s", w.Code, tc.status, len(e.followupSlots), len(store.finished), w.Body.String())
			}
			if tc.name == "nonstream ignores first byte clock" && (w.Body.String() != output || !w.protected) {
				t.Fatal("nonstreaming native body or client write deadline lost")
			}
			if tc.name == "stream idle after actual byte" && w.Body.String() != ": started\n\n" {
				t.Fatal("stream bytes changed before idle cancellation")
			}
			if tc.name == "stream flow ignores request clock" && !strings.Contains(w.Body.String(), "response.completed") {
				t.Fatal("request clock interrupted an active stream")
			}
		})
	}
}
