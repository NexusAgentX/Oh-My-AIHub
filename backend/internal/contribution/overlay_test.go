package contribution

import (
	"errors"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func starterDraft() Draft {
	return Draft{ID: "draft", OwnerID: "member", Kind: "supplier", TargetID: "supplier", Changes: market.Changes{
		Suppliers:      []market.Change[market.Supplier]{{Value: market.Supplier{ID: "supplier", Name: "Synthetic supplier", Kind: "relay", WebsiteURL: "https://synthetic.example", Enabled: true}}},
		Groups:         []market.Change[market.Group]{{Value: market.Group{ID: "group", SupplierID: "supplier", Name: "Synthetic group", Enabled: true}}},
		Addresses:      []market.Change[market.Address]{{Value: market.Address{ID: "address", SupplierID: "supplier", BaseURL: "https://synthetic.example"}}},
		GroupAddresses: []market.GroupAddressSet{{GroupID: "group", Links: []market.GroupAddress{{ID: "link", GroupID: "group", SupplierID: "supplier", AddressID: "address"}}}},
		Models:         []market.Change[market.GroupModel]{{Value: market.GroupModel{ID: "model", GroupID: "group", ModelID: "synthetic/model:v1", ModelType: "text", Enabled: true}}},
		Quotes:         []market.Quote{{ID: "quote", GroupModelID: "model", Currency: "CNY", BillingUnit: "per_million_tokens"}},
	}}
}
func TestBundlePrivateViewHasIndependentTargetsAndNoInventedSupplierModel(t *testing.T) {
	draft := starterDraft()
	directory, targets, err := Overlay(draft, market.Directory{})
	if err != nil || len(targets) != 2 || len(directory.Models) != 1 || directory.Suppliers[0].ContributorID != "member" {
		t.Fatalf("bundle=%+v targets=%+v err=%v", directory, targets, err)
	}
	for _, target := range targets {
		if target.Binding.TargetKind == "supplier" || target.Binding.CapabilityDigest == "" {
			t.Fatal("metadata supplier invented an inference target")
		}
	}
	if draft.Changes.Groups[0].Value.CapabilityDigest != "" || draft.Changes.Models[0].Value.DisplayName != "" {
		t.Fatal("private preparation mutated the saved proposal")
	}
}
func TestGroupHeaderChangesChildrenButEmergencyDisableRequiresNoPaidTests(t *testing.T) {
	directory, _, err := Overlay(starterDraft(), market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	group := directory.Groups[0]
	ua := "synthetic-agent"
	group.Defaults.UserAgent = &ua
	change := Draft{OwnerID: "member", Changes: market.Changes{Groups: []market.Change[market.Group]{{BaseRevision: 1, Value: group}}}}
	next, targets, err := Overlay(change, directory)
	if err != nil || len(targets) != 2 || next.Models[0].CapabilityDigest == directory.Models[0].CapabilityDigest {
		t.Fatalf("parent dependency not reflected: %+v %v", targets, err)
	}
	group.Enabled = false
	change.Changes.Groups[0].Value = group
	_, targets, err = Overlay(change, directory)
	if err != nil || len(targets) != 0 {
		t.Fatalf("emergency disable attempted paid detection: %+v %v", targets, err)
	}
}
func TestSupplierDeletionBlockedByDisabledGroupsAndStaleEdits(t *testing.T) {
	directory, _, err := Overlay(starterDraft(), market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	directory.Groups[0].Enabled = false
	supplier := directory.Suppliers[0]
	now := time.Now()
	supplier.DeletedAt = &now
	_, _, err = Overlay(Draft{Changes: market.Changes{Suppliers: []market.Change[market.Supplier]{{BaseRevision: 1, Value: supplier}}}}, directory)
	if !errors.Is(err, market.ErrHasGroups) {
		t.Fatalf("disabled group bypassed supplier deletion guard: %v", err)
	}
	group := directory.Groups[0]
	group.Name = "other edit"
	_, _, err = Overlay(Draft{Changes: market.Changes{Groups: []market.Change[market.Group]{{BaseRevision: 2, Value: group}}}}, directory)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale base accepted: %v", err)
	}
}
func TestQuoteAndAddressSetsCannotBypassTheirPublishedParentRevision(t *testing.T) {
	directory, _, err := Overlay(starterDraft(), market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	for _, changes := range []market.Changes{{Quotes: []market.Quote{directory.Quotes[0]}}, {GroupAddresses: []market.GroupAddressSet{{GroupID: "group", Links: directory.GroupAddresses}}}} {
		if _, _, err := Overlay(Draft{Changes: changes}, directory); !errors.Is(err, ErrInvalid) {
			t.Fatalf("publication bypassed parent base: %v", err)
		}
	}
}
