package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var _ c2c.Store = (*Store)(nil)

type c2cQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

const c2cOrderColumns = `
	o.id::text, o.owner_account_id::text, owner.display_name, o.side,
	o.unit_price_fen, o.total_nano, o.available_nano, o.allocated_nano,
	o.settled_nano, o.closed_nano, o.minimum_nano, o.maximum_nano,
	o.status, COALESCE(o.parent_hold_id::text, ''), o.created_at, o.updated_at,
	o.cancelled_at`

const c2cTradeColumns = `
	t.id::text, t.order_id::text, o.side,
	t.buyer_account_id::text, buyer.display_name,
	t.seller_account_id::text, seller.display_name,
	t.quantity_nano, t.unit_price_fen, t.fiat_amount_fen, t.status,
	t.hold_id::text, t.payment_reference_chars,
	COALESCE(t.payment_reference_key_id, ''), COALESCE(t.payment_reference_nonce, ''::bytea),
	COALESCE(t.payment_reference_ciphertext, ''::bytea), t.payment_reference_deleted_at,
	t.payment_deadline, t.review_due_at,
	COALESCE(t.ledger_transaction_id::text, ''), t.created_at, t.updated_at,
	t.paid_at, t.resolved_at`

func scanC2COrder(row scanner) (c2c.Order, error) {
	var order c2c.Order
	var side, status string
	var total, available, allocated, settled, closed, minimum, maximum int64
	err := row.Scan(
		&order.ID, &order.OwnerAccountID, &order.OwnerDisplayName, &side,
		&order.UnitPriceFen, &total, &available, &allocated, &settled, &closed,
		&minimum, &maximum, &status, &order.ParentHoldID, &order.CreatedAt,
		&order.UpdatedAt, &order.CancelledAt,
	)
	order.Side = c2c.Side(side)
	order.Total = money.FromNano(total)
	order.Available = money.FromNano(available)
	order.Allocated = money.FromNano(allocated)
	order.Settled = money.FromNano(settled)
	order.Closed = money.FromNano(closed)
	order.Minimum = money.FromNano(minimum)
	order.Maximum = money.FromNano(maximum)
	order.Status = c2c.OrderStatus(status)
	return order, err
}

func scanC2CTrade(row scanner) (c2c.Trade, error) {
	var trade c2c.Trade
	var side, status string
	var quantity int64
	err := row.Scan(
		&trade.ID, &trade.OrderID, &side,
		&trade.BuyerAccountID, &trade.BuyerDisplayName,
		&trade.SellerAccountID, &trade.SellerDisplayName,
		&quantity, &trade.UnitPriceFen, &trade.FiatAmountFen, &status,
		&trade.HoldID, &trade.PaymentReferenceChars,
		&trade.PaymentReferenceData.KeyID, &trade.PaymentReferenceData.Nonce,
		&trade.PaymentReferenceData.Ciphertext, &trade.PaymentReferenceGone,
		&trade.PaymentDeadline, &trade.ReviewDueAt,
		&trade.LedgerTransactionID, &trade.CreatedAt, &trade.UpdatedAt,
		&trade.PaidAt, &trade.ResolvedAt,
	)
	trade.OrderSide = c2c.Side(side)
	trade.Quantity = money.FromNano(quantity)
	trade.Status = c2c.TradeStatus(status)
	return trade, err
}

func loadC2COrder(ctx context.Context, queryer c2cQueryer, orderID string, lock bool) (c2c.Order, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR UPDATE OF o"
	}
	order, err := scanC2COrder(queryer.QueryRow(ctx, `
		SELECT `+c2cOrderColumns+`
		FROM c2c_orders o
		JOIN accounts owner ON owner.id = o.owner_account_id
		WHERE o.id = $1`+lockClause, orderID))
	if err != nil {
		return c2c.Order{}, mapC2CError(err)
	}
	methods, err := loadC2CPaymentMethods(ctx, queryer, order.ID)
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

func loadC2CPaymentMethods(ctx context.Context, queryer c2cQueryer, orderID string) ([]c2c.PaymentMethod, error) {
	rows, err := queryer.Query(ctx, `
		SELECT id::text, order_id::text, method_type, position, qr_available,
		       key_id, nonce, ciphertext, created_at
		FROM c2c_payment_methods WHERE order_id = $1 ORDER BY position`, orderID)
	if err != nil {
		return nil, mapC2CError(err)
	}
	defer rows.Close()
	methods := make([]c2c.PaymentMethod, 0, c2c.MaximumMethods)
	for rows.Next() {
		var method c2c.PaymentMethod
		var methodType string
		if err := rows.Scan(
			&method.ID, &method.OrderID, &methodType, &method.Position, &method.QRAvailable,
			&method.Private.KeyID, &method.Private.Nonce, &method.Private.Ciphertext, &method.CreatedAt,
		); err != nil {
			return nil, mapC2CError(err)
		}
		method.Type = c2c.PaymentMethodType(methodType)
		methods = append(methods, method)
	}
	return methods, mapC2CError(rows.Err())
}

func loadC2CTrade(ctx context.Context, queryer c2cQueryer, tradeID string, lock bool, details bool) (c2c.Trade, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR UPDATE OF t"
	}
	trade, err := scanC2CTrade(queryer.QueryRow(ctx, `
		SELECT `+c2cTradeColumns+`
		FROM c2c_trades t
		JOIN c2c_orders o ON o.id = t.order_id
		JOIN accounts buyer ON buyer.id = t.buyer_account_id
		JOIN accounts seller ON seller.id = t.seller_account_id
		WHERE t.id = $1`+lockClause, tradeID))
	if err != nil {
		return c2c.Trade{}, mapC2CError(err)
	}
	if !details {
		return trade, nil
	}
	method, err := loadSelectedC2CPaymentMethod(ctx, queryer, trade.ID)
	if err != nil {
		return c2c.Trade{}, err
	}
	trade.SelectedPaymentMethod = &method
	if trade.Evidence, err = loadC2CEvidence(ctx, queryer, trade.ID); err != nil {
		return c2c.Trade{}, err
	}
	if trade.Statements, err = loadC2CStatements(ctx, queryer, trade.ID); err != nil {
		return c2c.Trade{}, err
	}
	if trade.Events, err = loadC2CEvents(ctx, queryer, trade.OrderID, trade.ID); err != nil {
		return c2c.Trade{}, err
	}
	return trade, nil
}

func loadSelectedC2CPaymentMethod(ctx context.Context, queryer c2cQueryer, tradeID string) (c2c.PaymentMethod, error) {
	var method c2c.PaymentMethod
	var methodType string
	err := queryer.QueryRow(ctx, `
		SELECT pm.id::text, pm.order_id::text, pm.method_type, pm.position, pm.qr_available,
		       pm.key_id, pm.nonce, pm.ciphertext, pm.created_at
		FROM c2c_trades t
		JOIN c2c_payment_methods pm ON pm.id = t.selected_payment_method_id
		WHERE t.id = $1`, tradeID).Scan(
		&method.ID, &method.OrderID, &methodType, &method.Position, &method.QRAvailable,
		&method.Private.KeyID, &method.Private.Nonce, &method.Private.Ciphertext, &method.CreatedAt,
	)
	method.Type = c2c.PaymentMethodType(methodType)
	return method, mapC2CError(err)
}

