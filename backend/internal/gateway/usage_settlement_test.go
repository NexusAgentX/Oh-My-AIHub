package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestNormalizedUsageReachesAtomicSettlementAndSnapshot(t *testing.T) {
	start := time.Unix(1000, 0)
	store := &speedStore{}
	engine := NewEngine(Dependencies{Store: store, Now: func() time.Time { return start.Add(time.Second) }})
	body := "data: " + `{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":30,"cache_creation_tokens":10,"cache_creation_token_details":{"ephemeral_5m_input_tokens":4,"ephemeral_1h_input_tokens":6}}}}` + "\n\ndata: " + `{"usage":{"completion_tokens":20}}` + "\n\n"
	model := catalog.Model{InputPrice: 1_000_000, OutputPrice: 2_000_000, CacheWritePrice: 3_000_000, CacheReadPrice: 4_000_000}
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(""))}
	call := &callState{id: "normalized", started: start, stream: true, format: channel.FormatOpenAIChat, key: KeyAuth{OwnerID: "consumer"}, attempts: []Attempt{{}}}
	recorder := httptest.NewRecorder()
	engine.streamBack(recorder, call, model, Candidate{ChannelID: "channel", OwnerID: "provider", MultiplierNano: ledger.FixedPointScale, RateNano: ledger.UnitRate}, attemptResult{response: response, first: []byte(body), firstErr: io.EOF, cancel: func(error) {}, upstream: context.Background(), watch: watchClient(context.Background(), func(error) {}, drainWindow), started: start}, settings.Settings{})
	finish := store.finish
	if recorder.Body.String() != body || finish.Usage.InputTokens != 60 || finish.Usage.OutputTokens != 20 || finish.Usage.CacheWriteTokens != 10 || finish.Usage.CacheReadTokens != 30 {
		t.Fatalf("response/usage: %+v", finish)
	}
	if finish.Cost != 250 || finish.Bill == nil || finish.Bill.Cost != 250 || finish.Bill.ConsumerID != "consumer" || finish.Bill.ProviderID != "provider" {
		t.Fatalf("settlement: %+v", finish)
	}
	var snap struct {
		Detail   ledger.UsageDetail `json:"detail"`
		Selected struct {
			CacheWrite string `json:"cache_write"`
		} `json:"selected_prices"`
	}
	if err := json.Unmarshal(finish.PriceSnapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Detail.Tokens["cache_write_5m"] != 4 || snap.Detail.Tokens["cache_write_1h"] != 6 || snap.Selected.CacheWrite != "0.003" {
		t.Fatalf("snapshot: %s", finish.PriceSnapshot)
	}
}

// Keep the repeated-frame correctness check in race builds as well. Allocation
// accounting is tested separately because race instrumentation changes escape costs.
func TestGeminiCumulativeUsage(t *testing.T) {
	body := []byte(strings.Repeat(geminiCumulativeUsageFrame, 1000))
	o := NewObserver(channel.FormatGemini, true, "text/event-stream")
	o.Write(body, time.Unix(0, 0))
	got := o.Finish()
	if !got.Found || got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 70 || got.Frames != 1000 {
		t.Fatalf("repeated cumulative frames: %+v", got)
	}
}

const geminiCumulativeUsageFrame = `data: {"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"thoughtsTokenCount":20,"totalTokenCount":170}}` + "\n\n"
