package detection

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

// TestProfile contains enabled selectors, never prices. Changing an amount or
// address reuses evidence; enabling a new service tier or image pricing variant
// requires that variant to be exercised before the report can authorize review.
type TestProfile struct {
	ServiceTiers []string       `json:"service_tiers"`
	Images       []ImageVariant `json:"images"`
}
type ImageVariant struct {
	Unit        string `json:"unit"`
	Size        string `json:"size"`
	Quality     string `json:"quality"`
	Background  string `json:"background"`
	ServiceTier string `json:"service_tier"`
}

func profileFor(q market.Quote) TestProfile {
	p := TestProfile{ServiceTiers: []string{}, Images: []ImageVariant{}}
	for _, tier := range q.Tiers {
		if s := tier.Conditions.ServiceTier; s != "" {
			p.ServiceTiers = append(p.ServiceTiers, s)
		}
	}
	for _, u := range q.UnitPrices {
		if u.ServiceTier != "" {
			p.ServiceTiers = append(p.ServiceTiers, u.ServiceTier)
		}
		if u.Tool == "" {
			p.Images = append(p.Images, ImageVariant{Unit: u.Unit, Size: u.ImageSize, Quality: u.ImageQuality, Background: u.ImageBackground, ServiceTier: u.ServiceTier})
		}
	}
	slices.Sort(p.ServiceTiers)
	p.ServiceTiers = slices.Compact(p.ServiceTiers)
	slices.SortFunc(p.Images, func(a, b ImageVariant) int {
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		return strings.Compare(string(aa), string(bb))
	})
	p.Images = slices.Compact(p.Images)
	return p
}
func CompatibleProfile(a, b TestProfile) bool {
	return slices.Equal(a.ServiceTiers, b.ServiceTiers) && slices.Equal(a.Images, b.Images)
}
func profileScenarios(plan Plan) []string {
	var ids []string
	for _, item := range plan.Items {
		for _, req := range item.Requirements {
			if strings.HasSuffix(req, ".cache_tier") || strings.HasPrefix(req, "images.") || strings.HasSuffix(req, ".image_tool") || req == "codex-special.RL24" || req == "codex-special.RL25" {
				ids = append(ids, item.ID)
				break
			}
		}
	}
	return ids
}