func loadC2CEvidence(ctx context.Context, queryer c2cQueryer, tradeID string) ([]c2c.Evidence, error) {
	rows, err := queryer.Query(ctx, `
		SELECT e.id::text, e.trade_id::text, e.uploader_account_id::text, uploader.display_name,
		       e.kind, e.mime_type, e.size_bytes, e.width, e.height, e.sha256,
		       COALESCE(e.key_id, ''), COALESCE(e.nonce, ''::bytea), COALESCE(e.ciphertext, ''::bytea),
		       e.created_at, e.deleted_at
		FROM c2c_evidence e JOIN accounts uploader ON uploader.id = e.uploader_account_id
		WHERE e.trade_id = $1 ORDER BY e.created_at, e.id`, tradeID)
	if err != nil {
		return nil, mapC2CError(err)
	}
	defer rows.Close()
	items := make([]c2c.Evidence, 0)
	for rows.Next() {
		item, err := scanC2CEvidence(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, mapC2CError(rows.Err())
}

func scanC2CEvidence(row scanner) (c2c.Evidence, error) {
	var evidence c2c.Evidence
	var kind string
	var digest []byte
	err := row.Scan(
		&evidence.ID, &evidence.TradeID, &evidence.UploaderAccountID, &evidence.UploaderName,
		&kind, &evidence.MIME, &evidence.SizeBytes, &evidence.Width, &evidence.Height, &digest,
		&evidence.Encrypted.KeyID, &evidence.Encrypted.Nonce, &evidence.Encrypted.Ciphertext,
		&evidence.CreatedAt, &evidence.DeletedAt,
	)
	evidence.Kind = c2c.EvidenceKind(kind)
	copy(evidence.SHA256[:], digest)
	return evidence, mapC2CError(err)
}

func loadC2CStatements(ctx context.Context, queryer c2cQueryer, tradeID string) ([]c2c.Statement, error) {
	rows, err := queryer.Query(ctx, `
		SELECT s.id::text, s.trade_id::text, s.actor_account_id::text, actor.display_name,
		       s.character_count, COALESCE(s.key_id, ''), COALESCE(s.nonce, ''::bytea),
		       COALESCE(s.ciphertext, ''::bytea), s.created_at, s.deleted_at
		FROM c2c_dispute_statements s JOIN accounts actor ON actor.id = s.actor_account_id
		WHERE s.trade_id = $1 ORDER BY s.created_at, s.id`, tradeID)
	if err != nil {
		return nil, mapC2CError(err)
	}
	defer rows.Close()
	items := make([]c2c.Statement, 0, 2)
	for rows.Next() {
		var item c2c.Statement
		if err := rows.Scan(
			&item.ID, &item.TradeID, &item.ActorAccountID, &item.ActorDisplayName,
			&item.CharacterCount, &item.Encrypted.KeyID, &item.Encrypted.Nonce,
			&item.Encrypted.Ciphertext, &item.CreatedAt, &item.DeletedAt,
		); err != nil {
			return nil, mapC2CError(err)
		}
		items = append(items, item)
	}
	return items, mapC2CError(rows.Err())
}

func loadC2CEvents(ctx context.Context, queryer c2cQueryer, orderID, tradeID string) ([]c2c.Event, error) {
	rows, err := queryer.Query(ctx, `
		SELECT id, order_id::text, COALESCE(trade_id::text, ''), COALESCE(actor_account_id::text, ''),
		       action, reason, COALESCE(ledger_transaction_id::text, ''), COALESCE(hold_business_id, ''), created_at
		FROM c2c_events
		WHERE order_id = $1 AND ($2 = '' OR trade_id = $2::uuid)
		ORDER BY id`, orderID, tradeID)
	if err != nil {
		return nil, mapC2CError(err)
	}
	defer rows.Close()
	items := make([]c2c.Event, 0)
	for rows.Next() {
		var item c2c.Event
		if err := rows.Scan(
			&item.ID, &item.OrderID, &item.TradeID, &item.ActorAccountID, &item.Action,
			&item.Reason, &item.LedgerTransactionID, &item.HoldBusinessID, &item.CreatedAt,
		); err != nil {
			return nil, mapC2CError(err)
		}
		items = append(items, item)
	}
	return items, mapC2CError(rows.Err())
}

func (s *Store) Market(ctx context.Context) (c2c.Market, error) {
	market := c2c.Market{GuidancePriceFen: 100}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		 (SELECT unit_price_fen FROM c2c_trades WHERE status = 'released_to_buyer' ORDER BY resolved_at DESC, id DESC LIMIT 1),
		 (SELECT max(unit_price_fen) FROM c2c_orders WHERE side = 'buy' AND status = 'open' AND available_nano > 0),
		 (SELECT min(unit_price_fen) FROM c2c_orders WHERE side = 'sell' AND status = 'open' AND available_nano > 0)
	`).Scan(&market.LatestPriceFen, &market.BestBidFen, &market.BestAskFen); err != nil {
		return c2c.Market{}, mapC2CError(err)
	}
	if market.BestBidFen != nil && market.BestAskFen != nil {
		spread := *market.BestAskFen - *market.BestBidFen
		market.SpreadFen = &spread
	}
	var err error
	market.SellOrders, err = listC2CMarketOrders(ctx, s.pool, c2c.SideSell)
	if err != nil {
		return c2c.Market{}, err
	}
	market.BuyOrders, err = listC2CMarketOrders(ctx, s.pool, c2c.SideBuy)
	return market, err
}

func listC2CMarketOrders(ctx context.Context, queryer c2cQueryer, side c2c.Side) ([]c2c.Order, error) {
	direction := "ASC"
	if side == c2c.SideBuy {
		direction = "DESC"
	}
	rows, err := queryer.Query(ctx, `
		SELECT `+c2cOrderColumns+`
		FROM c2c_orders o JOIN accounts owner ON owner.id = o.owner_account_id
		WHERE o.side = $1 AND o.status = 'open' AND o.available_nano > 0
		ORDER BY o.unit_price_fen `+direction+`, o.created_at, o.id LIMIT 200`, side)
	if err != nil {
		return nil, mapC2CError(err)
	}
	defer rows.Close()
	items := make([]c2c.Order, 0)
	for rows.Next() {
		item, err := scanC2COrder(rows)
		if err != nil {
			return nil, mapC2CError(err)
		}
		methods, err := loadC2CPaymentMethods(ctx, queryer, item.ID)
		if err != nil {
			return nil, err
		}
		for _, method := range methods {
			item.PaymentTypes = append(item.PaymentTypes, method.Type)
		}
		items = append(items, item)
	}
	return items, mapC2CError(rows.Err())
}

func (s *Store) Order(ctx context.Context, orderID string) (c2c.Order, error) {
	return loadC2COrder(ctx, s.pool, orderID, false)
}

func (s *Store) OrderParticipant(ctx context.Context, orderID, accountID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM c2c_trades
			WHERE order_id = $1 AND (buyer_account_id = $2 OR seller_account_id = $2)
		)`, orderID, accountID).Scan(&exists)
	return exists, mapC2CError(err)
}

func (s *Store) Trade(ctx context.Context, tradeID string) (c2c.Trade, error) {
	return loadC2CTrade(ctx, s.pool, tradeID, false, true)
}

func (s *Store) MyActivity(ctx context.Context, accountID string) ([]c2c.Order, []c2c.Trade, error) {
	orderRows, err := s.pool.Query(ctx, `
		SELECT `+c2cOrderColumns+`
		FROM c2c_orders o JOIN accounts owner ON owner.id = o.owner_account_id
		WHERE o.owner_account_id = $1 ORDER BY o.updated_at DESC, o.id DESC LIMIT 200`, accountID)
	if err != nil {
		return nil, nil, mapC2CError(err)
	}
	orders := make([]c2c.Order, 0)
	for orderRows.Next() {
		item, err := scanC2COrder(orderRows)
		if err != nil {
			orderRows.Close()
			return nil, nil, mapC2CError(err)
		}
		orders = append(orders, item)
	}
	err = orderRows.Err()
	orderRows.Close()
	if err != nil {
		return nil, nil, mapC2CError(err)
	}
	tradeRows, err := s.pool.Query(ctx, `
		SELECT `+c2cTradeColumns+`
		FROM c2c_trades t
		JOIN c2c_orders o ON o.id = t.order_id
		JOIN accounts buyer ON buyer.id = t.buyer_account_id
		JOIN accounts seller ON seller.id = t.seller_account_id
		WHERE t.buyer_account_id = $1 OR t.seller_account_id = $1
		ORDER BY t.updated_at DESC, t.id DESC LIMIT 200`, accountID)
	if err != nil {
		return nil, nil, mapC2CError(err)
	}
	defer tradeRows.Close()
	trades := make([]c2c.Trade, 0)
	for tradeRows.Next() {
		item, err := scanC2CTrade(tradeRows)
		if err != nil {
			return nil, nil, mapC2CError(err)
		}
		trades = append(trades, cleanC2CTradeSnapshot(item))
	}
	return orders, trades, mapC2CError(tradeRows.Err())
}

func (s *Store) AdminDisputes(ctx context.Context) ([]c2c.Trade, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+c2cTradeColumns+`
		FROM c2c_trades t
		JOIN c2c_orders o ON o.id = t.order_id
		JOIN accounts buyer ON buyer.id = t.buyer_account_id
		JOIN accounts seller ON seller.id = t.seller_account_id
		WHERE t.status = 'disputed' ORDER BY t.updated_at, t.id LIMIT 200`)
	if err != nil {
		return nil, mapC2CError(err)
	}
	defer rows.Close()
	items := make([]c2c.Trade, 0)
	for rows.Next() {
		item, err := scanC2CTrade(rows)
		if err != nil {
			return nil, mapC2CError(err)
		}
		items = append(items, cleanC2CTradeSnapshot(item))
	}
	return items, mapC2CError(rows.Err())
}

