package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type browseMarketRepository struct {
	groupchannel.Repository
	data groupchannel.Data
}

func (r *browseMarketRepository) Snapshot(context.Context) (groupchannel.Data, error) {
	return r.data, nil
}

func TestMarketBrowseUsesApprovedIDsScopesCursorAndPublicationBarrier(t *testing.T) {
	repo := &browseMarketRepository{}
	runtime := &groupchannel.Runtime{Repository: repo}
	runtime.Rebuild = func(ctx context.Context) error {
		_, err := runtime.Wrap(groupBaseLoader{}).LoadCandidates(ctx)
		return err
	}
	p := newPlatform(t, func(d *Dependencies) { d.GroupChannels = &groupchannel.Service{Runtime: runtime, Repository: repo} })
	member, owner := p.member(t, "market-browser")
	quoteID := "00000000-0000-4000-8000-000000000084"
	otherQuoteID := "00000000-0000-4000-8000-000000000086"
	d := market.Directory{Suppliers: []market.Supplier{{ID: "supplier", Name: "Approved supplier", Kind: "official_subscription", Enabled: true, ContributorID: "PRIVATE-contributor"}}, Groups: []market.Group{{ID: "group", SupplierID: "supplier", Name: "Group", Enabled: true}}, Models: []market.GroupModel{{ID: "00000000-0000-4000-8000-000000000083", ModelID: "same/model:v1", DisplayName: "Different display", UpstreamModel: "PRIVATE-upstream", GroupID: "group", ModelType: "text", Enabled: true, QuoteID: &quoteID}, {ID: "00000000-0000-4000-8000-000000000085", ModelID: "z-model", GroupID: "group", ModelType: "text", Enabled: true}}, Quotes: []market.Quote{{ID: quoteID, GroupModelID: "00000000-0000-4000-8000-000000000083", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}}, Addresses: []market.Address{{ID: "address", BaseURL: "https://PRIVATE-origin.example"}}, GroupAddresses: []market.GroupAddress{{ID: "link", GroupID: "group", AddressID: "address"}}}
	d.Models[1].QuoteID = &otherQuoteID
	for i := range d.Models {
		d.Models[i].Revision = 1
	}
	d.Groups[0].Revision = 1
	d.Groups[0].ConnectionVersion = 1
	row := groupchannel.Record{ID: "channel", Owner: channel.Owner{ID: owner}, OwnerActive: true, Name: "Shared channel", GroupID: "group", Status: channel.StatusListed, Revision: 1, CredentialRevision: 1, AddressIDs: []string{"link"}}
	repo.data.Channels = []groupchannel.Record{row}
	repo.data.Probes = []groupchannel.Probe{{ChannelID: row.ID, GroupModelID: d.Models[0].ID, Binding: groupchannel.ProbeBinding(row, d.Groups[0], d.Models[0]), Tests: map[channel.Format]channel.FormatTest{channel.FormatOpenAIResponses: {OK: true}}}}
	d.Quotes = append(d.Quotes, market.Quote{ID: otherQuoteID, GroupModelID: "00000000-0000-4000-8000-000000000085", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"})
	reportID := "approved-report"
	search := gatewaycontract.SearchSettings{Version: "v1", Method: "binary", Low: 1, High: 1024, HardMaximum: 1024, Precision: 1, PrecisionMode: "absolute", Rounds: 4}
	repo.data.Reports = []detection.Job{{ReportID: &reportID, State: "finished", Complete: true, UpdatedAt: time.Now(), Target: detection.Target{GroupID: "group", Binding: market.EvidenceBinding{TargetKind: "group", TargetID: "group"}}, Parameters: detection.Parameters{Context: search, MaxOutput: search}}}
	if err := runtime.Install(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	member.expect(t, 401, "GET", "/api/market/models", nil, withoutCookie)
	member.expect(t, 200, "GET", "/api/market/reports/approved-report", nil)
	member.expect(t, 404, "GET", "/api/market/reports/unpublished-report", nil)
	hall := member.expect(t, 200, "GET", "/api/market/channels?only_online=true", nil)
	if hall["total"] != float64(1) {
		t.Fatal(hall)
	}
	member.expect(t, 200, "GET", "/api/market/channels/channel", nil)
	member.expect(t, 404, "GET", "/api/market/channels/missing", nil)
	first := member.expect(t, 200, "GET", "/api/market/models?limit=1", nil)
	if first["total"] != float64(2) || first["next_cursor"] == nil || first["items"].([]any)[0].(map[string]any)["id"] != "same/model:v1" {
		t.Fatal(first)
	}
	member.expect(t, 200, "GET", "/api/market/models?limit=1&cursor="+first["next_cursor"].(string), nil)
	second, _ := p.member(t, "market-other")
	second.expect(t, 409, "GET", "/api/market/models?limit=1&cursor="+first["next_cursor"].(string), nil)
	filtered := member.expect(t, 200, "GET", "/api/market/models?features=tools,strict_json&only_online=true", nil)
	if filtered["total"] != float64(0) {
		t.Fatal("unknown capability admitted", filtered)
	}
	member.expect(t, 422, "GET", "/api/market/models?features=unknown_feature", nil)
	member.expect(t, 422, "GET", "/api/market/models?min_context=NaN", nil)
	detail := member.expect(t, 200, "GET", "/api/market/models/same%2Fmodel:v1", nil)
	if detail["model"].(map[string]any)["id"] != "same/model:v1" {
		t.Fatal(detail)
	}
	for _, path := range []string{"/api/market/models", "/api/market/suppliers", "/api/market/groups/group", "/api/market/models/same%2Fmodel:v1", "/api/market/channels", "/api/market/channels/channel"} {
		encoded, _ := json.Marshal(member.expect(t, 200, "GET", path, nil))
		if strings.Contains(string(encoded), "PRIVATE-") {
			t.Fatal("private config leaked", path, string(encoded))
		}
	}
	d.Governance = []market.ModelRule{{ModelID: "same/model:v1", Disabled: true}}
	if err := runtime.Install(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	member.expect(t, 404, "GET", "/api/market/models/same%2Fmodel:v1", nil)
	member.expect(t, 409, "GET", "/api/market/models?limit=1&cursor="+first["next_cursor"].(string), nil)
	runtime.Ready = func() error { return errors.New("PRIVATE-publication-detail") }
	for _, path := range []string{"/api/market/models", "/api/market/suppliers", "/api/market/groups/group", "/api/market/reports/unpublished"} {
		member.expect(t, 503, "GET", path, nil)
	}
}

func TestMarketPublicReportOmitsExecutionIdentityAddressAndQuota(t *testing.T) {
	id := "approved-report"
	scenario := "openai-responses.base@upstream_protocol"
	req := "openai-responses.base"
	assertion := req + "/a1"
	j := detection.Job{ID: "PRIVATE-task", OwnerID: "PRIVATE-owner", ReportID: &id, State: "finished", Complete: true, UpdatedAt: time.Now(), Target: detection.Target{DraftID: "PRIVATE-draft", GroupID: "group", Model: "PRIVATE-upstream", Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "offer", CredentialScope: "PRIVATE-credential"}}, PendingCleanup: []detection.Artifact{{NativeID: "PRIVATE-resource"}}, Plan: detection.Plan{ManifestVersion: "manifest-v1", Items: []detection.Planned{{ID: scenario, Layer: "upstream_protocol", Requirements: []string{req}, AssertionLabels: map[string]string{assertion: "accepted_native_message_shapes"}}}}, Results: []detection.Result{{ScenarioID: scenario, Layer: "upstream_protocol", State: "unsupported", Reason: "unsupported", Assertions: []detection.Assertion{{ID: assertion, State: "unsupported", Reason: "unsupported"}}, Attempts: []detection.Attempt{{EntryURL: "https://PRIVATE-origin.example", Path: "PRIVATE-path"}}, Quotas: []detection.QuotaEvidence{{Header: "PRIVATE-quota"}}}}}
	search := gatewaycontract.SearchSettings{Version: "v1", Method: "binary", Low: 1, High: 1024, HardMaximum: 1024, Precision: 1, PrecisionMode: "absolute", Rounds: 4}
	j.Parameters = detection.Parameters{Context: search, MaxOutput: search}
	m, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	out := publicReport(j, detection.Projection{Valid: true, Capabilities: []market.Capability{{ID: req + "/accepted_native_message_shapes", Layer: "upstream_protocol", State: "unsupported"}}}, m, nil)
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "PRIVATE-") || out.Validity != "current" || out.Scenarios[0].Assertions[0].State != "unsupported" {
		t.Fatal("redaction changed result or leaked private fields", string(encoded))
	}
	loadOpenAPI(t).assertSchema(t, "MarketPublicReport", encoded)
	out = publicReport(j, detection.Projection{}, m, nil)
	if out.Validity != "stale" || out.Scenarios[0].Current {
		t.Fatal("stale report advertised current", out)
	}
}
