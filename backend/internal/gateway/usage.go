package gateway

import (
	"math"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// UsageState separates "the upstream sent no usage at all" from "the upstream
// sent usage this platform cannot price". Absent usage only affects settlement;
// invalid usage must poison the whole attempt and never be replaced by an
// earlier valid snapshot.
type UsageState uint8

const (
	UsageAbsent UsageState = iota
	UsageValid
	UsageInvalid
)

type UsageObservation struct {
	InputTokens      *int64
	OutputTokens     *int64
	CacheWriteTokens *int64
	CacheReadTokens  *int64
	Conflict         bool
}

func (o *UsageObservation) Merge(next UsageObservation) {
	o.Conflict = o.Conflict || next.Conflict || mergeUsageValue(&o.InputTokens, next.InputTokens) ||
		mergeCumulativeUsageValue(&o.OutputTokens, next.OutputTokens) || mergeUsageValue(&o.CacheWriteTokens, next.CacheWriteTokens) ||
		mergeUsageValue(&o.CacheReadTokens, next.CacheReadTokens)
}

func mergeCumulativeUsageValue(current **int64, next *int64) bool {
	if next == nil {
		return false
	}
	if *current != nil && *next < **current {
		return true
	}
	value := *next
	*current = &value
	return false
}

func (o UsageObservation) Complete() (*ledger.UsageV1, bool) {
	if o.Conflict || o.InputTokens == nil || o.OutputTokens == nil || o.CacheWriteTokens == nil || o.CacheReadTokens == nil {
		return nil, false
	}
	usage := &ledger.UsageV1{
		InputTokens: *o.InputTokens, OutputTokens: *o.OutputTokens,
		CacheWriteTokens: *o.CacheWriteTokens, CacheReadTokens: *o.CacheReadTokens,
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheWriteTokens < 0 || usage.CacheReadTokens < 0 {
		return nil, false
	}
	return usage, true
}

func mergeUsageValue(current **int64, next *int64) bool {
	if next == nil {
		return false
	}
	if *current != nil && **current != *next {
		return true
	}
	value := *next
	*current = &value
	return false
}

// missingUsageState reports UsageAbsent when the key is missing or explicitly
// null, and UsageInvalid when it is present with the wrong type.
func missingUsageState(container map[string]any, key string) UsageState {
	raw, exists := container[key]
	if !exists || raw == nil {
		return UsageAbsent
	}
	return UsageInvalid
}

// openAIUsageState maps openAIUsage's "the usage object exists but cannot be
// priced" result onto UsageInvalid.
func openAIUsageState(usage map[string]any, inputKey, outputKey, detailKey string) (UsageObservation, UsageState) {
	observation, ok := openAIUsage(usage, inputKey, outputKey, detailKey)
	if !ok {
		return UsageObservation{}, UsageInvalid
	}
	return observation, UsageValid
}

func addNonnegativeTokens(values ...int64) (int64, bool) {
	total := int64(0)
	for _, value := range values {
		if value < 0 || total > math.MaxInt64-value {
			return 0, false
		}
		total += value
	}
	return total, true
}

func openAIUsage(usage map[string]any, inputKey, outputKey, detailKey string) (UsageObservation, bool) {
	outputDetailKey := "output_tokens_details"
	if outputKey == "completion_tokens" {
		outputDetailKey = "completion_tokens_details"
	}
	inputTotal, inputOK := intField(usage, inputKey)
	output, outputOK := intField(usage, outputKey)
	if !inputOK || !outputOK {
		return UsageObservation{}, false
	}
	cached := int64(0)
	cacheWrite := int64(0)
	if details, ok := usage[detailKey].(map[string]any); ok {
		// Audio/image tokens are billable dimensions the four-bucket formula
		// cannot price; anything else in the details object is ignored.
		if !zeroOptionalTokenField(details, "audio_tokens") || !zeroOptionalTokenField(details, "image_tokens") {
			return UsageObservation{}, false
		}
		var cachedOK, cacheWriteOK bool
		cached, cachedOK = intFieldDefault(details, "cached_tokens", 0)
		cacheWrite, cacheWriteOK = intFieldDefault(details, "cache_write_tokens", 0)
		if !cachedOK || !cacheWriteOK {
			return UsageObservation{}, false
		}
	} else if _, exists := usage[detailKey]; exists {
		return UsageObservation{}, false
	}
	reasoning := int64(0)
	if details, ok := usage[outputDetailKey].(map[string]any); ok {
		if !nonnegativeOptionalTokenField(details, "reasoning_tokens") || !zeroOptionalTokenField(details, "audio_tokens") ||
			!nonnegativeOptionalTokenField(details, "accepted_prediction_tokens") || !nonnegativeOptionalTokenField(details, "rejected_prediction_tokens") {
			return UsageObservation{}, false
		}
		var reasoningOK bool
		reasoning, reasoningOK = intFieldDefault(details, "reasoning_tokens", 0)
		if !reasoningOK {
			return UsageObservation{}, false
		}
	} else if _, exists := usage[outputDetailKey]; exists {
		return UsageObservation{}, false
	}
	visibleOutput := output
	billedOutput := output
	combinedVisible, combinedVisibleOK := addNonnegativeTokens(inputTotal, visibleOutput)
	if !combinedVisibleOK {
		return UsageObservation{}, false
	}
	if outputKey == "completion_tokens" && reasoning > 0 {
		combinedBilled, combinedBilledOK := addNonnegativeTokens(combinedVisible, reasoning)
		if !combinedBilledOK {
			return UsageObservation{}, false
		}
		if _, exists := usage["total_tokens"]; exists {
			total, valid := intField(usage, "total_tokens")
			if !valid {
				return UsageObservation{}, false
			}
			switch total {
			case combinedVisible:
			case combinedBilled:
				billedOutput, _ = addNonnegativeTokens(visibleOutput, reasoning)
			default:
				return UsageObservation{}, false
			}
		}
	} else if _, exists := usage["total_tokens"]; exists {
		total, valid := intField(usage, "total_tokens")
		if !valid || total != combinedVisible {
			return UsageObservation{}, false
		}
	}
	output = billedOutput
	partitioned, partitionedOK := addNonnegativeTokens(cached, cacheWrite)
	if !partitionedOK || inputTotal < partitioned {
		return UsageObservation{}, false
	}
	input := inputTotal - partitioned
	return UsageObservation{
		InputTokens: &input, OutputTokens: &output, CacheWriteTokens: &cacheWrite, CacheReadTokens: &cached,
	}, true
}

func allZeroTokenFields(value map[string]any) bool {
	for key := range value {
		if !zeroOptionalTokenField(value, key) {
			return false
		}
	}
	return true
}

func nonnegativeOptionalTokenField(value map[string]any, key string) bool {
	if _, exists := value[key]; !exists {
		return true
	}
	parsed, ok := intField(value, key)
	return ok && parsed >= 0
}

func zeroOptionalTokenField(value map[string]any, key string) bool {
	if _, exists := value[key]; !exists {
		return true
	}
	parsed, ok := intField(value, key)
	return ok && parsed == 0
}
