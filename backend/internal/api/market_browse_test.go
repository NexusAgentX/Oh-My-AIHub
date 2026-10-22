package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type browseMarketRepository struct {
	groupchannel.Repository
	data groupchannel.Data
}

func TestMarketRoutingPreferencesCannotRestoreRetiredGlobalModels(t *testing.T) {
	p, _ := marketModelFixture(t, "approved-model", nil)
	p.store.models["retired-model"] = catalog.Model{ID: "retired-model", Enabled: true}
	member, _ := p.member(t, "routing-market-only")
	key := member.expect(t, 201, "POST", "/api/keys", map[string]any{"name": "routing-only"})["key"].(map[string]any)["id"].(string)
	for _, prefix := range []string{"/api/routing/", "/api/keys/" + key + "/routing/"} {
		member.expect(t, 200, "PUT", prefix+"approved-model", map[string]any{"mode": "fastest"})
		member.expect(t, 404, "PUT", prefix+"retired-model", map[string]any{"mode": "fastest"})
		member.expect(t, 404, "PUT", prefix+"unknown-model", map[string]any{"mode": "fastest"})
	}
	if count := len(p.store.models); count != 1 {
		t.Fatal("fixture must retain one historical global model", count)
	}
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
	if err := runtime.Install(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	member.expect(t, 401, "GET", "/api/market/models", nil, withoutCookie)
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
	member.expect(t, 200, "PUT", "/api/routing/same%2Fmodel:v1", map[string]any{"mode": "fastest", "order": []string{}, "excluded": []string{}})
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
	member.expect(t, 404, "PUT", "/api/routing/same%2Fmodel:v1", map[string]any{"mode": "fastest", "order": []string{}, "excluded": []string{}})
	member.expect(t, 409, "GET", "/api/market/models?limit=1&cursor="+first["next_cursor"].(string), nil)
	runtime.Ready = func() error { return errors.New("PRIVATE-publication-detail") }
	member.expect(t, 503, "PUT", "/api/routing/same%2Fmodel:v1", map[string]any{"mode": "fastest", "order": []string{}, "excluded": []string{}})
	for _, path := range []string{"/api/market/models", "/api/market/suppliers", "/api/market/groups/group"} {
		member.expect(t, 503, "GET", path, nil)
	}
}