func (s *Store) Evidence(ctx context.Context, evidenceID string) (c2c.Evidence, error) {
	return scanC2CEvidence(s.pool.QueryRow(ctx, `
		SELECT e.id::text, e.trade_id::text, e.uploader_account_id::text, uploader.display_name,
		       e.kind, e.mime_type, e.size_bytes, e.width, e.height, e.sha256,
		       COALESCE(e.key_id, ''), COALESCE(e.nonce, ''::bytea), COALESCE(e.ciphertext, ''::bytea),
		       e.created_at, e.deleted_at
		FROM c2c_evidence e JOIN accounts uploader ON uploader.id = e.uploader_account_id
		WHERE e.id = $1`, evidenceID))
}

func (s *Store) EncryptionTargets(ctx context.Context) ([]c2c.EncryptionTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, 'payment_method', key_id, nonce, ciphertext FROM c2c_payment_methods
		UNION ALL
		SELECT id::text, 'payment_reference', payment_reference_key_id,
		       payment_reference_nonce, payment_reference_ciphertext
		FROM c2c_trades WHERE payment_reference_deleted_at IS NULL AND payment_reference_ciphertext IS NOT NULL
		UNION ALL
		SELECT id::text, 'dispute_statement', key_id, nonce, ciphertext
		FROM c2c_dispute_statements WHERE deleted_at IS NULL
		UNION ALL
		SELECT id::text, 'evidence:' || kind, key_id, nonce, ciphertext
		FROM c2c_evidence WHERE deleted_at IS NULL
		ORDER BY 1`)
	if err != nil {
		return nil, mapC2CError(err)
	}
	defer rows.Close()
	items := make([]c2c.EncryptionTarget, 0)
	for rows.Next() {
		var item c2c.EncryptionTarget
		if err := rows.Scan(&item.RecordID, &item.Purpose, &item.Encrypted.KeyID, &item.Encrypted.Nonce, &item.Encrypted.Ciphertext); err != nil {
			return nil, mapC2CError(err)
		}
		items = append(items, item)
	}
	return items, mapC2CError(rows.Err())
}

func mapC2CError(err error) error {
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

func cleanC2COrderSnapshot(order c2c.Order) c2c.Order {
	order.PaymentMethods = nil
	return order
}

func cleanC2CTradeSnapshot(trade c2c.Trade) c2c.Trade {
	trade.SelectedPaymentMethod = nil
	trade.Evidence = nil
	trade.Statements = nil
	trade.Events = nil
	trade.PaymentReference = ""
	trade.PaymentReferenceData = c2c.EncryptedValue{}
	return trade
}

func c2cActorKey(command c2c.Command) (string, any) {
	if command.Actor.ID == "" {
		return "system:timeout", nil
	}
	return command.Actor.ID, command.Actor.ID
}

func reserveC2CCommand(ctx context.Context, tx pgx.Tx, command c2c.Command) (json.RawMessage, bool, error) {
	actorKey, actorID := c2cActorKey(command)
	result, err := tx.Exec(ctx, `
		INSERT INTO c2c_commands (
			actor_key, actor_account_id, operation, idempotency_key, payload_hash, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (actor_key, operation, idempotency_key) DO NOTHING`,
		actorKey, actorID, command.Operation, command.IdempotencyKey, command.PayloadHash[:], command.Now)
	if err != nil {
		return nil, false, mapC2CError(err)
	}
	if result.RowsAffected() == 1 {
		return nil, false, nil
	}
	var storedHash []byte
	var snapshot []byte
	var completedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT payload_hash, result_payload, completed_at
		FROM c2c_commands
		WHERE actor_key = $1 AND operation = $2 AND idempotency_key = $3
		FOR UPDATE`, actorKey, command.Operation, command.IdempotencyKey).Scan(&storedHash, &snapshot, &completedAt); err != nil {
		return nil, false, mapC2CError(err)
	}
	if !bytes.Equal(storedHash, command.PayloadHash[:]) || completedAt == nil || len(snapshot) == 0 {
		return nil, false, c2c.ErrConflict
	}
	return snapshot, true, nil
}

