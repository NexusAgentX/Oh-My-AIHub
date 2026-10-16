package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

func TestImageObservationsKeepMetadataWithoutInventingUsage(t *testing.T) {
	o := NewObserver(channel.FormatOpenAIResponses, false, "application/json")
	o.imageModel = "configured-image"
	o.absorb([]byte(`{"output":[{"id":"ig_meta","type":"image_generation_call","status":"completed","size":"1536x864","quality":"high","output_format":"webp","background":"transparent","action":"edit","result":"PRIVATE_IMAGE","revised_prompt":"PRIVATE_PROMPT","error":{"message":"PRIVATE_ERROR"}}]}`))
	got := o.Finish()
	if got.Found || len(got.Usage.Detail.ImageGenerations) != 1 {
		t.Fatalf("%+v", got)
	}
	image := got.Usage.Detail.ImageGenerations[0]
	if image.Model != "configured-image" || image.ModelSource != "request" || image.Usage != nil || image.Status != "completed" || image.Size != "1536x864" || image.Quality != "high" || image.OutputFormat != "webp" || image.Background != "transparent" || image.Action != "edit" {
		t.Fatalf("%+v", image)
	}
	raw, _ := json.Marshal(got.Usage.Detail)
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatalf("retained content: %s", raw)
	}
}

func TestImageStreamMetadataMergesAndTerminalStatusDoesNotRegress(t *testing.T) {
	events := []string{
		`{"type":"response.image_generation_call.in_progress","item_id":"ig_one"}`,
		`{"type":"response.output_item.added","item":{"id":"ig_one","type":"image_generation_call","status":"in_progress"}}`,
		`{"type":"response.image_generation_call.generating","item_id":"ig_one"}`,
		`{"type":"response.image_generation_call.completed","item_id":"ig_one"}`,
		`{"type":"response.output_item.done","item":{"id":"ig_one","type":"image_generation_call","status":"completed","size":"1024x1024","quality":"max","model":"returned-image","usage":{"input_tokens":12,"output_tokens":34,"input_tokens_details":{"image_tokens":10}}}}`,
		`{"type":"response.completed","response":{"id":"resp_one","status":"completed","usage":{"input_tokens":100,"output_tokens":20},"output":[{"id":"ig_one","type":"image_generation_call","status":"generating","usage":{"input_tokens":12,"output_tokens":34}}]}}`,
	}
	var stream strings.Builder
	for _, event := range events {
		fmt.Fprintf(&stream, "data: %s\n\n", event)
	}
	got := observe(channel.FormatOpenAIResponses, true, "text/event-stream", stream.String(), 1)
	image := got.Usage.Detail.ImageGenerations[0]
	if len(got.Usage.Detail.ImageGenerations) != 1 || got.Usage.Detail.Units["image_generation"] != ledger.FixedPointScale || image.Status != "completed" || image.ModelSource != "response" || image.Model != "returned-image" || image.Size != "1024x1024" || image.Quality != "max" || image.Usage.InputTokens != 12 || image.Usage.OutputTokens != 34 || image.Usage.Detail.Tokens["input_image"] != 10 {
		t.Fatalf("%+v", got)
	}
	if !got.Found || got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 20 || len(got.Usage.Detail.Iterations) != 0 {
		t.Fatal("image observation changed billable usage")
	}
	// Text deltas with item_id must continue to bypass JSON decoding.
	if keys := scanFrameKeys([]byte(`{"type":"response.output_text.delta","item_id":"msg_one","delta":"text"}`)); keys != 0 {
		t.Fatalf("text delta entered observation path: %v", keys)
	}
}

func TestImageObservationBoundsAndInvalidMetadata(t *testing.T) {
	o := NewObserver(channel.FormatOpenAIResponses, false, "application/json")
	o.absorb([]byte(`{"output":[{"id":"ig_invalid","type":"image_generation_call","model":"PRIVATE MODEL TEXT","status":"PRIVATE STATUS","size":"PRIVATE SIZE","quality":"PRIVATE QUALITY","background":"PRIVATE BACKGROUND","action":"PRIVATE ACTION","output_format":"PRIVATE FORMAT"},{"id":"PRIVATE ID","type":"image_generation_call"}]}`))
	for i := 0; i < 140; i++ {
		o.absorb([]byte(fmt.Sprintf(`{"output":[{"id":"ig_%d","type":"image_generation_call"}]}`, i)))
	}
	// Even at capacity, an existing observation can receive its final state.
	o.absorb([]byte(`{"output":[{"id":"ig_invalid","type":"image_generation_call","status":"failed","size":"999999x999999"}]}`))
	got := o.Finish()
	raw, _ := json.Marshal(got.Usage.Detail)
	if strings.Contains(string(raw), "PRIVATE") || len(got.Usage.Detail.ImageGenerations) != maxBillingItems || got.Usage.Detail.ImageGenerations[0].Status != "failed" || got.Usage.Detail.Units["image_generation"] != 128*ledger.FixedPointScale || !strings.Contains(string(raw), "上限") {
		t.Fatalf("%s", raw)
	}
}

func TestBackgroundImageObservationsPersistWithoutUsageOrCharges(t *testing.T) {
	for _, withUsage := range []bool{false, true} {
		t.Run(fmt.Sprint(withUsage), func(t *testing.T) {
			usage := ""
			if withUsage {
				usage = `,"usage":{"input_tokens":100,"output_tokens":20}`
			}
			response := `{"id":"resp_followup","status":"completed","output":[{"id":"ig_final","type":"image_generation_call","status":"failed","size":"1024x1536","quality":"medium","model":"image-model","usage":{"input_tokens":1000000,"output_tokens":1000000},"error":{"message":"PRIVATE_ERROR"}}]` + usage + `}`
			e, store, job := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, response)
			})
			e.reserveBackground(job.CallID)
			e.pollBackgroundJob(context.Background(), job)
			store.mu.Lock()
			defer store.mu.Unlock()
			finish, ok := store.finished[job.CallID]
			if !ok || !strings.Contains(string(finish.PriceSnapshot), `"image_generations"`) || !strings.Contains(string(finish.PriceSnapshot), `"status":"failed"`) || strings.Contains(string(finish.PriceSnapshot), "PRIVATE_ERROR") {
				t.Fatalf("%+v", finish)
			}
			if !withUsage && (finish.Bill != nil || finish.Cost != 0 || finish.Outcome != OutcomeUnbilled) {
				t.Fatal("observations invented billable usage")
			}
			if withUsage && finish.Cost.String() != "0.0002" {
				t.Fatalf("cost=%s", finish.Cost.String())
			}
		})
	}
}

func BenchmarkImageObservationMetadata(b *testing.B) {
	raw := []byte(`{"id":"resp_one","usage":{"input_tokens":100,"output_tokens":20},"output":[{"id":"ig_one","type":"image_generation_call","status":"completed","size":"1024x1024","quality":"high","output_format":"png"}]}`)
	b.ReportAllocs()
	for b.Loop() {
		o := NewObserver(channel.FormatOpenAIResponses, false, "application/json")
		o.Write(raw, time.Time{})
		o.Finish()
	}
}
