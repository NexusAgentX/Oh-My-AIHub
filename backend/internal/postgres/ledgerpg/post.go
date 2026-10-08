// Package ledgerpg is the PostgreSQL persistence of the zero-sum ledger
// (ADR-0025). Post books one balanced transaction inside a transaction owned
// by the caller (ADR-0020), so business rows and ledger entries commit
// atomically.
package ledgerpg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// Post books transaction t through tx. It inserts the transaction row (a
// repeated idempotency key returns the stored transaction with Replayed set
// and books nothing), locks the involved ledger accounts in id order, updates
// their balances and writes one entry per line with its balance after. The
// database checks at commit that the entries sum to zero.
func Post(ctx context.Context, tx pgx.Tx, t ledger.Transaction) (ledger.Posted, error) {
	if err := t.Validate(); err != nil {
		return ledger.Posted{}, err
	}
	q := New(tx)
	params := InsertTransactionParams{Type: string(t.Type), IdempotencyKey: t.IdempotencyKey, Reason: t.Reason}
	if t.Related != nil {
		params.RelatedType, params.RelatedID = &t.Related.Type, &t.Related.ID
	}
	if t.ActorID != "" {
		params.ActorID = &t.ActorID
	}
	inserted, err := q.InsertTransaction(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return replay(ctx, q, t)
	}
	if err != nil {
		return ledger.Posted{}, err
	}

	var userIDs, systemCodes []string
	for _, line := range t.Entries {
		if line.Account.UserID != "" {
			userIDs = append(userIDs, line.Account.UserID)
		} else {
			systemCodes = append(systemCodes, string(line.Account.System))
		}
	}
	locked, err := q.LockLedgerAccounts(ctx, LockLedgerAccountsParams{UserIds: userIDs, SystemCodes: systemCodes})
	if err != nil {
		return ledger.Posted{}, err
	}
	type account struct {
		id      string
		balance money.Amount
	}
	accounts := make(map[ledger.AccountRef]*account, len(locked))
	for _, row := range locked {
		accounts[refOf(row.AccountID, row.SystemCode)] = &account{id: row.ID, balance: row.BalanceNano}
	}
	posted := ledger.Posted{ID: inserted.ID, Type: t.Type, CreatedAt: inserted.CreatedAt, Entries: make([]ledger.PostedEntry, 0, len(t.Entries))}
	for _, line := range t.Entries {
		target, ok := accounts[line.Account]
		if !ok {
			return ledger.Posted{}, ledger.ErrNotFound
		}
		after, err := ledger.AddBalance(target.balance, line.Amount)
		if err != nil {
			return ledger.Posted{}, err
		}
		target.balance = after
		if err := q.UpdateLedgerBalance(ctx, UpdateLedgerBalanceParams{ID: target.id, BalanceNano: after}); err != nil {
			return ledger.Posted{}, err
		}
		if err := q.InsertEntry(ctx, InsertEntryParams{TransactionID: inserted.ID, LedgerAccountID: target.id, AmountNano: line.Amount, BalanceAfterNano: after}); err != nil {
			return ledger.Posted{}, err
		}
		posted.Entries = append(posted.Entries, ledger.PostedEntry{LedgerAccountID: target.id, Account: line.Account, Amount: line.Amount, BalanceAfter: after})
	}
	return posted, nil
}

func replay(ctx context.Context, q *Queries, t ledger.Transaction) (ledger.Posted, error) {
	existing, err := q.GetTransactionByKey(ctx, t.IdempotencyKey)
	if err != nil {
		return ledger.Posted{}, err
	}
	sameRelated := (t.Related == nil && existing.RelatedType == nil) ||
		(t.Related != nil && existing.RelatedType != nil && existing.RelatedID != nil && *existing.RelatedType == t.Related.Type && *existing.RelatedID == t.Related.ID)
	if existing.Type != string(t.Type) || !sameRelated {
		return ledger.Posted{}, ledger.ErrConflict
	}
	return loadPosted(ctx, q, existing)
}

func loadPosted(ctx context.Context, q *Queries, existing LedgerTransaction) (ledger.Posted, error) {
	rows, err := q.ListTransactionEntries(ctx, existing.ID)
	if err != nil {
		return ledger.Posted{}, err
	}
	posted := ledger.Posted{ID: existing.ID, Type: ledger.TransactionType(existing.Type), CreatedAt: existing.CreatedAt, Replayed: true}
	for _, row := range rows {
		posted.Entries = append(posted.Entries, ledger.PostedEntry{
			LedgerAccountID: row.LedgerAccountID, Account: refOf(row.AccountID, row.SystemCode),
			Amount: row.AmountNano, BalanceAfter: row.BalanceAfterNano,
		})
	}
	return posted, nil
}

func refOf(accountID, systemCode *string) ledger.AccountRef {
	if accountID != nil {
		return ledger.User(*accountID)
	}
	if systemCode != nil {
		return ledger.System(ledger.SystemCode(*systemCode))
	}
	return ledger.AccountRef{}
}

// CreateUserAccount creates the ledger account of a new identity account
// inside the caller's transaction.
func CreateUserAccount(ctx context.Context, db DBTX, accountID string) error {
	return New(db).InsertUserLedgerAccount(ctx, &accountID)
}

// Balance and CreditLimit read a user's current values through db, normally
// the caller's transaction, so business rules can be checked before Post.
func Balance(ctx context.Context, db DBTX, accountID string) (money.Amount, error) {
	points, err := points(ctx, db, accountID)
	return points.Balance, err
}

func CreditLimit(ctx context.Context, db DBTX, accountID string) (money.Amount, error) {
	points, err := points(ctx, db, accountID)
	return points.CreditLimit, err
}

func points(ctx context.Context, db DBTX, accountID string) (ledger.Points, error) {
	row, err := New(db).GetPoints(ctx, &accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.Points{}, ledger.ErrNotFound
	}
	if err != nil {
		return ledger.Points{}, err
	}
	return ledger.Points{Balance: row.BalanceNano, CreditLimit: row.CreditLimitNano, UpdatedAt: row.UpdatedAt}, nil
}
