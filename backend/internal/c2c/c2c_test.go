package c2c

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestFiatAmountFenRoundsUpWithoutFloatingPoint(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		quantity money.Amount
		price    int64
		want     int64
	}{
		{quantity: money.FromNano(1), price: 1, want: 1},
		{quantity: money.FromNano(money.Scale), price: 100, want: 100},
		{quantity: money.FromNano(money.Scale + 1), price: 100, want: 101},
		{quantity: money.FromNano(250_000_000), price: 101, want: 26},
	} {
		got, err := FiatAmountFen(test.quantity, test.price)
		if err != nil || got != test.want {
			t.Fatalf("FiatAmountFen(%d, %d) = %d, %v; want %d", test.quantity, test.price, got, err, test.want)
		}
	}
	if _, err := FiatAmountFen(0, 100); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero quantity error = %v", err)
	}
}

func TestKeyringUsesRecordAndPurposeSeparatedAAD(t *testing.T) {
	t.Parallel()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	keyring, err := ParseKeyring("v1="+key, "v1")
	if err != nil {
		t.Fatalf("parse keyring: %v", err)
	}
	encrypted, err := keyring.Encrypt("record-1", "payment_method", []byte("private contact"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plaintext, err := keyring.Decrypt("record-1", "payment_method", encrypted)
	if err != nil || string(plaintext) != "private contact" {
		t.Fatalf("decrypt = %q, %v", plaintext, err)
	}
	if _, err := keyring.Decrypt("record-2", "payment_method", encrypted); err == nil {
		t.Fatal("record AAD substitution unexpectedly decrypted")
	}
	if _, err := keyring.Decrypt("record-1", "payment_reference", encrypted); err == nil {
		t.Fatal("purpose AAD substitution unexpectedly decrypted")
	}
}

type resolveRecordingStore struct {
	Store
	action ResolutionAction
	calls  int
}

func (s *resolveRecordingStore) ResolveDispute(_ context.Context, command Command, tradeID string, action ResolutionAction, reason string, _ time.Time) (Trade, error) {
	s.calls++
	s.action = action
	if command.Operation != "c2c.dispute.resolve" || tradeID != "trade-id" || reason != "fraud review" {
		return Trade{}, errors.New("unexpected resolve command")
	}
	return Trade{ID: tradeID}, nil
}

func TestResolveDisputeAcceptsRestrictActionsForAdminsOnly(t *testing.T) {
	t.Parallel()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	keyring, err := ParseKeyring("v1="+key, "v1")
	if err != nil {
		t.Fatal(err)
	}
	store := &resolveRecordingStore{}
	service, err := NewService(store, keyring)
	if err != nil {
		t.Fatal(err)
	}
	admin := identity.Account{ID: "admin-id", Status: identity.StatusActive, IsAdmin: true}
	for _, action := range []ResolutionAction{ResolutionRestrictBuyer, ResolutionRestrictSeller} {
		if _, err := service.ResolveDispute(context.Background(), admin, "key-"+string(action), " trade-id ", action, " fraud review "); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if store.action != action {
			t.Fatalf("store action = %s, want %s", store.action, action)
		}
	}
	member := admin
	member.IsAdmin = false
	for name, call := range map[string]func() error{
		"member": func() error {
			_, err := service.ResolveDispute(context.Background(), member, "key", "trade-id", ResolutionRestrictBuyer, "fraud review")
			return err
		},
		"blank reason": func() error {
			_, err := service.ResolveDispute(context.Background(), admin, "key", "trade-id", ResolutionRestrictSeller, "  ")
			return err
		},
		"unknown action": func() error {
			_, err := service.ResolveDispute(context.Background(), admin, "key", "trade-id", ResolutionAction("restrict_both"), "fraud review")
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s error = %v, want invalid input", name, err)
		}
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want 2", store.calls)
	}
}
