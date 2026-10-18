package contribution

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type ReportUse struct {
	ReportID        string    `json:"report_id"`
	TargetKind      string    `json:"target_kind"`
	TargetID        string    `json:"target_id"`
	Reused          bool      `json:"reused"`
	FinishedAt      time.Time `json:"finished_at"`
	ManifestVersion string    `json:"manifest_version"`
	CodeVersion     string    `json:"code_version"`
}
type TargetReview struct {
	Kind    string     `json:"kind"`
	ID      string     `json:"id"`
	ModelID string     `json:"model_id"`
	State   string     `json:"state"`
	Report  *ReportUse `json:"report"`
}
type Evaluation struct {
	Ready   bool            `json:"ready"`
	Targets []TargetReview  `json:"targets"`
	Reports []detection.Job `json:"-"`
}

// Evaluate reads only immutable trusted reports. Approved evidence may be
// reused without exposing another member's private task/attempt metadata.
// Negative complete conclusions qualify; absent implementations never do.
func Evaluate(ctx context.Context, actor Actor, draft Draft, live market.Directory, u UnitOfWork, planner ReportPlanner) (Evaluation, error) {
	_, targets, err := Overlay(draft, live)
	if err != nil {
		return Evaluation{}, err
	}
	result := Evaluation{Ready: true, Targets: []TargetReview{}, Reports: []detection.Job{}}
	if planner == nil {
		return result, ErrInvalid
	}
	targetIDs := make([]string, len(targets))
	for i, target := range targets {
		targetIDs[i] = target.Binding.TargetID
	}
	approved, err := u.ApprovedReports(ctx, targetIDs)
	if err != nil {
		return result, err
	}
	ids := append(slices.Clone(draft.ReportIDs), approved...)
	seen := map[string]bool{}
	ids = slices.DeleteFunc(ids, func(id string) bool {
		if seen[id] {
			return true
		}
		seen[id] = true
		return false
	})
	loaded := map[string]detection.Job{}
	for _, id := range ids {
		report, err := u.Report(ctx, id)
		if err != nil {
			if errors.Is(err, detection.ErrNotFound) || errors.Is(err, ErrNotFound) {
				continue
			}
			return result, err
		}
		if report.ReportID == nil || *report.ReportID != id || (!actor.Admin && report.OwnerID != draft.OwnerID && !slices.Contains(approved, id)) {
			continue
		}
		loaded[id] = report
	}
	for _, target := range targets {
		entry := TargetReview{Kind: target.Binding.TargetKind, ID: target.Binding.TargetID, ModelID: target.Model, State: "report_required"}
		plan, err := planner.Plan(target)
		if err != nil {
			return result, err
		}
		if detection.HasPending(plan) || len(plan.Items) == 0 {
			entry.State = "suite_pending"
			result.Ready = false
			result.Targets = append(result.Targets, entry)
			continue
		}
		for _, id := range ids {
			report, ok := loaded[id]
			if !ok || !report.Complete || !detection.Complete(report) {
				continue
			}
			current := target.Binding
			current.ManifestVersion = plan.ManifestVersion
			current.CodeVersion = plan.RegistryVersion
			// Search settings were fixed for this report; administrative changes
			// affect future tasks, not otherwise valid completed evidence.
			current.ParameterVersion = report.Target.Binding.ParameterVersion
			current.CredentialScope = report.Target.Binding.CredentialScope
			if !market.CompatibleBinding(report.Target.Binding, current) {
				continue
			}
			entry.State = "ready"
			entry.Report = &ReportUse{ReportID: id, TargetKind: entry.Kind, TargetID: entry.ID, Reused: slices.Contains(approved, id) || report.Target.Binding.TargetRevision < current.TargetRevision, FinishedAt: report.UpdatedAt, ManifestVersion: plan.ManifestVersion, CodeVersion: plan.RegistryVersion}
			result.Reports = append(result.Reports, report)
			break
		}
		if entry.Report == nil {
			result.Ready = false
		}
		result.Targets = append(result.Targets, entry)
	}
	return result, nil
}
