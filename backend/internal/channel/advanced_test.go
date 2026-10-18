package channel

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"testing"
)

func TestValidateAdvancedRejectsForbiddenHeadersAndBadRanges(t *testing.T) {
	ua := "agent/1"
	bad := []Advanced{
		{HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "Authorization", Value: "x"}}}},
		{HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "host", Value: "x"}}}},
		{HeaderRules: HeaderRules{Remove: []string{"Content-Length"}}},
		{HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "X-Ok", Value: "line\nbreak"}}}},
		{HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "bad name", Value: "x"}}}},
	}
	zero, tooHigh, short, long := int32(0), int32(200), int32(500), int32(5)
	bad = append(bad, Advanced{ConcurrencyLimit: &zero}, Advanced{CooldownFailures: &tooHigh}, Advanced{TTFTTimeoutMS: &short},
		Advanced{IdleTimeoutMS: &[]int32{999}[0]}, Advanced{CooldownSeconds: &long})
	for index, advanced := range bad {
		if err := ValidateAdvanced(advanced); err == nil {
			t.Fatalf("case %d was accepted: %+v", index, advanced)
		}
	}
	good := Advanced{UserAgent: &ua, HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "X-Tenant", Value: "acme"}}, Remove: []string{"X-Debug"}}}
	if err := ValidateAdvanced(good); err != nil {
		t.Fatal(err)
	}
}

func TestCustomPricesBlended(t *testing.T) {
	if got := (CustomPrices{Input: money.FromNano(1e9), Output: money.FromNano(5e9), CacheRead: money.FromNano(9e9)}).Blended(); got != 2e9 {
		t.Fatalf("blended = %v", got)
	}
}
