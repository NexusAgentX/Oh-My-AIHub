package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type recoveryResources struct {
	resourcestate.Repository
	err  error
	rows []resourcestate.Record
}

func (s *recoveryResources) Observe(_ context.Context, rows []resourcestate.Record) error {
	if s.err != nil {
		return s.err
	}
	s.rows = append(s.rows, rows...)
	return nil
}

func TestBackgroundResourceRecoveryUsesFrozenOwnerAndQuarantineStopsReads(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "temporary-write", true: "quarantine"}[collision], func(t *testing.T) {
			reached := 0
			e, store, job := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
				reached++
				if r.Header.Get("Authorization") != "Bearer synthetic-upstream-key" {
					t.Error("frozen credential lost")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"object":"response","id":"resp_followup","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
			})
			var metadata backgroundMetadata
			if err := json.Unmarshal(job.Metadata, &metadata); err != nil {
				t.Fatal(err)
			}
			metadata.Candidate.GroupID = "group"
			metadata.Candidate.CredentialRevision = 1
			metadata.Candidate.CredentialScope = "actual-scope"
			job.Metadata, _ = json.Marshal(metadata)
			resources := &recoveryResources{err: errors.New("metadata temporarily unavailable")}
			if collision {
				resources.err = resourcestate.ErrQuarantined
			}
			e.Resources = resources
			// Revocation blocks new consumer reads; it cannot orphan an already
			// registered private job or change its original producer ownership.
			store.key.Enabled = false
			store.key.KeyID = "rotated-platform-key"
			e.pollBackgroundJob(t.Context(), job)
			if reached != 0 {
				t.Fatal("unregistered or ambiguous ID was polled")
			}
			if collision {
				if len(store.finished) != 1 || store.finished[job.CallID].Cost != 0 {
					t.Fatal("collision did not end safely", store.finished)
				}
				return
			}
			if len(store.finished) != 0 {
				t.Fatal("temporary metadata outage abandoned pending job")
			}
			resources.err = nil
			e.pollBackgroundJob(t.Context(), job)
			if reached != 1 || len(store.finished) != 1 || len(resources.rows) == 0 {
				t.Fatal("pending job was not recovered", reached, store.finished, resources.rows)
			}
			for _, row := range resources.rows {
				if row.AccountID != "consumer" || row.KeyID != "key" || row.CreationID != job.CallID {
					t.Fatal("ownership changed during recovery", row)
				}
			}
		})
	}
}

func TestAuxiliaryWriteUnknownIsNotReplayedAndReadsShareAttemptBudget(t *testing.T) {
	for _, tc := range []struct {
		name, method, replay string
		status, want         int
	}{
		{"unknown-write", "POST", "write", 503, 1},
		{"read-fallback", "GET", "read", 503, 2},
		{"rate-limit", "GET", "read", 429, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached := 0
			e, _, job := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
				reached++
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":"synthetic"}`)
			})
			var metadata backgroundMetadata
			if err := json.Unmarshal(job.Metadata, &metadata); err != nil {
				t.Fatal(err)
			}
			c := metadata.Candidate
			c.Entrances = []Entrance{{BaseURL: c.BaseURL}, {BaseURL: c.BaseURL}, {BaseURL: c.BaseURL}}
			budget := 2
			r := httptest.NewRequest(tc.method, "/v1/responses/resp_followup", nil)
			r.SetPathValue("responseID", "resp_followup")
			ep := gatewaycontract.Endpoint{ID: "responses.get", Path: "/v1/responses/{response_id}", Replay: tc.replay}
			_, _, _ = e.auxiliaryExchange(t.Context(), r, ep, c, authBearer, e.settingsSnap.Load().outbound, settings.Settings{}, routing.Pref{}, nil, &budget)
			if reached != tc.want || budget != 2-tc.want {
				t.Fatal("replayed write, rate limit, or multiplied attempt budget", reached, budget)
			}
		})
	}
}
