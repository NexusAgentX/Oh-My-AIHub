package api

import (
	"context"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

// Explicit approved directory fixture for handler/observation tests. It creates
// no global model and issues no real upstream capability claim.
func marketModelFixture(t *testing.T, model string, records []groupchannel.Record) (*platform, *gateway.Engine) {
	t.Helper()
	const group = "00000000-0000-4000-8000-000000000091"
	const offering = "00000000-0000-4000-8000-000000000092"
	const quote = "00000000-0000-4000-8000-000000000093"
	repo := &browseMarketRepository{}
	runtime := &groupchannel.Runtime{Repository: repo}
	runtime.Rebuild = func(ctx context.Context) error {
		_, err := runtime.Wrap(groupBaseLoader{}).LoadCandidates(ctx)
		return err
	}
	var engine *gateway.Engine
	p := newPlatform(t, func(d *Dependencies) {
		engine = d.Gateway
		d.GroupChannels = &groupchannel.Service{Runtime: runtime, Repository: repo}
	})
	quoteID := quote
	directory := market.Directory{Suppliers: []market.Supplier{{ID: "supplier", Name: "Synthetic supplier", Kind: "relay", Enabled: true}}, Groups: []market.Group{{ID: group, SupplierID: "supplier", Name: "Synthetic group", Enabled: true, Revision: 1, ConnectionVersion: 1}}, Models: []market.GroupModel{{ID: offering, GroupID: group, ModelID: model, UpstreamModel: model, ModelType: "text", Enabled: true, Revision: 1, QuoteID: &quoteID}}, Quotes: []market.Quote{{ID: quote, GroupModelID: offering, Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}}, Addresses: []market.Address{{ID: "address", BaseURL: "https://synthetic.example"}}, GroupAddresses: []market.GroupAddress{{ID: "link", GroupID: group, AddressID: "address"}}}
	for _, r := range records {
		r.OwnerActive = true
		r.Status = channel.StatusListed
		r.GroupID = group
		r.Revision = 1
		r.CredentialRevision = 1
		r.AddressIDs = []string{"link"}
		repo.data.Channels = append(repo.data.Channels, r)
		repo.data.Probes = append(repo.data.Probes, groupchannel.Probe{ChannelID: r.ID, GroupModelID: offering, Binding: groupchannel.ProbeBinding(r, directory.Groups[0], directory.Models[0]), Tests: map[channel.Format]channel.FormatTest{channel.FormatOpenAIResponses: {OK: true}}})
	}
	if err := runtime.Install(context.Background(), directory); err != nil {
		t.Fatal(err)
	}
	return p, engine
}
