package api

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/fingerprint"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type publicReportAssertion struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}
type publicReportScenario struct {
	Endpoints     []publicReportEndpoint      `json:"endpoints"`
	ID            string                      `json:"id"`
	Layer         string                      `json:"layer"`
	State         string                      `json:"state"`
	Reason        string                      `json:"reason"`
	Current       bool                        `json:"current"`
	Aggregate     bool                        `json:"aggregate"`
	ScopeVersion  string                      `json:"scope_version"`
	CodeVersion   string                      `json:"code_version"`
	SourceIDs     []string                    `json:"source_ids"`
	Assertions    []publicReportAssertion     `json:"assertions"`
	EstimatedHTTP *int64                      `json:"estimated_http"`
	Usage         detection.Usage             `json:"usage"`
	Capacity      []detection.CapacityReport  `json:"capacity"`
	Settings      []detection.SettingEvidence `json:"settings"`
	Images        []detection.ImageEvidence   `json:"images"`
	Fingerprint   *fingerprint.Report         `json:"fingerprint"`
}
type publicReportEndpoint struct {
	ID             string `json:"id"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	ProxySupported bool   `json:"proxy_supported"`
}
type publicDetectionReport struct {
	ID              string                  `json:"id"`
	TargetKind      string                  `json:"target_kind"`
	TargetID        string                  `json:"target_id"`
	GroupID         string                  `json:"group_id"`
	State           string                  `json:"state"`
	Validity        string                  `json:"validity"`
	Complete        bool                    `json:"complete"`
	ManifestVersion string                  `json:"manifest_version"`
	RegistryVersion string                  `json:"registry_version"`
	Parameters      detection.Parameters    `json:"parameters"`
	Usage           detection.Usage         `json:"usage"`
	UpdatedAt       time.Time               `json:"updated_at"`
	Scenarios       []publicReportScenario  `json:"scenarios"`
	Sources         []publicReportSource    `json:"sources"`
	Exclusions      []publicReportExclusion `json:"exclusions"`
}
type publicReportSource struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Version string `json:"version"`
}
type publicReportExclusion struct {
	Protocol  string   `json:"protocol"`
	SourceIDs []string `json:"source_ids"`
	Reason    string   `json:"reason"`
}

func (a *app) getMarketReport(w http.ResponseWriter, r *http.Request) {
	s, state, ok := a.marketSnapshot(w, r)
	if !ok {
		return
	}
	id := r.PathValue("reportID")
	for _, j := range state.Data.Reports {
		if j.ReportID == nil || *j.ReportID != id {
			continue
		}
		groupID := j.Target.GroupID
		if j.Target.Binding.TargetKind == "group" {
			groupID = j.Target.Binding.TargetID
		}
		if _, ok := s.Groups[groupID]; !ok {
			break
		}
		if j.Target.Binding.TargetKind == "group_model" {
			if _, ok := s.Models[j.Target.Binding.TargetID]; !ok {
				break
			}
		}
		manifest, err := gatewaycontract.Load()
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, 200, publicReport(j, state.Reports[j.Target.Binding.TargetID], manifest, a.gatewayEndpoints))
		return
	}
	writeError(w, 404, "report_not_found", "没有已批准且可见的检测报告")
}

// The immutable approved report is projected field by field. No owner/task,
// credentials, target profile/headers, entry URL, native resource IDs or raw
// attempts leave this handler. Quota/account balances remain private too.
func publicReport(j detection.Job, projection detection.Projection, m gatewaycontract.Manifest, proxy map[string]bool) publicDetectionReport {
	out := publicDetectionReport{ID: *j.ReportID, TargetKind: j.Target.Binding.TargetKind, TargetID: j.Target.Binding.TargetID, GroupID: j.Target.GroupID, State: j.State, Validity: "stale", Complete: j.Complete, ManifestVersion: j.Plan.ManifestVersion, RegistryVersion: j.Plan.RegistryVersion, Parameters: j.Parameters, Usage: j.Usage, UpdatedAt: j.UpdatedAt, Scenarios: []publicReportScenario{}, Sources: []publicReportSource{}, Exclusions: []publicReportExclusion{}}
	for _, v := range m.Sources {
		out.Sources = append(out.Sources, publicReportSource{v.ID, v.URL, v.Version})
	}
	for _, v := range m.Exclusions {
		out.Exclusions = append(out.Exclusions, publicReportExclusion{v.Protocol, v.SourceIDs, v.Reason})
	}
	if out.TargetKind == "group" {
		out.GroupID = out.TargetID
	}
	reqs := map[string]gatewaycontract.Requirement{}
	endpoints := map[string]gatewaycontract.Endpoint{}
	for _, e := range m.Endpoints {
		endpoints[e.ID] = e
	}
	for _, suite := range m.Suites {
		for _, req := range suite.Requirements {
			reqs[req.ID] = req
		}
	}
	results := map[string]detection.Result{}
	for _, v := range j.Results {
		results[v.ScenarioID] = v
	}
	current := map[string]bool{}
	if projection.Valid {
		for _, c := range projection.Capabilities {
			current[c.Layer+"/"+c.ID] = true
		}
	}
	all, any := true, false
	for _, plan := range j.Plan.Items {
		result, exists := results[plan.ID]
		v := publicReportScenario{ID: plan.ID, Layer: plan.Layer, State: result.State, Reason: result.Reason, Aggregate: plan.Aggregate, ScopeVersion: plan.ScopeVersion, CodeVersion: plan.CodeVersion, SourceIDs: []string{}, Assertions: []publicReportAssertion{}, EstimatedHTTP: plan.EstimatedHTTP, Usage: result.Usage, Capacity: result.Capacity, Settings: result.Settings, Images: result.Images, Fingerprint: result.Fingerprint}
		if !exists {
			v.State = "untested"
			v.Reason = "not_executed"
		}
		v.Current = projection.Valid && !plan.Aggregate && exists
		for _, assertion := range result.Assertions {
			label := plan.AssertionLabels[assertion.ID]
			v.Assertions = append(v.Assertions, publicReportAssertion{ID: assertion.ID, Label: label, State: assertion.State, Reason: assertion.Reason})
			cut := strings.LastIndex(assertion.ID, "/a")
			if cut < 0 || !current[plan.Layer+"/"+assertion.ID[:cut]+"/"+label] {
				v.Current = false
			}
		}
		for _, id := range plan.Requirements {
			v.SourceIDs = append(v.SourceIDs, reqs[id].SourceIDs...)
			v.SourceIDs = append(v.SourceIDs, reqs[id].Conditional...)
		}
		v.Endpoints = []publicReportEndpoint{}
		seenEndpoints := map[string]bool{}
		for _, id := range plan.Requirements {
			for _, eid := range reqs[id].Endpoints {
				e, ok := endpoints[eid]
				if !ok || seenEndpoints[eid] {
					continue
				}
				seenEndpoints[eid] = true
				v.Endpoints = append(v.Endpoints, publicReportEndpoint{ID: e.ID, Method: e.Method, Path: e.Path, ProxySupported: proxy[e.ID]})
			}
		}
		slices.SortFunc(v.Endpoints, func(a, b publicReportEndpoint) int { return strings.Compare(a.ID, b.ID) })
		slices.Sort(v.SourceIDs)
		v.SourceIDs = slices.Compact(v.SourceIDs)
		if !plan.Aggregate {
			any = any || v.Current
			all = all && v.Current
		}
		out.Scenarios = append(out.Scenarios, v)
	}
	if any {
		out.Validity = "partial"
		if all {
			out.Validity = "current"
		}
	}
	return out
}