func completeC2CCommand(ctx context.Context, tx pgx.Tx, command c2c.Command, snapshot any) error {
	actorKey, _ := c2cActorKey(command)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		UPDATE c2c_commands SET result_payload = $4, completed_at = $5
		WHERE actor_key = $1 AND operation = $2 AND idempotency_key = $3 AND completed_at IS NULL`,
		actorKey, command.Operation, command.IdempotencyKey, encoded, command.Now)
	if err != nil {
		return mapC2CError(err)
	}
	if result.RowsAffected() != 1 {
		return c2c.ErrConflict
	}
	return nil
}

func decodeC2CSnapshot[T any](snapshot json.RawMessage) (T, error) {
	var result T
	if err := json.Unmarshal(snapshot, &result); err != nil {
		return result, fmt.Errorf("decode C2C command snapshot: %w", err)
	}
	return result, nil
}

func lockC2CActor(ctx context.Context, tx pgx.Tx, command c2c.Command, requireAdmin bool) error {
	if command.Actor.ID == "" {
		if command.Operation == "c2c.trade.expire" && !requireAdmin {
			return nil
		}
		return c2c.ErrForbidden
	}
	if err := lockC2CAccountMutationKeys(ctx, tx, command.Actor.ID); err != nil {
		return err
	}
	var status string
	var mustChange, isAdmin bool
	if err := tx.QueryRow(ctx, `
		SELECT status, must_change_password, is_admin FROM accounts WHERE id = $1`,
		command.Actor.ID).Scan(&status, &mustChange, &isAdmin); err != nil {
		return mapC2CError(err)
	}
	if status != string(identity.StatusActive) || mustChange || (requireAdmin && !isAdmin) {
		return c2c.ErrForbidden
	}
	return nil
}

func lockC2CAccountMutationKeys(ctx context.Context, tx pgx.Tx, accountIDs ...string) error {
	unique := make(map[string]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID != "" {
			unique[accountID] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(unique))
	for accountID := range unique {
		ordered = append(ordered, accountID)
	}
	sort.Strings(ordered)
	for _, accountID := range ordered {
		if _, err := tx.Exec(ctx, `
			SELECT pg_advisory_xact_lock(hashtextextended('oh-my-aihub-account:' || $1, 0))`, accountID); err != nil {
			return err
		}
	}
	return nil
}

func lockC2COrderMutationKeys(ctx context.Context, tx pgx.Tx, actorID, orderID string) error {
	var ownerID string
	if err := tx.QueryRow(ctx, `SELECT owner_account_id::text FROM c2c_orders WHERE id = $1`, orderID).Scan(&ownerID); err != nil {
		return mapC2CError(err)
	}
	return lockC2CAccountMutationKeys(ctx, tx, actorID, ownerID)
}

func lockC2CTradeMutationKeys(ctx context.Context, tx pgx.Tx, actorID, tradeID string) error {
	var buyerID, sellerID string
	if err := tx.QueryRow(ctx, `
		SELECT buyer_account_id::text, seller_account_id::text
		FROM c2c_trades WHERE id = $1`, tradeID).Scan(&buyerID, &sellerID); err != nil {
		return mapC2CError(err)
	}
	return lockC2CAccountMutationKeys(ctx, tx, actorID, buyerID, sellerID)
}

func derivedC2CLedgerKey(command c2c.Command, suffix string) string {
	actorKey, _ := c2cActorKey(command)
	digest := sha256.Sum256([]byte(strings.Join([]string{
		actorKey, command.Operation, command.IdempotencyKey, suffix,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func insertC2CEvent(ctx context.Context, tx pgx.Tx, command c2c.Command, orderID, tradeID, action, reason, ledgerTransactionID, holdBusinessID string) error {
	var actorID any
	if command.Actor.ID != "" {
		actorID = command.Actor.ID
	}
	var tradeValue, ledgerValue, holdValue any
	if tradeID != "" {
		tradeValue = tradeID
	}
	if ledgerTransactionID != "" {
		ledgerValue = ledgerTransactionID
	}
	if holdBusinessID != "" {
		holdValue = holdBusinessID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO c2c_events (
			order_id, trade_id, actor_account_id, action, reason,
			ledger_transaction_id, hold_business_id, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		orderID, tradeValue, actorID, action, reason, ledgerValue, holdValue, command.Now)
	return mapC2CError(err)
}

func insertC2CEvidence(ctx context.Context, tx pgx.Tx, command c2c.Command, tradeID string, evidence c2c.NewEvidence) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO c2c_evidence (
			id, trade_id, uploader_account_id, kind, mime_type, size_bytes,
			width, height, sha256, key_id, nonce, ciphertext, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		evidence.ID, tradeID, command.Actor.ID, evidence.Kind, evidence.MIME,
		evidence.SizeBytes, evidence.Width, evidence.Height, evidence.SHA256[:],
		evidence.Encrypted.KeyID, evidence.Encrypted.Nonce, evidence.Encrypted.Ciphertext, command.Now)
	return mapC2CError(err)
}

func ensureC2COrderOwnerReady(ctx context.Context, tx pgx.Tx, order c2c.Order) error {
	var status string
	var mustChange bool
	if err := tx.QueryRow(ctx, `
		SELECT status, must_change_password FROM accounts WHERE id = $1`,
		order.OwnerAccountID).Scan(&status, &mustChange); err != nil {
		return mapC2CError(err)
	}
	if status != string(identity.StatusActive) || mustChange {
		return c2c.ErrConflict
	}
	return nil
}

func c2cOrderStatusAfter(order c2c.Order) (c2c.OrderStatus, error) {
	if order.Status == c2c.OrderCancelled {
		return c2c.OrderCancelled, nil
	}
	if order.Available > 0 {
		return c2c.OrderOpen, nil
	}
	if order.Allocated > 0 {
		return c2c.OrderAllocated, nil
	}
	if order.Settled == order.Total {
		return c2c.OrderFilled, nil
	}
	return "", c2c.ErrConflict
}

func persistC2COrderAmounts(ctx context.Context, tx pgx.Tx, order c2c.Order, now time.Time) error {
	status, err := c2cOrderStatusAfter(order)
	if err != nil {
		return err
	}
	order.Status = status
	result, err := tx.Exec(ctx, `
		UPDATE c2c_orders
		SET available_nano = $2, allocated_nano = $3, settled_nano = $4,
		    closed_nano = $5, status = $6, updated_at = $7
		WHERE id = $1`, order.ID, order.Available.Nano(), order.Allocated.Nano(),
		order.Settled.Nano(), order.Closed.Nano(), order.Status, now)
	if err != nil {
		return mapC2CError(err)
	}
	if result.RowsAffected() != 1 {
		return c2c.ErrConflict
	}
	return nil
}

func (s *Store) CreateOrder(ctx context.Context, command c2c.Command, input c2c.NewOrder) (c2c.Order, error) {
	var result c2c.Order
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2CActor(ctx, tx.Tx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Order](snapshot)
			return err
		}
		parentHoldID := ""
		if input.Side == c2c.SideSell {
			hold, err := ledger.NewService(tx).CreateHold(ctx, ledger.CreateHoldRequest{
				IdempotencyKey: derivedC2CLedgerKey(command, "parent-hold"),
				AccountID:      command.Actor.ID,
				Amount:         input.Total,
				FundingPolicy:  ledger.HoldFundingSettledBalanceOnly,
				Purpose:        ledger.HoldPurposeAssetReservation,
				Reason:         "reserve points for C2C sell order",
				BusinessType:   "c2c_sell_order",
				BusinessID:     input.ID,
			})
			if err != nil {
				return err
			}
			parentHoldID = hold.ID
		}

		var holdValue any
		if parentHoldID != "" {
			holdValue = parentHoldID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO c2c_orders (
				id, owner_account_id, side, unit_price_fen, total_nano,
				available_nano, allocated_nano, settled_nano, closed_nano,
				minimum_nano, maximum_nano, status, parent_hold_id, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $5, 0, 0, 0, $6, $7, 'open', $8, $9, $9)`,
			input.ID, command.Actor.ID, input.Side, input.UnitPriceFen, input.Total.Nano(),
			input.Minimum.Nano(), input.Maximum.Nano(), holdValue, command.Now); err != nil {
			return mapC2CError(err)
		}
		for _, method := range input.PaymentMethods {
			if _, err := tx.Exec(ctx, `
				INSERT INTO c2c_payment_methods (
					id, order_id, method_type, position, qr_available,
					key_id, nonce, ciphertext, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				method.ID, input.ID, method.Type, method.Position, method.QRAvailable,
				method.Private.KeyID, method.Private.Nonce, method.Private.Ciphertext, command.Now); err != nil {
				return mapC2CError(err)
			}
		}
		if err := insertC2CEvent(ctx, tx.Tx, command, input.ID, "", "order.created", "C2C order published", "", input.ID); err != nil {
			return err
		}
		result, err = loadC2COrder(ctx, tx.Tx, input.ID, false)
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2COrderSnapshot(result))
	})
	return result, mapC2CError(err)
}

func (s *Store) TakeOrder(ctx context.Context, command c2c.Command, orderID string, input c2c.NewTrade) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		var ownerID string
		if err := tx.QueryRow(ctx, `SELECT owner_account_id::text FROM c2c_orders WHERE id = $1`, orderID).Scan(&ownerID); err != nil {
			return mapC2CError(err)
		}
		if err := lockC2CAccountMutationKeys(ctx, tx.Tx, command.Actor.ID, ownerID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Trade](snapshot)
			return err
		}
		order, err := loadC2COrder(ctx, tx.Tx, orderID, true)
		if err != nil {
			return err
		}
		if order.OwnerAccountID == command.Actor.ID {
			return c2c.ErrForbidden
		}
		if err := ensureC2COrderOwnerReady(ctx, tx.Tx, order); err != nil {
			return err
		}
		if order.Status != c2c.OrderOpen || order.Available <= 0 || input.Quantity <= 0 || input.Quantity > order.Available || input.Quantity > order.Maximum || (input.Quantity < order.Minimum && input.Quantity != order.Available) {
			return c2c.ErrConflict
		}
		methodFound := false
		for _, method := range order.PaymentMethods {
			if method.ID == input.PaymentMethodID {
				methodFound = true
				break
			}
		}
		if !methodFound {
			return c2c.ErrInvalidInput
		}
		fiatAmount, err := c2c.FiatAmountFen(input.Quantity, order.UnitPriceFen)
		if err != nil {
			return err
		}

		buyerID, sellerID, holdID := command.Actor.ID, order.OwnerAccountID, order.ParentHoldID
		if order.Side == c2c.SideBuy {
			buyerID, sellerID = order.OwnerAccountID, command.Actor.ID
			hold, err := ledger.NewService(tx).CreateHold(ctx, ledger.CreateHoldRequest{
				IdempotencyKey: derivedC2CLedgerKey(command, "trade-hold"),
				AccountID:      sellerID,
				Amount:         input.Quantity,
				FundingPolicy:  ledger.HoldFundingSettledBalanceOnly,
				Purpose:        ledger.HoldPurposeAssetReservation,
				Reason:         "reserve points for C2C buy-order trade",
				BusinessType:   "c2c_trade",
				BusinessID:     input.ID,
			})
			if err != nil {
				return err
			}
			holdID = hold.ID
		}

		order.Available -= input.Quantity
		order.Allocated += input.Quantity
		if err := persistC2COrderAmounts(ctx, tx.Tx, order, command.Now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO c2c_trades (
				id, order_id, buyer_account_id, seller_account_id, quantity_nano,
				unit_price_fen, fiat_amount_fen, status, hold_id, selected_payment_method_id,
				payment_deadline, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, 'awaiting_payment', $8, $9, $10, $11, $11)`,
			input.ID, order.ID, buyerID, sellerID, input.Quantity.Nano(), order.UnitPriceFen,
			fiatAmount, holdID, input.PaymentMethodID, input.PaymentDeadline, command.Now); err != nil {
			return mapC2CError(err)
		}
		if err := insertC2CEvent(ctx, tx.Tx, command, order.ID, input.ID, "trade.created", "order quantity allocated to C2C trade", "", input.ID); err != nil {
			return err
		}
		result, err = loadC2CTrade(ctx, tx.Tx, input.ID, false, false)
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2CTradeSnapshot(result))
	})
	return result, mapC2CError(err)
}

