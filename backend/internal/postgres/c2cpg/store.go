// Package c2cpg is the PostgreSQL implementation of c2c.Store. SQL lives in
// queries.sql; the generated code is committed beside it.
//
// Every command (publish, take, cancel, pay, release, dispute, expire) opens
// its own pgx.Tx and binds the ledger to the same transaction with
// ledgerpg.NewTx, so the C2C rows and the ledger holds/transfers commit or roll
// back together (ADR-0020). The transaction uses the pool default isolation
// level and the lock order is documented on each command in commands.go.
package c2cpg

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: New(pool)}
}

var _ c2c.Store = (*Store)(nil)

// mapError converts PostgreSQL and ledger errors to C2C errors.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ledger.ErrNotFound) {
		return c2c.ErrNotFound
	}
	if errors.Is(err, ledger.ErrConflict) || errors.Is(err, ledger.ErrHoldClosed) || errors.Is(err, ledger.ErrHoldAmountExceeded) {
		return c2c.ErrConflict
	}
	if errors.Is(err, ledger.ErrInvalidInput) || errors.Is(err, ledger.ErrAmountOverflow) {
		return c2c.ErrInvalidInput
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23514", "40001", "40P01":
			return c2c.ErrConflict
		case "22P02", "22003":
			return c2c.ErrInvalidInput
		}
	}
	return err
}

// ---------------------------------------------------------------------------
// Row mapping
// ---------------------------------------------------------------------------

// toOrder maps the order projection. The order queries select the same
// columns, so callers convert their row type to GetOrderRow.
func toOrder(row GetOrderRow) c2c.Order {
	return c2c.Order{
		ID: row.ID, OwnerAccountID: row.OwnerAccountID, OwnerDisplayName: row.OwnerDisplayName,
		UnitPriceFen: row.UnitPriceFen,
		Total:        row.TotalNano, Available: row.AvailableNano, Allocated: row.AllocatedNano,
		Settled: row.SettledNano, Closed: row.ClosedNano, Minimum: row.MinimumNano, Maximum: row.MaximumNano,
		Status: c2c.OrderStatus(row.Status), ParentHoldID: row.ParentHoldID,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, CancelledAt: row.CancelledAt,
		Takeable: row.Takeable,
	}
}

// toTrade maps the trade projection; callers convert their row type to GetTradeRow.
func toTrade(row GetTradeRow) c2c.Trade {
	return c2c.Trade{
		ID: row.ID, OrderID: row.OrderID,
		BuyerAccountID: row.BuyerAccountID, BuyerDisplayName: row.BuyerDisplayName,
		SellerAccountID: row.SellerAccountID, SellerDisplayName: row.SellerDisplayName,
		BuyerCreditFrozen: row.BuyerCreditFrozen, SellerCreditFrozen: row.SellerCreditFrozen,
		Quantity: row.QuantityNano, UnitPriceFen: row.UnitPriceFen, FiatAmountFen: row.FiatAmountFen,
		Status: c2c.TradeStatus(row.Status), HoldID: row.HoldID,
		PaymentReferenceChars: int(row.PaymentReferenceChars),
		PaymentReferenceData: c2c.EncryptedValue{
			KeyID: row.PaymentReferenceKeyID, Nonce: row.PaymentReferenceNonce,
			Ciphertext: row.PaymentReferenceCiphertext,
		},
		PaymentReferenceGone: row.PaymentReferenceDeletedAt,
		PaymentDeadline:      row.PaymentDeadline, ReviewDueAt: row.ReviewDueAt,
		LedgerTransactionID: row.LedgerTransactionID,
		CreatedAt:           row.CreatedAt, UpdatedAt: row.UpdatedAt, PaidAt: row.PaidAt, ResolvedAt: row.ResolvedAt,
	}
}

func toPaymentMethod(row C2cPaymentMethod) c2c.PaymentMethod {
	return c2c.PaymentMethod{
		ID: row.ID, OrderID: row.OrderID, Type: c2c.PaymentMethodType(row.MethodType),
		Position: int(row.Position), QRAvailable: row.QrAvailable,
		Private:   c2c.EncryptedValue{KeyID: row.KeyID, Nonce: row.Nonce, Ciphertext: row.Ciphertext},
		CreatedAt: row.CreatedAt,
	}
}

func cleanOrderSnapshot(order c2c.Order) c2c.Order {
	order.PaymentMethods = nil
	return order
}

