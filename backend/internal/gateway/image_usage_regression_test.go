package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

func TestImageSubUsageCannotStandInForParentUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, usage := range []string{"", `,"usage":{"input_tokens":0,"output_tokens":0}`, `,"usage":{"input_tokens":100,"output_tokens":20}`} {
			raw := `{"id":"resp_image","status":"completed","output":[{"id":"ig_one","type":"image_generation_call","model":"image-model","usage":{"input_tokens":1000000,"output_tokens":1000000}}]` + usage + `}`
			contentType := "application/json"
			if stream {
				raw = "data: " + `{"type":"response.completed","response":` + raw + "}\n\n"
				contentType = "text/event-stream"
			}
			got := observe(channel.FormatOpenAIResponses, stream, contentType, raw, 1)
			if got.Found != (usage != "") || got.Usage.Detail == nil || len(got.Usage.Detail.Iterations) != 1 || got.Usage.Detail.Iterations[0].Usage.InputTokens != 1_000_000 {
				t.Fatalf("stream=%v usage=%s: %+v", stream, usage, got)
			}
			if usage == "" && (got.UsageStatus != "missing" || got.Usage.InputTokens != 0 || got.Usage.OutputTokens != 0) {
				t.Fatal("image facts manufactured reported parent usage", got)
			}
		}
	}
}

func TestBackgroundImageOnlyUsagePersistsAsUnbilled(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			body := `{"id":"resp_followup","status":"completed","output":[{"id":"ig_one","type":"image_generation_call","model":"image-model","usage":{"input_tokens":1000000,"output_tokens":1000000,"input_tokens_details":{"image_tokens":1000000}}}]}`
			e, store, job := followupFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: "+`{"type":"response.completed","response":`+body+"}\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, body)
				}
			})
			e.reserveBackground(job.CallID)
			e.pollBackgroundJob(context.Background(), job)
			store.mu.Lock()
			defer store.mu.Unlock()
			finish, ok := store.finished[job.CallID]
			if !ok || finish.Outcome != OutcomeUnbilled || finish.Bill != nil || finish.Cost != 0 || finish.Fee != 0 || !strings.Contains(string(finish.PriceSnapshot), "主模型用量缺失，未计费") {
				t.Fatalf("finish=%+v", finish)
			}
			var snapshot struct {
				Detail ledger.UsageDetail `json:"detail"`
			}
			if err := json.Unmarshal(finish.PriceSnapshot, &snapshot); err != nil || len(snapshot.Detail.Iterations) != 1 || snapshot.Detail.Iterations[0].Usage.Detail.Tokens["input_image"] != 1_000_000 {
				t.Fatalf("snapshot=%s err=%v", finish.PriceSnapshot, err)
			}
		})
	}
}

// Returning only the child facts models a long response that has moved past
// these tool frames. None of their ignored JSON should stay live with the facts.
func imageFactsWithLargeIgnoredUsage() []ledger.UsageIteration {
	o := NewObserver(channel.FormatOpenAIResponses, false, "application/json")
	for i := range 8 {
		raw := fmt.Sprintf(`{"id":"ig_%d","type":"image_generation_call","model":"image-model","usage":{"input_tokens":12,"output_tokens":34,"input_tokens_details":{"image_tokens":10},"ignored":"%s"}}`, i, strings.Repeat("x", 1<<20))
		o.observeResponseItem([]byte(raw))
	}
	return o.detail.Iterations
}

func TestImageUsageFactsDoNotRetainParserBuffers(t *testing.T) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	facts := imageFactsWithLargeIgnoredUsage()
	runtime.GC()
	runtime.ReadMemStats(&after)
	if len(facts) != 8 || facts[0].Usage.Detail.Tokens["input_image"] != 10 {
		t.Fatal("detaching parser state changed usage facts")
	}
	// Eight 1 MiB usage buffers used to remain reachable. Allow generous runtime
	// noise while requiring the retained metadata to remain independent of them.
	if after.HeapAlloc > before.HeapAlloc+2<<20 {
		t.Fatalf("child facts retained %d bytes of parser state", after.HeapAlloc-before.HeapAlloc)
	}
	runtime.KeepAlive(facts)
}
