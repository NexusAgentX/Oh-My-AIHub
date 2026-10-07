package ledgerpg

import (
	"context"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// Tx executes ledger operations on a transaction the caller owns. It never
// begins, commits or rolls back: postings, holds and the caller's own business
// rows therefore commit or roll back together, and the deferred ledger
// constraints are checked at the caller's commit.
type Tx struct {
	q *Queries
}

// NewTx binds ledger operations to db, normally an open pgx.Tx (or a savepoint
// created from one).
func NewTx(db DBTX) *Tx {
	return &Tx{q: New(db)}
}

var _ ledger.Store = (*Tx)(nil)

func (t *Tx) Wallet(ctx context.Context, accountID string) (ledger.Wallet, error) {
	return walletByRef(ctx, t.q, ledger.UserAccount(accountID))
}

func (t *Tx) WalletByRef(ctx context.Context, account ledger.AccountRef) (ledger.Wallet, error) {
	return walletByRef(ctx, t.q, account)
}

func (t *Tx) Entries(ctx context.Context, accountID string, beforeID int64, limit int) ([]ledger.Entry, error) {
	return entriesByRef(ctx, t.q, ledger.UserAccount(accountID), beforeID, limit)
}

func (t *Tx) EntriesByRef(ctx context.Context, account ledger.AccountRef, beforeID int64, limit int) ([]ledger.Entry, error) {
	return entriesByRef(ctx, t.q, account, beforeID, limit)
}

func (t *Tx) Metrics(ctx context.Context) (ledger.Metrics, error) {
	return ledgerMetrics(ctx, t.q)
}

func (t *Tx) Post(ctx context.Context, request ledger.PostRequest, hash [32]byte) (ledger.Transaction, error) {
	if err := ledger.ValidatePostRequest(request); err != nil {
		return ledger.Transaction{}, err
	}
	operation := "transaction:" + string(request.Kind)
	_, snapshot, replay, err := reserveCommand(ctx, t.q, request.IdempotencyKey, operation, hash)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if replay {
		if request.Kind == ledger.TransactionAdjustment {
			if err := requireActiveAdminLocked(ctx, t.q, request.ActorAccountID); err != nil {
				return ledger.Transaction{}, err
			}
		}
		return decodeCommandResult[ledger.Transaction](snapshot)
	}
	created, err := postLocked(ctx, t.q, operation, request, nil, "", "", false)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := completeCommand(ctx, t.q, operation, request.IdempotencyKey, created.ID, created); err != nil {
		return ledger.Transaction{}, err
	}
	return created, nil
}

func (t *Tx) Reverse(ctx context.Context, key, originalID, reason, referenceID, actorID string, hash [32]byte) (ledger.Transaction, error) {
	return t.reverse(ctx, key, originalID, reason, referenceID, actorID, hash, false)
}

// ReverseSystem is intentionally not part of ledger.Store. It allows an
// automatic delivery compensation to strictly negate one sealed settlement
// transaction without depending on a currently available human administrator.
// It cannot create arbitrary postings.
func (t *Tx) ReverseSystem(ctx context.Context, key, originalID, reason, referenceID string, hash [32]byte) (ledger.Transaction, error) {
	return t.reverse(ctx, key, originalID, reason, referenceID, "", hash, true)
}

func (t *Tx) TransferBadDebt(ctx context.Context, accountID string, amount money.Amount, key, reason, referenceID, actorID string, hash [32]byte) (ledger.Transaction, error) {
	operation := "transaction:" + string(ledger.TransactionBadDebt)
	_, snapshot, replay, err := reserveCommand(ctx, t.q, key, operation, hash)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if replay {
		if err := requireActiveAdminLocked(ctx, t.q, actorID); err != nil {
			return ledger.Transaction{}, err
		}
		return decodeCommandResult[ledger.Transaction](snapshot)
	}
	postings := []ledger.Posting{
		{Account: ledger.UserAccount(accountID), BusinessRole: ledger.EntryRoleDebtor, Amount: amount},
		{Account: ledger.SystemAccount(ledger.AccountLoss), BusinessRole: ledger.EntryRolePlatformLoss, Amount: -amount},
	}
	resolved, states, err := resolveAndLockPostings(ctx, t.q, postings, nil)
	if err != nil {
		return ledger.Transaction{}, err
	}
	debtor := states[resolved[0].ledgerAccountID]
	debtorFinal, err := ledger.Add(debtor.postedBalance, amount)
	if err != nil || debtor.postedBalance >= 0 || debtorFinal > 0 {
		return ledger.Transaction{}, ledger.ErrInvalidInput
	}
	request := ledger.PostRequest{
		IdempotencyKey: key, Kind: ledger.TransactionBadDebt, Reason: reason,
		ReferenceType: "bad_debt", ReferenceID: referenceID, ActorAccountID: actorID,
		Entries: postings,
	}
	created, err := postLocked(ctx, t.q, operation, request, states, "", "", false)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := completeCommand(ctx, t.q, operation, key, created.ID, created); err != nil {
		return ledger.Transaction{}, err
	}
	return created, nil
}