func (s *Store) CancelOrder(ctx context.Context, command c2c.Command, orderID string) (c2c.Order, error) {
	var result c2c.Order
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2COrderMutationKeys(ctx, tx.Tx, command.Actor.ID, orderID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Order](snapshot)
			return err
		}
		order, err := loadC2COrder(ctx, tx.Tx, orderID, true)
		if err != nil {
			return err
		}
		if order.OwnerAccountID != command.Actor.ID {
			return c2c.ErrForbidden
		}
		if order.Status != c2c.OrderOpen && order.Status != c2c.OrderAllocated {
			return c2c.ErrConflict
		}
		closing := order.Available
		if order.Side == c2c.SideSell && closing > 0 {
			if _, err := ledger.NewService(tx).ReleaseHold(ctx, ledger.MutateHoldRequest{
				IdempotencyKey: derivedC2CLedgerKey(command, "parent-release"),
				HoldID:         order.ParentHoldID,
				BusinessID:     order.ID,
				Amount:         ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: closing},
				Reason:         "release unallocated points from cancelled C2C sell order",
			}); err != nil {
				return err
			}
		}
		resultTag, err := tx.Exec(ctx, `
			UPDATE c2c_orders
			SET available_nano = 0, closed_nano = closed_nano + $2,
			    status = 'cancelled', cancelled_at = $3, updated_at = $3
			WHERE id = $1`, order.ID, closing.Nano(), command.Now)
		if err != nil {
			return mapC2CError(err)
		}
		if resultTag.RowsAffected() != 1 {
			return c2c.ErrConflict
		}
		if err := insertC2CEvent(ctx, tx.Tx, command, order.ID, "", "order.cancelled", "C2C order cancelled", "", order.ID); err != nil {
			return err
		}
		result, err = loadC2COrder(ctx, tx.Tx, order.ID, false)
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2COrderSnapshot(result))
	})
	return result, mapC2CError(err)
}

