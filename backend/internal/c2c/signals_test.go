package c2c

import (
	"context"
	"testing"
	"time"
)

type signalStore struct {
	Store
	rows   []Trade
	filter TradeFilter
}

func (s *signalStore) ListTrades(_ context.Context, filter TradeFilter) ([]Trade, error) {
	s.filter = filter
	return s.rows, nil
}
func TestInboxSignalsNeedNoPaymentKeyringAndStayOwnerScoped(t *testing.T) {
	store := &signalStore{rows: []Trade{{ID: "trade", Buyer: Party{ID: "owner"}, Seller: Party{ID: "seller"}, Status: TradeDisputed, Methods: EncryptedValue{Ciphertext: []byte("unreadable")}, CreatedAt: time.Now()}}}
	service := NewService(store, nil)
	page, err := service.ListMySignals(context.Background(), "owner", "", 100)
	if err != nil || len(page.Items) != 1 || !page.Items[0].MissingStatement || page.Items[0].ViewerRole != "buyer" || store.filter.UserID != "owner" || !store.filter.Active {
		t.Fatalf("signals=%+v, filter=%+v, err=%v", page, store.filter, err)
	}
	if _, err = service.ListMySignals(context.Background(), "other", "", 100); err != ErrForbidden {
		t.Fatal("foreign metadata visible", err)
	}
}
