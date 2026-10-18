package contribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type reportPlanner struct{ pending bool }

func (p reportPlanner) Plan(detection.Target) (detection.Plan, error) {
	return detection.Plan{EngineVersion: "fixture.v1", ManifestVersion: "fixture.v1", RegistryVersion: "fixture-code", Items: []detection.Planned{{ID: "fixture.native", Implemented: !p.pending, Layer: "upstream_protocol", Assertions: []string{"fixture.native/a1"}}}}, nil
}

// This is an isolated trusted-storage fixture of a complete NEGATIVE report,
// not a detector override, actual upstream call or supplier support claim.
func negativeReport(target detection.Target, id string) detection.Job {
	plan, _ := (reportPlanner{}).Plan(target)
	settings := gatewaycontract.SearchSettings{Version: "fixture", Method: "binary", Low: 0, High: 8, HardMaximum: 8, PrecisionMode: "absolute", Precision: 1, Rounds: 4}
	parameters := detection.Parameters{Context: settings, MaxOutput: settings}
	encoded, _ := json.Marshal(parameters)
	hash := sha256.Sum256(encoded)
	target.Binding.ManifestVersion = plan.ManifestVersion
	target.Binding.CodeVersion = plan.RegistryVersion
	target.Binding.ParameterVersion = hex.EncodeToString(hash[:])
	target.Binding.CredentialScope = strings.Repeat("a", 64)
	return detection.Job{ID: "fixture-task", OwnerID: "member", Target: target, Parameters: parameters, Plan: plan, State: "finished", Complete: true, ReportID: &id, Results: []detection.Result{{ScenarioID: "fixture.native", Layer: "upstream_protocol", Source: "real_upstream", State: "unsupported", Reason: "unsupported", Assertions: []detection.Assertion{{ID: "fixture.native/a1", State: "unsupported", Reason: "unsupported", AttemptIDs: []string{"fixture-attempt"}}}}}}
}
func TestNegativeCompleteReportsQualifyButPendingOrForeignEvidenceDoesNot(t *testing.T) {
	draft := starterDraft()
	_, targets, err := Overlay(draft, market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	repo := &memoryReview{reports: map[string]detection.Job{}}
	for i, target := range targets {
		id := []string{"group-report", "model-report"}[i]
		repo.reports[id] = negativeReport(target, id)
		draft.ReportIDs = append(draft.ReportIDs, id)
	}
	evaluation, err := Evaluate(context.Background(), Actor{ID: "member"}, draft, market.Directory{}, repo, reportPlanner{})
	if err != nil || !evaluation.Ready || len(evaluation.Reports) != 2 {
		t.Fatalf("negative complete reports blocked: %+v %v", evaluation, err)
	}
	evaluation, err = Evaluate(context.Background(), Actor{ID: "member"}, draft, market.Directory{}, repo, reportPlanner{pending: true})
	if err != nil || evaluation.Ready || evaluation.Targets[0].State != "suite_pending" {
		t.Fatal("missing adapters disappeared from creation denominator", err)
	}
	for id, report := range repo.reports {
		report.OwnerID = "another-member"
		repo.reports[id] = report
	}
	evaluation, err = Evaluate(context.Background(), Actor{ID: "member"}, draft, market.Directory{}, repo, reportPlanner{})
	if err != nil || evaluation.Ready {
		t.Fatal("private foreign report borrowed", err)
	}
}
func TestPriceRevisionReusesCompatibleEvidenceButChangedHeadersAndInterruptedReportsDoNot(t *testing.T) {
	initial := starterDraft()
	live, targets, err := Overlay(initial, market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	repo := &memoryReview{reports: map[string]detection.Job{}}
	for i, target := range targets {
		id := []string{"group-report", "model-report"}[i]
		repo.reports[id] = negativeReport(target, id)
	}
	model := live.Models[0]
	quote := live.Quotes[0]
	quote.ID = "new-quote"
	draft := Draft{OwnerID: "member", Changes: market.Changes{Models: []market.Change[market.GroupModel]{{BaseRevision: 1, Value: model}}, Quotes: []market.Quote{quote}}, ReportIDs: []string{"group-report", "model-report"}}
	evaluation, err := Evaluate(context.Background(), Actor{ID: "member"}, draft, live, repo, reportPlanner{})
	if err != nil || !evaluation.Ready || !evaluation.Targets[0].Report.Reused {
		t.Fatal("price revision forced full new evidence", err)
	}
	group := live.Groups[0]
	ua := "different-ua"
	group.Defaults.UserAgent = &ua
	draft.Changes.Groups = []market.Change[market.Group]{{BaseRevision: 1, Value: group}}
	evaluation, err = Evaluate(context.Background(), Actor{ID: "member"}, draft, live, repo, reportPlanner{})
	if err != nil || evaluation.Ready {
		t.Fatal("late evidence ignored changed header inputs", err)
	}
	draft.Changes.Groups = nil
	for id, report := range repo.reports {
		report.State = "interrupted"
		repo.reports[id] = report
	}
	evaluation, err = Evaluate(context.Background(), Actor{ID: "member"}, draft, live, repo, reportPlanner{})
	if err != nil || evaluation.Ready {
		t.Fatal("interrupted report counted as complete", err)
	}
}

func TestNewlyEnabledPricingTierRequiresNewSelectorEvidence(t *testing.T) {
	live, targets, err := Overlay(starterDraft(), market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	repo := &memoryReview{reports: map[string]detection.Job{}}
	for i, target := range targets {
		id := []string{"group-report", "model-report"}[i]
		repo.reports[id] = negativeReport(target, id)
	}
	quote := live.Quotes[0]
	quote.ID = "quote-with-new-tier"
	quote.Tiers = []market.Tier{{Conditions: market.Conditions{ServiceTier: "openai:fast"}, Prices: quote.Prices}}
	draft := Draft{OwnerID: "member", Changes: market.Changes{Models: []market.Change[market.GroupModel]{{BaseRevision: 1, Value: live.Models[0]}}, Quotes: []market.Quote{quote}}, ReportIDs: []string{"group-report", "model-report"}}
	evaluation, err := Evaluate(context.Background(), Actor{ID: "member"}, draft, live, repo, reportPlanner{})
	if err != nil || evaluation.Ready {
		t.Fatal("old default-tier report authorized a new enabled tier", err)
	}
	_, updated, err := Overlay(draft, live)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range updated {
		if target.Binding.TargetKind == "group_model" {
			repo.reports["new-tier-report"] = negativeReport(target, "new-tier-report")
		}
	}
	draft.ReportIDs = append(draft.ReportIDs, "new-tier-report")
	evaluation, err = Evaluate(context.Background(), Actor{ID: "member"}, draft, live, repo, reportPlanner{})
	if err != nil || !evaluation.Ready {
		t.Fatal("fresh complete negative selector evidence did not qualify", err)
	}
}