func (s *Store) AdminCancelOrder(ctx context.Context, command c2c.Command, orderID, reason string) (c2c.Order, error) {
	var result c2c.Order
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2COrderMutationKeys(ctx, tx.Tx, command.Actor.ID, orderID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, true); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Order](snapshot)
			return err
		}
		order, err := loadC2COrder(ctx, tx.Tx, orderID, true)
		if err != nil {
			return err
		}
		if order.Status != c2c.OrderOpen && order.Status != c2c.OrderAllocated {
			return c2c.ErrConflict
		}
		closing := order.Available
		if order.Side == c2c.SideSell && closing > 0 {
			if _, err := ledger.NewService(tx).ReleaseHold(ctx, ledger.MutateHoldRequest{
				IdempotencyKey: derivedC2CLedgerKey(command, "parent-release"),
				HoldID:         order.ParentHoldID,
				BusinessID:     order.ID,
				Amount:         ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: closing},
				Reason:         reason,
			}); err != nil {
				return err
			}
		}
		resultTag, err := tx.Exec(ctx, `
			UPDATE c2c_orders
			SET available_nano = 0, closed_nano = closed_nano + $2,
			    status = 'cancelled', cancelled_at = $3, updated_at = $3
			WHERE id = $1`, order.ID, closing.Nano(), command.Now)
		if err != nil {
			return mapC2CError(err)
		}
		if resultTag.RowsAffected() != 1 {
			return c2c.ErrConflict
		}
		if err := insertC2CEvent(ctx, tx.Tx, command, order.ID, "", "order.admin_cancelled", reason, "", order.ID); err != nil {
			return err
		}
		result, err = loadC2COrder(ctx, tx.Tx, order.ID, false)
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2COrderSnapshot(result))
	})
	return result, mapC2CError(err)
}

func lockC2COrderAndTrade(ctx context.Context, tx pgx.Tx, tradeID string) (c2c.Order, c2c.Trade, error) {
	var orderID string
	if err := tx.QueryRow(ctx, `SELECT order_id::text FROM c2c_trades WHERE id = $1`, tradeID).Scan(&orderID); err != nil {
		return c2c.Order{}, c2c.Trade{}, mapC2CError(err)
	}
	order, err := loadC2COrder(ctx, tx, orderID, true)
	if err != nil {
		return c2c.Order{}, c2c.Trade{}, err
	}
	trade, err := loadC2CTrade(ctx, tx, tradeID, true, false)
	if err != nil {
		return c2c.Order{}, c2c.Trade{}, err
	}
	if trade.OrderID != order.ID {
		return c2c.Order{}, c2c.Trade{}, c2c.ErrConflict
	}
	return order, trade, nil
}

func returnC2CAllocation(ctx context.Context, tx *LedgerTransaction, command c2c.Command, order c2c.Order, trade c2c.Trade, terminal c2c.TradeStatus, action, reason string) (c2c.Trade, error) {
	if trade.Status != c2c.TradeAwaitingPayment && trade.Status != c2c.TradePaid && trade.Status != c2c.TradeDisputed {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if (trade.Status == c2c.TradePaid || trade.Status == c2c.TradeDisputed) && terminal != c2c.TradeReturnedToSeller {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if trade.Status == c2c.TradeAwaitingPayment && terminal != c2c.TradeCancelled && terminal != c2c.TradeExpired {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if order.Allocated < trade.Quantity {
		return c2c.Trade{}, c2c.ErrConflict
	}

	releasedHold := false
	if order.Side == c2c.SideBuy || order.Status == c2c.OrderCancelled {
		if _, err := ledger.NewService(tx).ReleaseHold(ctx, ledger.MutateHoldRequest{
			IdempotencyKey: derivedC2CLedgerKey(command, "trade-release"),
			HoldID:         trade.HoldID,
			BusinessID:     trade.ID,
			Amount:         ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: trade.Quantity},
			Reason:         reason,
		}); err != nil {
			return c2c.Trade{}, err
		}
		releasedHold = true
	}
	order.Allocated -= trade.Quantity
	if order.Status == c2c.OrderCancelled {
		order.Closed += trade.Quantity
	} else {
		order.Available += trade.Quantity
	}
	if err := persistC2COrderAmounts(ctx, tx.Tx, order, command.Now); err != nil {
		return c2c.Trade{}, err
	}
	resultTag, err := tx.Exec(ctx, `
		UPDATE c2c_trades SET status = $2, resolved_at = $3, updated_at = $3
		WHERE id = $1 AND status = $4`, trade.ID, terminal, command.Now, trade.Status)
	if err != nil {
		return c2c.Trade{}, mapC2CError(err)
	}
	if resultTag.RowsAffected() != 1 {
		return c2c.Trade{}, c2c.ErrConflict
	}
	holdBusinessID := ""
	if releasedHold {
		holdBusinessID = trade.ID
	}
	if err := insertC2CEvent(ctx, tx.Tx, command, order.ID, trade.ID, action, reason, "", holdBusinessID); err != nil {
		return c2c.Trade{}, err
	}
	return loadC2CTrade(ctx, tx.Tx, trade.ID, false, false)
}

func captureC2CTrade(ctx context.Context, tx *LedgerTransaction, command c2c.Command, order c2c.Order, trade c2c.Trade, action, reason string) (c2c.Trade, error) {
	if trade.Status != c2c.TradePaid && trade.Status != c2c.TradeDisputed {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if order.Allocated < trade.Quantity {
		return c2c.Trade{}, c2c.ErrConflict
	}
	captured, err := ledger.NewService(tx).CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{
			IdempotencyKey: derivedC2CLedgerKey(command, "trade-capture"),
			HoldID:         trade.HoldID,
			BusinessID:     trade.ID,
			Amount:         ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: trade.Quantity},
			Reason:         reason,
		},
		Credits: []ledger.Posting{{
			Account: ledger.UserAccount(trade.BuyerAccountID), BusinessRole: ledger.EntryRoleBuyer,
			Amount: trade.Quantity,
		}},
		ReferenceType: "c2c_trade",
		ReferenceID:   trade.ID,
	})
	if err != nil {
		return c2c.Trade{}, err
	}
	order.Allocated -= trade.Quantity
	order.Settled += trade.Quantity
	if err := persistC2COrderAmounts(ctx, tx.Tx, order, command.Now); err != nil {
		return c2c.Trade{}, err
	}
	resultTag, err := tx.Exec(ctx, `
		UPDATE c2c_trades
		SET status = 'released_to_buyer', ledger_transaction_id = $2,
		    resolved_at = $3, updated_at = $3
		WHERE id = $1 AND status = $4`, trade.ID, captured.Transaction.ID, command.Now, trade.Status)
	if err != nil {
		return c2c.Trade{}, mapC2CError(err)
	}
	if resultTag.RowsAffected() != 1 {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if err := insertC2CEvent(ctx, tx.Tx, command, order.ID, trade.ID, action, reason, captured.Transaction.ID, trade.ID); err != nil {
		return c2c.Trade{}, err
	}
	return loadC2CTrade(ctx, tx.Tx, trade.ID, false, false)
}

func (s *Store) MarkPaid(ctx context.Context, command c2c.Command, tradeID string, evidence *c2c.NewEvidence, paymentReference *c2c.EncryptedValue, paymentReferenceChars int) (c2c.Trade, error) {
	var result c2c.Trade
	expired := false
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2CTradeMutationKeys(ctx, tx.Tx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Trade](snapshot)
			expired = err == nil && result.Status == c2c.TradeExpired
			return err
		}
		order, trade, err := lockC2COrderAndTrade(ctx, tx.Tx, tradeID)
		if err != nil {
			return err
		}
		if trade.BuyerAccountID != command.Actor.ID {
			return c2c.ErrForbidden
		}
		if trade.Status != c2c.TradeAwaitingPayment {
			return c2c.ErrConflict
		}
		if !command.Now.Before(trade.PaymentDeadline) {
			result, err = returnC2CAllocation(ctx, tx, command, order, trade, c2c.TradeExpired, "trade.expired", "C2C payment deadline expired")
			if err != nil {
				return err
			}
			expired = true
			return completeC2CCommand(ctx, tx.Tx, command, cleanC2CTradeSnapshot(result))
		}
		if evidence != nil {
			if err := insertC2CEvidence(ctx, tx.Tx, command, trade.ID, *evidence); err != nil {
				return err
			}
		}
		var keyID any
		var nonce, ciphertext []byte
		if paymentReference != nil {
			keyID, nonce, ciphertext = paymentReference.KeyID, paymentReference.Nonce, paymentReference.Ciphertext
		}
		resultTag, err := tx.Exec(ctx, `
			UPDATE c2c_trades
			SET status = 'paid', payment_reference_chars = $2,
			    payment_reference_key_id = $3, payment_reference_nonce = $4,
			    payment_reference_ciphertext = $5, paid_at = $6, updated_at = $6
			WHERE id = $1 AND status = 'awaiting_payment'`,
			trade.ID, paymentReferenceChars, keyID, nonce, ciphertext, command.Now)
		if err != nil {
			return mapC2CError(err)
		}
		if resultTag.RowsAffected() != 1 {
			return c2c.ErrConflict
		}
		if err := insertC2CEvent(ctx, tx.Tx, command, order.ID, trade.ID, "trade.paid", "buyer declared external payment", "", ""); err != nil {
			return err
		}
		result, err = loadC2CTrade(ctx, tx.Tx, trade.ID, false, false)
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2CTradeSnapshot(result))
	})
	if err != nil {
		return result, mapC2CError(err)
	}
	if expired {
		return result, c2c.ErrExpired
	}
	return result, nil
}

