package api

import (
	"context"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"testing"
)

// The common handler fixture reads only approved group IDs, as the server does.
// It publishes no detection report and makes no provider capability claim.
type platformModelReader struct{ runtime *groupchannel.Runtime }

func (r platformModelReader) List(ctx context.Context, disabled bool) ([]catalog.Model, error) {
	rows, err := r.runtime.Wrap(groupBaseLoader{}).LoadModels(ctx)
	if err != nil || disabled {
		return rows, err
	}
	out := []catalog.Model{}
	for _, row := range rows {
		if row.Enabled {
			out = append(out, row)
		}
	}
	return out, nil
}
func (r platformModelReader) Get(ctx context.Context, id string) (catalog.Model, error) {
	rows, err := r.List(ctx, true)
	if err != nil {
		return catalog.Model{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return catalog.Model{}, catalog.ErrNotFound
}

func seedModel(t *testing.T, p *platform, id string) {
	t.Helper()
	state, err := p.groups.Runtime.Current()
	if err != nil {
		t.Fatal(err)
	}
	d := reviewClone(state.Directory)
	const supplier = "00000000-0000-4000-8000-000000000061"
	const group = "00000000-0000-4000-8000-000000000062"
	const address = "00000000-0000-4000-8000-000000000063"
	const link = "00000000-0000-4000-8000-000000000064"
	if len(d.Groups) == 0 {
		d.Suppliers = []market.Supplier{{ID: supplier, Name: "Synthetic supplier", Kind: "relay", Enabled: true}}
		d.Groups = []market.Group{{ID: group, SupplierID: supplier, Name: "Synthetic group", Enabled: true, Revision: 1, ConnectionVersion: 1}}
		d.Addresses = []market.Address{{ID: address, SupplierID: supplier, BaseURL: "https://relay.example"}}
		d.GroupAddresses = []market.GroupAddress{{ID: link, GroupID: group, AddressID: address, SupplierID: supplier}}
	}
	offeringID, err := ids.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	d.Models = append(d.Models, market.GroupModel{ID: offeringID, ModelID: id, UpstreamModel: id, GroupID: group, ModelType: "text", Enabled: true, Revision: 1})
	if err := p.groups.Runtime.Install(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}
