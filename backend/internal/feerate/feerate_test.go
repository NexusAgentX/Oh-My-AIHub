package feerate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type stubStore struct {
	versions []Version
	appended []Version
}

func (s *stubStore) ListFeeRates(_ context.Context, limit int) ([]Version, error) {
	if limit < len(s.versions) {
		return s.versions[:limit], nil
	}
	return s.versions, nil
}

func (s *stubStore) AppendFeeRate(_ context.Context, actorID string, expectedVersion int64, rate money.Amount, reason string) (Version, error) {
	created := Version{Version: expectedVersion + 1, Rate: rate, CreatedByID: actorID, Reason: reason}
	s.appended = append(s.appended, created)
	return created, nil
}

func TestValidateBoundsPrecisionAndReason(t *testing.T) {
	cases := []struct {
		name    string
		version int64
		rate    money.Amount
		reason  string
		valid   bool
	}{
		{"zero", 1, 0, "r", true},
		{"hundred percent", 1, MaxRate, "r", true},
		{"one nano", 1, 1, "r", true},
		{"negative", 1, -1, "r", false},
		{"above hundred percent", 1, MaxRate + 1, "r", false},
		{"missing version", 0, 1, "r", false},
		{"missing reason", 1, 1, "", false},
		{"long reason", 1, 1, strings.Repeat("费", MaxReasonRunes+1), false},
		{"max reason", 1, 1, strings.Repeat("费", MaxReasonRunes), true},
	}
	for _, testCase := range cases {
		err := Validate(testCase.version, testCase.rate, testCase.reason)
		if testCase.valid && err != nil {
			t.Errorf("%s: unexpected error %v", testCase.name, err)
		}
		if !testCase.valid && !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: error = %v, want ErrInvalidInput", testCase.name, err)
		}
	}
}

func TestServiceRequiresAdministratorAndTrimsReason(t *testing.T) {
	store := &stubStore{versions: []Version{{Version: 1, Rate: 1_000_000}}}
	service := NewService(store)
	member := identity.Account{ID: "member"}
	if _, err := service.List(context.Background(), member, 0); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("member list error = %v", err)
	}
	if _, err := service.Set(context.Background(), member, 1, 2_000_000, "raise"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("member set error = %v", err)
	}
	if len(store.appended) != 0 {
		t.Fatal("member reached the store")
	}

	admin := identity.Account{ID: "admin", IsAdmin: true}
	versions, err := service.List(context.Background(), admin, 0)
	if err != nil || len(versions) != 1 {
		t.Fatalf("admin list = %v, %v", versions, err)
	}
	if _, err := service.List(context.Background(), admin, MaxListLimit+1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized limit error = %v", err)
	}
	created, err := service.Set(context.Background(), admin, 1, 2_000_000, "  raise  ")
	if err != nil {
		t.Fatal(err)
	}
	if created.Reason != "raise" || created.CreatedByID != "admin" {
		t.Fatalf("created = %+v", created)
	}
	if _, err := service.Set(context.Background(), admin, 1, 2_000_000, "   "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank reason error = %v", err)
	}
}
