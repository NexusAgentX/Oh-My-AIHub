package c2c

import (
	"context"
	"strings"
	"time"
)

// TradeSignal is sufficient for reminders and deliberately has no payment
// methods, sealed credentials, amounts or statement text. Polling the inbox
// does not decrypt payment details.
type TradeSignal struct {
	ID               string
	Status           TradeStatus
	ViewerRole       string
	PaymentDeadline  time.Time
	CreatedAt        time.Time
	MissingStatement bool
}

func (s *Service) ListMySignals(ctx context.Context, owner, rawCursor string, limit int) (Page[TradeSignal], error) {
	if owner == "" || limit < 1 || limit > 100 {
		return Page[TradeSignal]{}, ErrInvalidInput
	}
	after, err := decodeCursor(rawCursor, false)
	if err != nil {
		return Page[TradeSignal]{}, err
	}
	rows, err := s.store.ListTrades(ctx, TradeFilter{UserID: owner, Active: true, After: after, Limit: limit + 1})
	if err != nil {
		return Page[TradeSignal]{}, err
	}
	rows, next := paginate(rows, limit, func(trade Trade) Cursor { return Cursor{Time: trade.CreatedAt, ID: trade.ID} })
	page := Page[TradeSignal]{Next: next, Items: make([]TradeSignal, 0, len(rows))}
	for _, trade := range rows {
		role, statement := "", trade.BuyerStatement
		switch owner {
		case trade.Buyer.ID:
			role = "buyer"
		case trade.Seller.ID:
			role = "seller"
			statement = trade.SellerStatement
		default:
			return Page[TradeSignal]{}, ErrForbidden
		}
		page.Items = append(page.Items, TradeSignal{ID: trade.ID, Status: trade.Status, ViewerRole: role, PaymentDeadline: trade.PaymentDeadline, CreatedAt: trade.CreatedAt, MissingStatement: statement == nil || strings.TrimSpace(*statement) == ""})
	}
	return page, nil
}
