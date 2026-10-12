package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/supplier"
)

func TestSupplierAddressesPersistAtomically(t *testing.T) {
	pool, store := isolatedDatabase(t)
	_, admin, _ := accounts(t, store)
	ctx := context.Background()
	policy, _ := channel.NewOutboundPolicy(nil, nil)
	service := supplier.Service{Store: store.Suppliers, Outbound: policy}
	first, err := service.Save(ctx, admin.ID, supplier.Supplier{Name: "First", Kind: "relay", WebsiteURL: "https://first.example", Visible: true, Endpoints: []supplier.Endpoint{{BaseURL: "https://api.first.example/v1"}, {BaseURL: "https://backup.first.example"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Save(ctx, admin.ID, supplier.Supplier{Name: "Second", Kind: "official", WebsiteURL: "https://second.example", Visible: true, Endpoints: []supplier.Endpoint{{BaseURL: "https://api.second.example"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	first.Name = "Changed"
	first.Endpoints = []supplier.Endpoint{{BaseURL: "https://new.first.example"}, {BaseURL: "https://api.second.example/v1"}}
	if _, err = service.Save(ctx, admin.ID, first, false); !errors.Is(err, supplier.ErrConflict) {
		t.Fatalf("conflict = %v", err)
	}
	values, err := store.Suppliers.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, value := range values {
		if value.ID == first.ID {
			found = true
			if value.Name != "First" || len(value.Endpoints) != 2 {
				t.Fatal("conflicting update was partially committed")
			}
		}
	}
	if !found {
		t.Fatal("supplier disappeared")
	}
	second.Visible = false
	second.Endpoints = nil
	if _, err = service.Save(ctx, admin.ID, second, false); err != nil {
		t.Fatal(err)
	}
	first.Endpoints = []supplier.Endpoint{{BaseURL: "https://api.second.example"}}
	if _, err = service.Save(ctx, admin.ID, first, false); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE action='supplier.saved'").Scan(&count); err != nil || count != 4 {
		t.Fatalf("audit count %d: %v", count, err)
	}
}
