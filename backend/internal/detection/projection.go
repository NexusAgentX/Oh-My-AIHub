package detection

import (
	"slices"
	"sort"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

// Projection is a pure, metadata-only view suitable for building an immutable
// routing snapshot off the request path. It does not authorize creation, perform
// I/O, derive prices/speed, or confer support from an aggregate protocol result.
type Projection struct {
	Valid        bool
	Capabilities []market.Capability
	Capacity     []VerifiedCapacity
	Settings     []SettingEvidence
	Images       []ImageEvidence
}
type VerifiedCapacity struct {
	ScenarioID    string
	Kind          string
	Unit          string
	VerifiedLower int64
	Approximate   bool
}

// Project accepts only a sealed, complete real-upstream report with current
// target/digest/manifest/implementation/parameter/credential binding. Age and
// address are deliberately absent. Each assertion remains independent: a
// missing receipt cannot erase a verified tool execution or prove billing.
func Project(job Job, current market.EvidenceBinding) Projection {
	p := Projection{Capabilities: []market.Capability{}, Capacity: []VerifiedCapacity{}, Settings: []SettingEvidence{}, Images: []ImageEvidence{}}
	if job.ReportID == nil || *job.ReportID == "" || job.ExecutionActive || !Complete(job) || !market.CompatibleBinding(job.Target.Binding, current) {
		return p
	}
	items := map[string]Planned{}
	for _, item := range job.Plan.Items {
		items[item.ID] = item
	}
	for _, result := range job.Results {
		if result.Source != "real_upstream" {
			return Projection{}
		}
		item := items[result.ScenarioID]
		if item.Aggregate {
			continue
		}
		witnesses := map[string]Attempt{}
		for _, a := range result.Attempts {
			witnesses[a.ID] = a
		}
		if ValidateResult(item, result, witnesses) != nil {
			return Projection{}
		}
		for _, a := range result.Assertions {
			label := item.AssertionLabels[a.ID]
			if label == "" {
				return Projection{}
			}
			p.Capabilities = append(p.Capabilities, market.Capability{ID: assertionRequirement(item, a.ID) + "/" + label, Layer: result.Layer, State: a.State, Reason: a.Reason, ReportID: *job.ReportID})
		}
		for _, c := range result.Capacity {
			if c.VerifiedLower == nil || *c.VerifiedLower <= 0 {
				continue
			}
			for _, step := range c.Steps {
				if step.Accepted && step.Semantic && !step.Approximate && step.Actual != nil && *step.Actual == *c.VerifiedLower {
					p.Capacity = append(p.Capacity, VerifiedCapacity{ScenarioID: result.ScenarioID, Kind: c.Kind, Unit: c.Unit, VerifiedLower: *c.VerifiedLower})
					break
				}
			}
		}
		for _, setting := range result.Settings {
			setting.AttemptIDs = slices.Clone(setting.AttemptIDs)
			p.Settings = append(p.Settings, setting)
		}
		for _, image := range result.Images {
			image.AttemptIDs = slices.Clone(image.AttemptIDs)
			image.Products = slices.Clone(image.Products)
			p.Images = append(p.Images, image)
		}
	}
	sort.Slice(p.Capabilities, func(i, j int) bool {
		if p.Capabilities[i].ID == p.Capabilities[j].ID {
			return p.Capabilities[i].Layer < p.Capabilities[j].Layer
		}
		return p.Capabilities[i].ID < p.Capabilities[j].ID
	})
	p.Valid = true
	return p
}
func assertionRequirement(item Planned, assertion string) string {
	for _, req := range item.Requirements {
		for i := range item.Assertions {
			if AssertionID(req, i) == assertion {
				return req
			}
		}
	}
	return ""
}

// Supports requires positive evidence for every explicit capability and the
// requested proof layer. Unknown, stale, partial, conditional-not-observed and
// unsupported never silently fall back to another model/protocol.
func (p Projection) Supports(layer string, required []string) bool {
	if !p.Valid {
		return false
	}
	for _, id := range required {
		if !slices.ContainsFunc(p.Capabilities, func(c market.Capability) bool { return c.ID == id && c.Layer == layer && c.State == "pass" }) {
			return false
		}
	}
	return true
}