func (s *Store) CancelTrade(ctx context.Context, command c2c.Command, tradeID string, system bool) (c2c.Trade, error) {
	var result c2c.Trade
	expired := false
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2CTradeMutationKeys(ctx, tx.Tx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Trade](snapshot)
			expired = err == nil && result.Status == c2c.TradeExpired
			return err
		}
		order, trade, err := lockC2COrderAndTrade(ctx, tx.Tx, tradeID)
		if err != nil {
			return err
		}
		if !system && trade.BuyerAccountID != command.Actor.ID {
			return c2c.ErrForbidden
		}
		if trade.Status != c2c.TradeAwaitingPayment {
			return c2c.ErrConflict
		}
		terminal, action, reason := c2c.TradeCancelled, "trade.cancelled", "buyer cancelled C2C trade before payment"
		if system || !command.Now.Before(trade.PaymentDeadline) {
			terminal, action, reason = c2c.TradeExpired, "trade.expired", "C2C payment deadline expired"
			expired = !system
		}
		result, err = returnC2CAllocation(ctx, tx, command, order, trade, terminal, action, reason)
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2CTradeSnapshot(result))
	})
	if err != nil {
		return result, mapC2CError(err)
	}
	if expired {
		return result, c2c.ErrExpired
	}
	return result, nil
}

func (s *Store) ConfirmReceipt(ctx context.Context, command c2c.Command, tradeID string) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2CTradeMutationKeys(ctx, tx.Tx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Trade](snapshot)
			return err
		}
		order, trade, err := lockC2COrderAndTrade(ctx, tx.Tx, tradeID)
		if err != nil {
			return err
		}
		if trade.SellerAccountID != command.Actor.ID {
			return c2c.ErrForbidden
		}
		if trade.Status != c2c.TradePaid {
			return c2c.ErrConflict
		}
		result, err = captureC2CTrade(ctx, tx, command, order, trade, "trade.released", "seller confirmed external payment and released points")
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2CTradeSnapshot(result))
	})
	return result, mapC2CError(err)
}

func insertC2CDisputeSubmission(ctx context.Context, tx pgx.Tx, command c2c.Command, tradeID string, statement c2c.NewStatement, evidence []c2c.NewEvidence) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO c2c_dispute_statements (
			id, trade_id, actor_account_id, character_count,
			key_id, nonce, ciphertext, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		statement.ID, tradeID, command.Actor.ID, statement.CharacterCount,
		statement.Encrypted.KeyID, statement.Encrypted.Nonce, statement.Encrypted.Ciphertext, command.Now); err != nil {
		return mapC2CError(err)
	}
	for _, item := range evidence {
		if err := insertC2CEvidence(ctx, tx, command, tradeID, item); err != nil {
			return err
		}
	}
	return nil
}

func c2cTradeParticipant(command c2c.Command, trade c2c.Trade) bool {
	return command.Actor.ID == trade.BuyerAccountID || command.Actor.ID == trade.SellerAccountID
}

func (s *Store) OpenDispute(ctx context.Context, command c2c.Command, tradeID string, statement c2c.NewStatement, evidence []c2c.NewEvidence) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2CTradeMutationKeys(ctx, tx.Tx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Trade](snapshot)
			return err
		}
		order, trade, err := lockC2COrderAndTrade(ctx, tx.Tx, tradeID)
		if err != nil {
			return err
		}
		if !c2cTradeParticipant(command, trade) {
			return c2c.ErrNotFound
		}
		if trade.Status != c2c.TradePaid {
			return c2c.ErrConflict
		}
		if err := insertC2CDisputeSubmission(ctx, tx.Tx, command, trade.ID, statement, evidence); err != nil {
			return err
		}
		reviewDue := command.Now.Add(c2c.ReviewExtension)
		resultTag, err := tx.Exec(ctx, `
			UPDATE c2c_trades SET status = 'disputed', review_due_at = $2, updated_at = $3
			WHERE id = $1 AND status = 'paid'`, trade.ID, reviewDue, command.Now)
		if err != nil {
			return mapC2CError(err)
		}
		if resultTag.RowsAffected() != 1 {
			return c2c.ErrConflict
		}
		if err := insertC2CEvent(ctx, tx.Tx, command, order.ID, trade.ID, "trade.disputed", "participant opened C2C dispute", "", ""); err != nil {
			return err
		}
		result, err = loadC2CTrade(ctx, tx.Tx, trade.ID, false, false)
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2CTradeSnapshot(result))
	})
	return result, mapC2CError(err)
}

