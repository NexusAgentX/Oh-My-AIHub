package ledger

import (
	"testing"
	"time"
)

func TestPriorityUsesFastPriceWithoutRewritingObservedTier(t *testing.T) {
	detail := &UsageDetail{ServiceTier: "openai:priority"}
	tier := PriceTier{ServiceTier: "openai:fast"}
	if !tier.Matches(10, time.Now(), detail) {
		t.Fatal("priority did not match fast")
	}
	if detail.ServiceTier != "openai:priority" {
		t.Fatal("observed tier mutated")
	}
	if (PriceTier{ServiceTier: "openai:ultrafast"}).Matches(10, time.Now(), detail) {
		t.Fatal("priority matched ultrafast")
	}
	if (PriceTier{ServiceTier: "openai:fast"}).Matches(10, time.Now(), &UsageDetail{ServiceTier: "openai:default"}) {
		t.Fatal("default matched fast")
	}
}
