package gateway

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// A finite vocabulary prevents request-supplied labels from growing metrics.
// Empty/auto is the default calling context. Explicit tiers only learn timing
// from a response that actually confirms that tier (a downgrade is not priority).
func requestedMetricTier(format channel.Format, value string) string {
	if value == "" || value == "auto" {
		return ""
	}
	return actualMetricTier(format, value)
}
func actualMetricTier(format channel.Format, value string) string {
	prefix := "openai:"
	if format == channel.FormatAnthropic {
		prefix = "anthropic:"
	}
	if format == channel.FormatGemini {
		prefix = "gemini:"
	}
	value = strings.TrimPrefix(value, prefix)
	if prefix == "openai:" && value == "priority" {
		value = "fast"
	}
	switch value {
	case "default", "standard", "priority", "flex", "fast", "ultrafast", "batch", "scale":
		return prefix + value
	default:
		return "unknown"
	}
}

// Called only on usage-bearing objects, using borrowed fields and bounded
// scratch storage. Counts are observation facts and never affect billing.
func (o *Observer) observeMetricCounts(raw []byte) {
	if !json.Valid(raw) {
		return
	}
	o.members = o.members[:0]
	root := o.objectOf(raw)
	if nested := root.get("response"); len(nested) > 0 {
		root = o.objectOf(nested)
	}
	usage := o.objectOf(root.get("usage"))
	switch o.format {
	case channel.FormatOpenAIChat, channel.FormatOpenAIResponses:
		field := "completion_tokens_details"
		if o.format == channel.FormatOpenAIResponses {
			field = "output_tokens_details"
		}
		detail := o.objectOf(usage.get(field))
		if detail != nil {
			o.metricReasoningKnown = false
			if n, ok := count(detail.get("reasoning_tokens")); ok {
				o.metricReasoningValue, o.metricReasoningKnown = n, true
			}
		}
	case channel.FormatGemini:
		usage = o.objectOf(root.get("usageMetadata"))
		if usage != nil {
			o.metricVisibleKnown = false
			// Native candidates exclude thoughts only when the validated total
			// agrees with the native disjoint total equation.
			candidate, cok := count(usage.get("candidatesTokenCount"))
			thought, tok := count(usage.get("thoughtsTokenCount"))
			prompt, pok := count(usage.get("promptTokenCount"))
			total, all := count(usage.get("totalTokenCount"))
			tool := int64(0)
			toolOK := true
			if usage.has("toolUsePromptTokenCount") {
				tool, toolOK = count(usage.get("toolUsePromptTokenCount"))
			}
			if !usage.has("thoughtsTokenCount") {
				thought, tok = 0, true
			}
			if sum, valid := sumTokens(prompt, tool, candidate, thought); cok && tok && pok && all && toolOK && valid && sum == total {
				o.metricVisibleValue, o.metricVisibleKnown = candidate, true
			}
		}
	}
	o.members = o.members[:0]
}

func observedOutputTPS(call *callState, o Observation) (*int, *float64) {
	if !call.stream || call.polling || call.format == channel.FormatOpenAIImages || !o.Found || o.MultipleOutputs || o.MediaOutput || o.FirstVisibleOutputAt.IsZero() || !o.OutputEndAt.After(o.FirstVisibleOutputAt) {
		return nil, nil
	}
	if o.Usage.Detail != nil && len(o.Usage.Detail.Iterations) > 0 {
		return nil, nil
	}
	tokens := o.Usage.OutputTokens
	if call.format == channel.FormatGemini {
		if o.VisibleTokens == nil {
			return nil, nil
		}
		tokens = *o.VisibleTokens
	} else if o.ReasoningTokens != nil {
		if *o.ReasoningTokens < 0 || *o.ReasoningTokens > tokens {
			return nil, nil
		}
		tokens -= *o.ReasoningTokens
	} else if o.ReasoningOutput || o.OpaqueOutput {
		return nil, nil
	}
	ms := int(o.OutputEndAt.Sub(o.FirstVisibleOutputAt) / time.Millisecond)
	// Tiny completions and intervals are dominated by chunk/network scheduling.
	if tokens < 32 || ms < 50 {
		return nil, nil
	}
	tps := float64(tokens) * 1000 / float64(ms)
	return &ms, &tps
}

func populateAttemptMetrics(call *callState, record *Attempt, o Observation, started time.Time) {
	record.MetricsVersion = 2
	record.MetricTier = requestedMetricTier(call.format, call.requestedServiceTier)
	record.ActualServiceTier = "unknown"
	if o.Usage.Detail != nil {
		record.ActualServiceTier = actualMetricTier(call.format, o.Usage.Detail.ServiceTier)
	}
	if call.polling {
		return
	}
	record.OutputKind = o.OutputKind
	if !o.FirstOutputAt.IsZero() && !o.FirstOutputAt.Before(started) {
		record.FirstOutputMS = millis(o.FirstOutputAt.Sub(started))
		selection := *record.FirstOutputMS
		if record.SelectionLatencyMS != nil && record.TTFTMS != nil {
			selection += max(0, *record.SelectionLatencyMS-*record.TTFTMS)
		}
		record.SelectionOutputMS = &selection
	}
	record.GenerationMS, record.OutputTPS = observedOutputTPS(call, o)
	if record.EndReason != EndCompleted {
		record.GenerationMS, record.OutputTPS = nil, nil
	}
	// Latency/TPS for explicitly requested tiers require actual confirmation.
	// The health counter still records a failed/downgraded requested tier.
	if record.MetricTier != "" && (record.MetricTier == "unknown" || record.MetricTier != record.ActualServiceTier) {
		record.SelectionOutputMS, record.OutputTPS = nil, nil
	}
}