// cleanTradeSnapshot removes everything private from a trade before it is
// stored as a command snapshot or returned in a list.
func cleanTradeSnapshot(trade c2c.Trade) c2c.Trade {
	trade.SelectedPaymentMethod = nil
	trade.Statements = nil
	trade.Events = nil
	trade.PaymentReference = ""
	trade.PaymentReferenceData = c2c.EncryptedValue{}
	return trade
}

// ---------------------------------------------------------------------------
// Loading (shared by reads and commands; q is bound to the pool or a transaction)
// ---------------------------------------------------------------------------

func loadOrder(ctx context.Context, q *Queries, orderID string, lock bool) (c2c.Order, error) {
	var row GetOrderRow
	var err error
	if lock {
		var locked GetOrderForUpdateRow
		locked, err = q.GetOrderForUpdate(ctx, orderID)
		row = GetOrderRow(locked)
	} else {
		row, err = q.GetOrder(ctx, orderID)
	}
	if err != nil {
		return c2c.Order{}, mapError(err)
	}
	order := toOrder(row)
	methods, err := loadPaymentMethods(ctx, q, order.ID)
	if err != nil {
		return c2c.Order{}, err
	}
	order.PaymentMethods = methods
	order.PaymentTypes = make([]c2c.PaymentMethodType, 0, len(methods))
	for _, method := range methods {
		order.PaymentTypes = append(order.PaymentTypes, method.Type)
	}
	return order, nil
}

func loadPaymentMethods(ctx context.Context, q *Queries, orderID string) ([]c2c.PaymentMethod, error) {
	rows, err := q.ListPaymentMethods(ctx, orderID)
	if err != nil {
		return nil, mapError(err)
	}
	methods := make([]c2c.PaymentMethod, 0, c2c.MaximumMethods)
	for _, row := range rows {
		methods = append(methods, toPaymentMethod(row))
	}
	return methods, nil
}

func loadTrade(ctx context.Context, q *Queries, tradeID string, lock bool, details bool) (c2c.Trade, error) {
	var row GetTradeRow
	var err error
	if lock {
		var locked GetTradeForUpdateRow
		locked, err = q.GetTradeForUpdate(ctx, tradeID)
		row = GetTradeRow(locked)
	} else {
		row, err = q.GetTrade(ctx, tradeID)
	}
	if err != nil {
		return c2c.Trade{}, mapError(err)
	}
	trade := toTrade(row)
	if !details {
		return trade, nil
	}
	selected, err := q.GetSelectedPaymentMethod(ctx, trade.ID)
	if err != nil {
		return c2c.Trade{}, mapError(err)
	}
	method := toPaymentMethod(selected)
	trade.SelectedPaymentMethod = &method
	if trade.Statements, err = loadStatements(ctx, q, trade.ID); err != nil {
		return c2c.Trade{}, err
	}
	if trade.Events, err = loadEvents(ctx, q, trade.OrderID, trade.ID); err != nil {
		return c2c.Trade{}, err
	}
	return trade, nil
}

func loadStatements(ctx context.Context, q *Queries, tradeID string) ([]c2c.Statement, error) {
	rows, err := q.ListStatements(ctx, tradeID)
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]c2c.Statement, 0, 2)
	for _, row := range rows {
		items = append(items, c2c.Statement{
			ID: row.ID, TradeID: row.TradeID, ActorAccountID: row.ActorAccountID,
			ActorDisplayName: row.ActorDisplayName, CharacterCount: int(row.CharacterCount),
			Encrypted: c2c.EncryptedValue{KeyID: row.KeyID, Nonce: row.Nonce, Ciphertext: row.Ciphertext},
			CreatedAt: row.CreatedAt, DeletedAt: row.DeletedAt,
		})
	}
	return items, nil
}

