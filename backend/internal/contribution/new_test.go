package contribution

import (
	"context"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type memoryDirectory struct{ repo *memoryReview }

func (m memoryDirectory) Snapshot(context.Context) (market.Directory, error) {
	m.repo.mu.Lock()
	defer m.repo.mu.Unlock()
	return clone(m.repo.live), nil
}
func (memoryDirectory) Apply(context.Context, string, market.Changes) error { return ErrInvalid }
func TestNewStarterAllocatesAllObjectIDsBeforeAnyDetection(t *testing.T) {
	s, repo, _ := metadataService(t)
	s.Live = memoryDirectory{repo}
	draft, err := s.New(context.Background(), Actor{ID: "member"}, NewInput{Kind: "supplier", Starter: true})
	if err != nil {
		t.Fatal(err)
	}
	c := draft.Changes
	if len(c.Suppliers) != 1 || len(c.Groups) != 1 || len(c.Models) != 1 || len(c.Addresses) != 1 || len(c.Quotes) != 1 {
		t.Fatal("starter bundle incomplete")
	}
	seen := map[string]bool{}
	for _, id := range []string{draft.ID, c.Suppliers[0].Value.ID, c.Groups[0].Value.ID, c.Models[0].Value.ID, c.Addresses[0].Value.ID, c.GroupAddresses[0].Links[0].ID, c.Quotes[0].ID} {
		if len(id) != 36 || seen[id] {
			t.Fatal("draft identities not unique UUIDs")
		}
		seen[id] = true
	}
	if c.Groups[0].Value.SupplierID != c.Suppliers[0].Value.ID || c.Models[0].Value.GroupID != c.Groups[0].Value.ID || c.Quotes[0].GroupModelID != c.Models[0].Value.ID || len(repo.live.Suppliers) != 0 {
		t.Fatal("unapproved parent escaped into live data")
	}
}
func TestEmergencyDisableDoesNotDependOnCurrentUpstreamReachability(t *testing.T) {
	s, repo, _ := metadataService(t)
	live, _, err := Overlay(starterDraft(), market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	repo.live = live
	s.Live = memoryDirectory{repo}
	draft, err := s.New(context.Background(), Actor{ID: "member"}, NewInput{Kind: "group", TargetID: "group", Action: "disable"})
	if err != nil {
		t.Fatal(err)
	}
	// No outbound policy or paid detector is needed for unchanged addresses
	// while taking an existing group out of service.
	submitted, err := s.Submit(context.Background(), Actor{ID: "member"}, draft.ID, draft.Revision)
	if err != nil || submitted.State != "submitted" {
		t.Fatalf("emergency stop was blocked by upstream access: %+v %v", submitted, err)
	}
}

func TestReusingApprovedAddressDoesNotCopyOrRepriceIt(t *testing.T) {
	s, repo, _ := metadataService(t)
	live, _, err := Overlay(starterDraft(), market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	repo.live = live
	s.Live = memoryDirectory{repo}
	actor := Actor{ID: "member"}
	d, err := s.New(context.Background(), actor, NewInput{Kind: "group", ParentID: live.Suppliers[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.ReuseAddress(context.Background(), actor, d.ID, d.Revision, d.TargetID, live.Addresses[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Changes.Addresses) != 0 || len(d.Changes.GroupAddresses) != 1 || len(d.Changes.GroupAddresses[0].Links) != 1 || d.Changes.GroupAddresses[0].Links[0].AddressID != live.Addresses[0].ID {
		t.Fatal("shared address was copied or placeholder retained")
	}
	if _, err = s.ReuseAddress(context.Background(), Actor{ID: "other"}, d.ID, d.Revision, d.TargetID, live.Addresses[0].ID); err != ErrNotFound {
		t.Fatal("foreign draft changed", err)
	}
	foreign := live.Addresses[0]
	foreign.ID = "foreign-address"
	foreign.SupplierID = "other-supplier"
	repo.live.Addresses = append(repo.live.Addresses, foreign)
	if _, err = s.ReuseAddress(context.Background(), actor, d.ID, d.Revision, d.TargetID, foreign.ID); err != ErrNotFound {
		t.Fatal("cross-supplier address accepted", err)
	}
	if _, err = s.ReuseAddress(context.Background(), actor, d.ID, d.Revision, d.TargetID, live.Addresses[0].ID); err != ErrConflict {
		t.Fatal("duplicate address reference accepted", err)
	}
}
