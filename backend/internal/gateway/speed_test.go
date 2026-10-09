package gateway

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type speedStore struct {
	Store
	finish CallFinish
}

func (s *speedStore) FinishCall(_ context.Context, finish CallFinish) (FinishResult, error) {
	s.finish = finish
	return FinishResult{Finished: true}, nil
}

func TestResponseOutputSpeed(t *testing.T) {
	for _, tt := range []struct {
		name                         string
		stream                       bool
		duration, ttft, attemptStart int
		want                         float64
	}{
		{"nonstream sample", false, 2380, 2377, 0, 6.0 / 2.38},
		{"nonstream longer output", false, 2380, 100, 0, 6.0 / 2.38},
		{"nonstream includes earlier attempts", false, 2380, 1377, 1000, 6.0 / 2.38},
		{"nonstream zero duration", false, 0, 0, 0, 0},
		{"stream unchanged", true, 2380, 2377, 0, 2000},
		{"stream zero output duration", true, 2380, 2380, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Unix(1000, 0)
			store := &speedStore{}
			engine := NewEngine(Dependencies{Store: store, Now: func() time.Time { return start.Add(time.Duration(tt.duration) * time.Millisecond) }})
			body := `{"id":"resp_test","usage":{"input_tokens":1,"output_tokens":6}}`
			contentType := "application/json"
			if tt.stream {
				body = "data: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
				contentType = "text/event-stream"
			}
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(""))}
			call := &callState{started: start, stream: tt.stream, format: channel.FormatOpenAIResponses, attempts: []Attempt{{}}}
			recorder := httptest.NewRecorder()
			engine.streamBack(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), call, catalog.Model{}, Candidate{}, attemptResult{
				response: response, first: []byte(body), firstErr: io.EOF, cancel: func(error) {}, started: start.Add(time.Duration(tt.attemptStart) * time.Millisecond), ttft: time.Duration(tt.ttft) * time.Millisecond,
			}, settings.Settings{})
			if recorder.Body.String() != body || store.finish.Usage.OutputTokens != 6 {
				t.Fatalf("response or usage changed: %s, %+v", recorder.Body.String(), store.finish)
			}
			speed := store.finish.TokensPerSecond
			if tt.want == 0 {
				if speed != nil {
					t.Fatalf("want unavailable, got %v", *speed)
				}
			} else if speed == nil || math.Abs(*speed-tt.want) > 1e-9 {
				t.Fatalf("want %v, got %v", tt.want, speed)
			}
		})
	}
}