func loadEvents(ctx context.Context, q *Queries, orderID, tradeID string) ([]c2c.Event, error) {
	var trade *string
	if tradeID != "" {
		trade = &tradeID
	}
	rows, err := q.ListEvents(ctx, ListEventsParams{OrderID: orderID, TradeID: trade})
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]c2c.Event, 0, len(rows))
	for _, row := range rows {
		items = append(items, c2c.Event{
			ID: row.ID, OrderID: row.OrderID, TradeID: row.TradeID, ActorAccountID: row.ActorAccountID,
			Action: row.Action, Reason: row.Reason, LedgerTransactionID: row.LedgerTransactionID,
			HoldBusinessID: row.HoldBusinessID, CreatedAt: row.CreatedAt,
		})
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

func (s *Store) Market(ctx context.Context) (c2c.Market, error) {
	market := c2c.Market{GuidancePriceFen: 100}
	sells, err := s.q.ListMarketSellOrders(ctx)
	if err != nil {
		return c2c.Market{}, mapError(err)
	}
	latest, err := s.q.GetLatestTradePrice(ctx)
	switch {
	case err == nil:
		market.LatestPriceFen = &latest
	case !errors.Is(err, pgx.ErrNoRows):
		return c2c.Market{}, mapError(err)
	}
	market.SellOrders, err = s.withPaymentTypes(ctx, len(sells), func(i int) GetOrderRow { return GetOrderRow(sells[i]) })
	if err != nil {
		return c2c.Market{}, err
	}
	// The list is ordered best (lowest) price first.
	if len(market.SellOrders) > 0 {
		price := market.SellOrders[0].UnitPriceFen
		market.BestAskFen = &price
	}
	return market, nil
}

// withPaymentTypes maps n market rows and attaches the accepted payment types.
func (s *Store) withPaymentTypes(ctx context.Context, n int, row func(int) GetOrderRow) ([]c2c.Order, error) {
	items := make([]c2c.Order, 0, n)
	for i := 0; i < n; i++ {
		item := toOrder(row(i))
		methods, err := loadPaymentMethods(ctx, s.q, item.ID)
		if err != nil {
			return nil, err
		}
		for _, method := range methods {
			item.PaymentTypes = append(item.PaymentTypes, method.Type)
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) Order(ctx context.Context, orderID string) (c2c.Order, error) {
	return loadOrder(ctx, s.q, orderID, false)
}

func (s *Store) OrderParticipant(ctx context.Context, orderID, accountID string) (bool, error) {
	exists, err := s.q.IsOrderParticipant(ctx, IsOrderParticipantParams{OrderID: orderID, AccountID: accountID})
	return exists, mapError(err)
}

func (s *Store) Trade(ctx context.Context, tradeID string) (c2c.Trade, error) {
	return loadTrade(ctx, s.q, tradeID, false, true)
}

func (s *Store) MyActivity(ctx context.Context, accountID string) ([]c2c.Order, []c2c.Trade, error) {
	orderRows, err := s.q.ListOwnerOrders(ctx, accountID)
	if err != nil {
		return nil, nil, mapError(err)
	}
	orders := make([]c2c.Order, 0, len(orderRows))
	for _, row := range orderRows {
		orders = append(orders, toOrder(GetOrderRow(row)))
	}
	tradeRows, err := s.q.ListAccountTrades(ctx, accountID)
	if err != nil {
		return nil, nil, mapError(err)
	}
	trades := make([]c2c.Trade, 0, len(tradeRows))
	for _, row := range tradeRows {
		trades = append(trades, cleanTradeSnapshot(toTrade(GetTradeRow(row))))
	}
	return orders, trades, nil
}

func (s *Store) AdminDisputes(ctx context.Context) ([]c2c.Trade, error) {
	rows, err := s.q.ListDisputedTrades(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]c2c.Trade, 0, len(rows))
	for _, row := range rows {
		items = append(items, cleanTradeSnapshot(toTrade(GetTradeRow(row))))
	}
	return items, nil
}

func (s *Store) EncryptionTargets(ctx context.Context) ([]c2c.EncryptionTarget, error) {
	rows, err := s.q.ListEncryptionTargets(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]c2c.EncryptionTarget, 0, len(rows))
	for _, row := range rows {
		items = append(items, c2c.EncryptionTarget{
			RecordID: row.RecordID, Purpose: row.Purpose,
			Encrypted: c2c.EncryptedValue{KeyID: row.KeyID, Nonce: row.Nonce, Ciphertext: row.Ciphertext},
		})
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Maintenance
// ---------------------------------------------------------------------------

func batchLimit(limit int) int32 {
	return int32(min(limit, math.MaxInt32))
}

// CleanupPrivateData erases payment references and dispute statements of
// terminal trades once the retention period has passed.
func (s *Store) CleanupPrivateData(ctx context.Context, now time.Time, limit int) (int, error) {
	cutoff := now.Add(-c2c.PrivateRetention)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)
	statements, err := q.CleanupDisputeStatements(ctx, CleanupDisputeStatementsParams{
		Cutoff: &cutoff, BatchLimit: batchLimit(limit), DeletedAt: &now,
	})
	if err != nil {
		return 0, mapError(err)
	}
	references, err := q.CleanupPaymentReferences(ctx, CleanupPaymentReferencesParams{
		Cutoff: &cutoff, BatchLimit: batchLimit(limit), DeletedAt: &now,
	})
	if err != nil {
		return 0, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, mapError(err)
	}
	return int(statements + references), nil
}
