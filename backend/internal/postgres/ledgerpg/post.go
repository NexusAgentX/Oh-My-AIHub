// Package ledgerpg is the PostgreSQL persistence of the zero-sum ledger
// (ADR-0025). Post books one balanced transaction inside a transaction owned
// by the caller (ADR-0020), so business rows and ledger entries commit
// atomically. The caller begins that transaction with Books.InTx, which is
// how the process hears what every posting did once the transaction has ended
// (the gateway's balance cache, ADR-0040, follows the ledger that way).
package ledgerpg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// Option adjusts one Post.
type Option func(*options)

type options struct {
	beforeLock func(ctx context.Context, transactionID string) error
}

// BeforeLock runs fn with the ID of the transaction row once it exists (the
// stored transaction's, when the posting is a replay) and before any ledger
// account is locked. It is for the caller's own writes that need the ID but not
// the balances, such as pointing the business row at the transaction: the
// account rows, the platform_revenue row of every API call above all, stay
// locked from the lock to the end of the transaction, so whatever the caller
// does before the lock is time the other postings do not wait for. An error
// from fn is returned by Post, and the caller's transaction must be rolled back.
func BeforeLock(fn func(ctx context.Context, transactionID string) error) Option {
	return func(o *options) { o.beforeLock = fn }
}

// Post books transaction t through tx. It inserts the transaction row (a
// repeated idempotency key returns the stored transaction with Replayed set
// and books nothing), locks the involved ledger accounts in id order, and then
// updates their balances and writes one entry per line with its balance after,
// all in one statement. The database checks at commit that the entries sum to
// zero. The observers of the Books that began tx are told of the changes to
// user accounts, and later how the transaction ended; a replayed posting books
// nothing and tells nothing.
//
// The statements are ordered by what they need. The transaction row (and with
// it the idempotency check) needs no lock, nor does BeforeLock's work; both run
// first. From the lock on, the transaction is a queue for everybody who posts
// over one of the same accounts, so it holds the locks for two round trips and
// the commit and no longer: the one that reads the balances while taking the
// locks, and the one that writes the new balances and entries.
func Post(ctx context.Context, tx Tx, t ledger.Transaction, opts ...Option) (ledger.Posted, error) {
	if err := t.Validate(); err != nil {
		return ledger.Posted{}, err
	}
	var settings options
	for _, opt := range opts {
		opt(&settings)
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
		replayed, err := replay(ctx, q, t)
		if err != nil {
			return ledger.Posted{}, err
		}
		if settings.beforeLock != nil {
			if err := settings.beforeLock(ctx, replayed.ID); err != nil {
				return ledger.Posted{}, err
			}
		}
		return replayed, nil
	}
	if err != nil {
		return ledger.Posted{}, err
	}
	if settings.beforeLock != nil {
		if err := settings.beforeLock(ctx, inserted.ID); err != nil {
			return ledger.Posted{}, err
		}
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
	// The new balances follow from the locked ones, so they are known before
	// anything is written. Validate guarantees that an account appears in one
	// line only, which is what lets BookEntries write each balance once.
	posted := ledger.Posted{ID: inserted.ID, Type: t.Type, CreatedAt: inserted.CreatedAt, Entries: make([]ledger.PostedEntry, 0, len(t.Entries))}
	book := BookEntriesParams{
		TransactionID: inserted.ID, LedgerAccountIds: make([]string, 0, len(t.Entries)),
		AmountsNano: make([]int64, 0, len(t.Entries)), BalancesAfterNano: make([]int64, 0, len(t.Entries)),
	}
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
		book.LedgerAccountIds = append(book.LedgerAccountIds, target.id)
		book.AmountsNano = append(book.AmountsNano, line.Amount.Nano())
		book.BalancesAfterNano = append(book.BalancesAfterNano, after.Nano())
		posted.Entries = append(posted.Entries, ledger.PostedEntry{LedgerAccountID: target.id, Account: line.Account, Amount: line.Amount, BalanceAfter: after})
	}
	if err := q.BookEntries(ctx, book); err != nil {
		return ledger.Posted{}, err
	}
	tx.posted(t.Type, userChanges(t.Entries))
	return posted, nil
}

// userChanges is what the lines do to user accounts.
func userChanges(lines []ledger.Line) []ledger.Change {
	changes := make([]ledger.Change, 0, len(lines))
	for _, line := range lines {
		if line.Account.UserID != "" {
			changes = append(changes, ledger.Change{AccountID: line.Account.UserID, Amount: line.Amount})
		}
	}
	return changes
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

// LockBalance locks the user's ledger account row for the rest of tx and
// returns its balance. Callers that check a balance rule before Post (a C2C
// listing needs balance >= amount) lock first, so concurrent requests cannot
// both pass the check.
func LockBalance(ctx context.Context, tx pgx.Tx, accountID string) (money.Amount, error) {
	rows, err := New(tx).LockLedgerAccounts(ctx, LockLedgerAccountsParams{UserIds: []string{accountID}, SystemCodes: []string{}})
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row.AccountID != nil && *row.AccountID == accountID {
			return row.BalanceNano, nil
		}
	}
	return 0, ledger.ErrNotFound
}