func (s *Store) AddDisputeEvidence(ctx context.Context, command c2c.Command, tradeID string, statement c2c.NewStatement, evidence []c2c.NewEvidence) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2CTradeMutationKeys(ctx, tx.Tx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Trade](snapshot)
			return err
		}
		order, trade, err := lockC2COrderAndTrade(ctx, tx.Tx, tradeID)
		if err != nil {
			return err
		}
		if !c2cTradeParticipant(command, trade) {
			return c2c.ErrNotFound
		}
		if trade.Status != c2c.TradeDisputed {
			return c2c.ErrConflict
		}
		if err := insertC2CDisputeSubmission(ctx, tx.Tx, command, trade.ID, statement, evidence); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE c2c_trades SET updated_at = $2 WHERE id = $1`, trade.ID, command.Now); err != nil {
			return mapC2CError(err)
		}
		if err := insertC2CEvent(ctx, tx.Tx, command, order.ID, trade.ID, "trade.evidence_added", "participant added C2C dispute evidence", "", ""); err != nil {
			return err
		}
		result, err = loadC2CTrade(ctx, tx.Tx, trade.ID, false, false)
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2CTradeSnapshot(result))
	})
	return result, mapC2CError(err)
}

func (s *Store) ResolveDispute(ctx context.Context, command c2c.Command, tradeID string, action c2c.ResolutionAction, reason string, now time.Time) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2CTradeMutationKeys(ctx, tx.Tx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, true); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeC2CSnapshot[c2c.Trade](snapshot)
			return err
		}
		order, trade, err := lockC2COrderAndTrade(ctx, tx.Tx, tradeID)
		if err != nil {
			return err
		}
		if (action == c2c.ResolutionExtend && trade.Status != c2c.TradeDisputed) ||
			(action != c2c.ResolutionExtend && trade.Status != c2c.TradePaid && trade.Status != c2c.TradeDisputed) {
			return c2c.ErrConflict
		}
		switch action {
		case c2c.ResolutionRelease:
			result, err = captureC2CTrade(ctx, tx, command, order, trade, "dispute.released", reason)
		case c2c.ResolutionReturn:
			result, err = returnC2CAllocation(ctx, tx, command, order, trade, c2c.TradeReturnedToSeller, "dispute.returned", reason)
		case c2c.ResolutionExtend:
			base := now
			if trade.ReviewDueAt != nil && trade.ReviewDueAt.After(base) {
				base = *trade.ReviewDueAt
			}
			reviewDue := base.Add(c2c.ReviewExtension)
			resultTag, updateErr := tx.Exec(ctx, `
				UPDATE c2c_trades SET review_due_at = $2, updated_at = $3
				WHERE id = $1 AND status = 'disputed'`, trade.ID, reviewDue, command.Now)
			if updateErr != nil {
				return mapC2CError(updateErr)
			}
			if resultTag.RowsAffected() != 1 {
				return c2c.ErrConflict
			}
			if err := insertC2CEvent(ctx, tx.Tx, command, order.ID, trade.ID, "dispute.extended", reason, "", ""); err != nil {
				return err
			}
			result, err = loadC2CTrade(ctx, tx.Tx, trade.ID, false, false)
		default:
			return c2c.ErrInvalidInput
		}
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2CTradeSnapshot(result))
	})
	return result, mapC2CError(err)
}

func c2cExpiryCommand(tradeID string, now time.Time) c2c.Command {
	payloadHash := sha256.Sum256([]byte("c2c.trade.expire\x00" + tradeID))
	return c2c.Command{
		Operation: "c2c.trade.expire", IdempotencyKey: tradeID,
		PayloadHash: payloadHash, Now: now,
	}
}

func (s *Store) expireC2CTrade(ctx context.Context, tradeID string, now time.Time) error {
	command := c2cExpiryCommand(tradeID, now)
	return mapC2CError(s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		if err := lockC2CTradeMutationKeys(ctx, tx.Tx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := lockC2CActor(ctx, tx.Tx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := reserveC2CCommand(ctx, tx.Tx, command)
		if err != nil {
			return err
		}
		if replay {
			_, err = decodeC2CSnapshot[c2c.Trade](snapshot)
			return err
		}
		order, trade, err := lockC2COrderAndTrade(ctx, tx.Tx, tradeID)
		if err != nil {
			return err
		}
		if trade.Status != c2c.TradeAwaitingPayment || now.Before(trade.PaymentDeadline) {
			return c2c.ErrConflict
		}
		result, err := returnC2CAllocation(ctx, tx, command, order, trade, c2c.TradeExpired, "trade.expired", "C2C payment deadline expired")
		if err != nil {
			return err
		}
		return completeC2CCommand(ctx, tx.Tx, command, cleanC2CTradeSnapshot(result))
	}))
}

func (s *Store) ExpireDue(ctx context.Context, now time.Time, limit int) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text FROM c2c_trades
		WHERE status = 'awaiting_payment' AND payment_deadline <= $1
		ORDER BY payment_deadline, id LIMIT $2`, now, limit)
	if err != nil {
		return 0, mapC2CError(err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, mapC2CError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, mapC2CError(err)
	}
	expired := 0
	for _, id := range ids {
		err := s.expireC2CTrade(ctx, id, now)
		if err == nil {
			expired++
			continue
		}
		if errors.Is(err, c2c.ErrConflict) || errors.Is(err, c2c.ErrNotFound) {
			continue
		}
		return expired, err
	}
	return expired, nil
}

func (s *Store) CleanupEvidence(ctx context.Context, now time.Time, limit int) (int, error) {
	cutoff := now.Add(-c2c.EvidenceRetention)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	result, err := tx.Exec(ctx, `
		WITH victims AS (
			SELECT e.id FROM c2c_evidence e
			JOIN c2c_trades t ON t.id = e.trade_id
			WHERE e.deleted_at IS NULL
			  AND t.status IN ('released_to_buyer', 'returned_to_seller', 'cancelled', 'expired')
			  AND t.resolved_at <= $1
			ORDER BY t.resolved_at, e.id
			LIMIT $2
			FOR UPDATE OF e SKIP LOCKED
		)
		UPDATE c2c_evidence e
		SET key_id = NULL, nonce = NULL, ciphertext = NULL, deleted_at = $3
		FROM victims WHERE e.id = victims.id`, cutoff, limit, now)
	if err != nil {
		return 0, mapC2CError(err)
	}
	cleaned := int(result.RowsAffected())
	statementResult, err := tx.Exec(ctx, `
		WITH victims AS (
			SELECT s.id FROM c2c_dispute_statements s
			JOIN c2c_trades t ON t.id = s.trade_id
			WHERE s.deleted_at IS NULL
			  AND t.status IN ('released_to_buyer', 'returned_to_seller', 'cancelled', 'expired')
			  AND t.resolved_at <= $1
			ORDER BY t.resolved_at, s.id
			LIMIT $2
			FOR UPDATE OF s SKIP LOCKED
		)
		UPDATE c2c_dispute_statements s
		SET key_id = NULL, nonce = NULL, ciphertext = NULL, deleted_at = $3
		FROM victims WHERE s.id = victims.id`, cutoff, limit, now)
	if err != nil {
		return 0, mapC2CError(err)
	}
	cleaned += int(statementResult.RowsAffected())
	referenceResult, err := tx.Exec(ctx, `
		WITH victims AS (
			SELECT id FROM c2c_trades
			WHERE payment_reference_ciphertext IS NOT NULL
			  AND status IN ('released_to_buyer', 'returned_to_seller', 'cancelled', 'expired')
			  AND resolved_at <= $1
			ORDER BY resolved_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE c2c_trades t
		SET payment_reference_key_id = NULL, payment_reference_nonce = NULL,
		    payment_reference_ciphertext = NULL, payment_reference_deleted_at = $3
		FROM victims WHERE t.id = victims.id`, cutoff, limit, now)
	if err != nil {
		return 0, mapC2CError(err)
	}
	cleaned += int(referenceResult.RowsAffected())
	if err := tx.Commit(ctx); err != nil {
		return 0, mapC2CError(err)
	}
	return cleaned, nil
}
