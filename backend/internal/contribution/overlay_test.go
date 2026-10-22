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
func TestBundlePrivateViewHasNoInventedSupplierModel(t *testing.T) {
	draft := starterDraft()
	directory, err := Overlay(draft, market.Directory{})
	if err != nil || len(directory.Models) != 1 || directory.Suppliers[0].ContributorID != "member" {
		t.Fatalf("bundle=%+v err=%v", directory, err)
	}
	if draft.Changes.Models[0].Value.CapabilityDigest != "" || draft.Changes.Models[0].Value.DisplayName != "" {
		t.Fatal("private preparation mutated the saved proposal")
	}
}
func TestGroupHeaderChangesDoNotInvalidateModelEvidenceButEmergencyDisablePublishesPrivateView(t *testing.T) {
	directory, err := Overlay(starterDraft(), market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	group := directory.Groups[0]
	ua := "synthetic-agent"
	group.Defaults.UserAgent = &ua
	change := Draft{OwnerID: "member", Changes: market.Changes{Groups: []market.Change[market.Group]{{BaseRevision: 1, Value: group}}}}
	next, err := Overlay(change, directory)
	// A group channel's probe is the same probe whatever UA the group sends, so
	// the model's evidence survives the edit.
	if err != nil || next.Models[0].CapabilityDigest != directory.Models[0].CapabilityDigest {
		t.Fatalf("group header edit invalidated model evidence: %+v %v", next, err)
	}
	group.Enabled = false
	change.Changes.Groups[0].Value = group
	disabled, err := Overlay(change, directory)
	if err != nil || len(disabled.Groups) != 1 || disabled.Groups[0].Enabled {
		t.Fatalf("emergency disable rejected: %+v %v", disabled, err)
	}
}
func TestSupplierDeletionBlockedByDisabledGroupsAndStaleEdits(t *testing.T) {
	directory, err := Overlay(starterDraft(), market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	directory.Groups[0].Enabled = false
	supplier := directory.Suppliers[0]
	now := time.Now()
	supplier.DeletedAt = &now
	_, err = Overlay(Draft{Changes: market.Changes{Suppliers: []market.Change[market.Supplier]{{BaseRevision: 1, Value: supplier}}}}, directory)
	if !errors.Is(err, market.ErrHasGroups) {
		t.Fatalf("disabled group bypassed supplier deletion guard: %v", err)
	}
	group := directory.Groups[0]
	group.Name = "other edit"
	_, err = Overlay(Draft{Changes: market.Changes{Groups: []market.Change[market.Group]{{BaseRevision: 2, Value: group}}}}, directory)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale base accepted: %v", err)
	}
}
func TestQuoteAndAddressSetsCannotBypassTheirPublishedParentRevision(t *testing.T) {
	directory, err := Overlay(starterDraft(), market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	for _, changes := range []market.Changes{{Quotes: []market.Quote{directory.Quotes[0]}}, {GroupAddresses: []market.GroupAddressSet{{GroupID: "group", Links: directory.GroupAddresses}}}} {
		if _, err := Overlay(Draft{Changes: changes}, directory); !errors.Is(err, ErrInvalid) {
			t.Fatalf("publication bypassed parent base: %v", err)
		}
	}
}
