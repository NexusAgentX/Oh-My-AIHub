package ledgerpg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
)

// Store implements ledger.Store: user reads and administrator postings that
// own their transaction.
type Store struct {
	pool  *pgxpool.Pool
	q     *Queries
	books *Books
}

// NewStore builds the store. books are the Books its postings report to; nil
// means nobody listens.
func NewStore(pool *pgxpool.Pool, books *Books) *Store {
	if books == nil {
		books = NewBooks()
	}
	return &Store{pool: pool, q: New(pool), books: books}
}

func (s *Store) Points(ctx context.Context, accountID string) (ledger.Points, error) {
	return points(ctx, s.pool, accountID)
}

// ListPoints returns the balance and credit limit of each of the given users
// that exists; unknown ids are missing from the result.
func (s *Store) ListPoints(ctx context.Context, accountIDs []string) (map[string]ledger.Points, error) {
	rows, err := s.q.ListPoints(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	points := make(map[string]ledger.Points, len(rows))
	for _, row := range rows {
		if row.AccountID != nil {
			points[*row.AccountID] = ledger.Points{Balance: row.BalanceNano, CreditLimit: row.CreditLimitNano, UpdatedAt: row.UpdatedAt}
		}
	}
	return points, nil
}

func (s *Store) ListEntries(ctx context.Context, filter ledger.EntryFilter) ([]ledger.EntryView, error) {
	rows, err := s.q.ListUserEntries(ctx, ListUserEntriesParams{
		AccountID: &filter.AccountID, Type: string(filter.Type), ApiKeyID: filter.APIKeyID,
		FromTime: filter.From, ToTime: filter.To, BeforeID: filter.BeforeID, RowLimit: int32(filter.Limit),
	})
	if err != nil {
		return nil, err
	}
	entries := make([]ledger.EntryView, 0, len(rows))
	for _, row := range rows {
		entry := ledger.EntryView{
			ID: row.ID, TransactionID: row.TransactionID, Type: ledger.TransactionType(row.Type), Reason: row.Reason,
			Amount: row.AmountNano, BalanceAfter: row.BalanceAfterNano, APIKeyID: row.ApiKeyID, APIKeyName: row.ApiKeyName,
			CreatedAt: row.CreatedAt,
		}
		if row.RelatedType != nil && row.RelatedID != nil {
			entry.RelatedType, entry.RelatedID = *row.RelatedType, *row.RelatedID
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// existing returns the stored transaction when key was already used.
func existing(ctx context.Context, q *Queries, key string, want ledger.TransactionType, accountID string) (ledger.Posted, bool, error) {
	row, err := q.GetTransactionByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.Posted{}, false, nil
	}
	if err != nil {
		return ledger.Posted{}, false, err
	}
	if row.Type != string(want) || row.RelatedID == nil || *row.RelatedID != accountID {
		return ledger.Posted{}, false, ledger.ErrConflict
	}
	posted, err := loadPosted(ctx, q, row)
	return posted, true, err
}

func (s *Store) Adjust(ctx context.Context, adjustment ledger.Adjustment) (ledger.Posted, error) {
	var posted ledger.Posted
	err := s.books.InTx(ctx, s.pool, func(tx Tx) error {
		var err error
		posted, err = Post(ctx, tx, ledger.Transaction{
			Type: ledger.TypeAdminAdjust, IdempotencyKey: adjustment.IdempotencyKey,
			Related: &ledger.Related{Type: "account", ID: adjustment.AccountID},
			ActorID: adjustment.ActorID, Reason: adjustment.Reason,
			Entries: []ledger.Line{
				{Account: ledger.User(adjustment.AccountID), Amount: adjustment.Amount},
				{Account: ledger.System(ledger.SystemPlatformRevenue), Amount: -adjustment.Amount},
			},
		})
		if err != nil || posted.Replayed {
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: adjustment.ActorID, Action: audit.ActionLedgerAdjust, TargetType: "account", TargetID: adjustment.AccountID,
			Reason: adjustment.Reason,
			Detail: map[string]any{"amount": adjustment.Amount.String(), "transaction_id": posted.ID, "balance_after": userBalanceAfter(posted, adjustment.AccountID).String()},
		})
	})
	return posted, err
}

func (s *Store) WriteOff(ctx context.Context, writeOff ledger.WriteOff) (ledger.Posted, error) {
	var posted ledger.Posted
	err := s.books.InTx(ctx, s.pool, func(tx Tx) error {
		q := s.q.WithTx(tx)
		previous, found, err := existing(ctx, q, writeOff.IdempotencyKey, ledger.TypeBadDebtWriteOff, writeOff.AccountID)
		if err != nil || found {
			posted = previous
			return err
		}
		locked, err := q.LockLedgerAccounts(ctx, LockLedgerAccountsParams{
			UserIds: []string{writeOff.AccountID}, SystemCodes: []string{string(ledger.SystemBadDebt)},
		})
		if err != nil {
			return err
		}
		var balance *money.Amount
		for _, row := range locked {
			if row.AccountID != nil {
				balance = &row.BalanceNano
			}
		}
		if balance == nil {
			return ledger.ErrNotFound
		}
		if *balance >= 0 {
			return ledger.ErrNothingToWriteOff
		}
		amount := -*balance
		posted, err = Post(ctx, tx, ledger.Transaction{
			Type: ledger.TypeBadDebtWriteOff, IdempotencyKey: writeOff.IdempotencyKey,
			Related: &ledger.Related{Type: "account", ID: writeOff.AccountID},
			ActorID: writeOff.ActorID, Reason: writeOff.Reason,
			Entries: []ledger.Line{
				{Account: ledger.User(writeOff.AccountID), Amount: amount},
				{Account: ledger.System(ledger.SystemBadDebt), Amount: -amount},
			},
		})
		if err != nil {
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: writeOff.ActorID, Action: audit.ActionLedgerWriteOff, TargetType: "account", TargetID: writeOff.AccountID,
			Reason: writeOff.Reason, Detail: map[string]any{"amount": amount.String(), "transaction_id": posted.ID},
		})
	})
	return posted, err
}

func userBalanceAfter(posted ledger.Posted, accountID string) money.Amount {
	for _, entry := range posted.Entries {
		if entry.Account.UserID == accountID {
			return entry.BalanceAfter
		}
	}
	return 0
}
