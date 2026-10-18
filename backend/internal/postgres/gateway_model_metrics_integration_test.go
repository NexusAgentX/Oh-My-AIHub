package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestModelMetricHistoryUsesCompletedTimeVersionAndSelectionBoundary(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("model-metric-consumer", "100")
	const candidate = "a0000000-0000-4000-8000-000000000001"
	// This call began outside the window but finished inside it. Its first
	// address failed; health must count only the final selection observation.
	_, err := e.pool.Exec(context.Background(), `INSERT INTO calls(account_id,model_id,requested_model,format,stream,outcome,created_at,completed_at,attempts)
VALUES($1,'gpt-test','gpt-test','openai_chat',true,'succeeded',now()-interval '25 hours',now()-interval '1 minute',
jsonb_build_array(
 jsonb_build_object('metrics_version',2,'metric_tier','openai:fast','channel',jsonb_build_object('id',$2::text),'end_reason','upstream_error','selection_final',false),
 jsonb_build_object('metrics_version',2,'metric_tier','openai:fast','channel',jsonb_build_object('id',$2::text),'end_reason','completed','selection_final',true,'selection_output_ms',200,'output_tps',80)))`, consumer.id, candidate)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.pool.Exec(context.Background(), `INSERT INTO calls(account_id,model_id,requested_model,format,stream,outcome,created_at,completed_at,attempts)
VALUES($1,'gpt-test','gpt-test','openai_chat',true,'upstream_failed',now()-interval '2 minutes',now()-interval '1 minute',
jsonb_build_array(
 jsonb_build_object('metrics_version',2,'metric_tier','openai:fast','channel',jsonb_build_object('id',$2::text),'end_reason','client_error'),
 jsonb_build_object('metrics_version',2,'metric_tier','openai:fast','channel',jsonb_build_object('id',$2::text),'end_reason','client_disconnected'),
 jsonb_build_object('channel',jsonb_build_object('id',$2::text),'end_reason','completed','ttft_ms',1)))`, consumer.id, candidate)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := e.store.Gateway.ModelAttemptHistory(context.Background(), time.Now().Add(-24*time.Hour), 8193)
	if err != nil {
		t.Fatal(err)
	}
	var counted, successes, excluded, client int64
	for _, row := range rows {
		if row.ChannelID != candidate || row.ModelID != "gpt-test" || row.Format != channel.FormatOpenAIChat || row.Tier != "openai:fast" || !row.Stream || row.HistoryTruncated {
			t.Fatalf("wrong scope: %+v", row)
		}
		counted += row.Counted
		successes += row.Successes
		excluded += row.Excluded
		client += row.ClientErrors
	}
	if counted != 1 || successes != 1 || excluded != 2 || client != 1 {
		t.Fatalf("history counted address/legacy/excluded attempts incorrectly: %d/%d excluded=%d client=%d", successes, counted, excluded, client)
	}
	if err := e.engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := e.engine.ModelStats("gpt-test", channel.FormatOpenAIChat, "priority", true)[candidate]
	if stats.Attempts != 1 || stats.Successes != 1 || stats.ExcludedSamples != 2 || stats.FirstOutputSamples != 1 || stats.OutputSpeedSamples != 1 || stats.FirstOutputP50MS == nil || *stats.FirstOutputP50MS < 190 || *stats.FirstOutputP50MS > 220 || stats.OutputTPSP50 == nil || *stats.OutputTPSP50 < 75 || *stats.OutputTPSP50 > 85 {
		t.Fatalf("warm-up did not preserve v2 facts: %+v", stats)
	}
	if len(e.engine.ModelStats("gpt-test", channel.FormatOpenAIChat, "", true)) != 0 || len(e.engine.ModelStats("gpt-test", channel.FormatOpenAIChat, "priority", false)) != 0 {
		t.Fatal("warm-up leaked into another calling context")
	}
}
