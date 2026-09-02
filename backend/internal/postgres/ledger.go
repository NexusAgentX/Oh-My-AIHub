package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ledgerAccountState struct {
	id                string
	identityAccountID string
	kind              ledger.AccountKind
	postedBalance     money.Amount
	assetReserved     money.Amount
	spendAuthorized   money.Amount
	creditLimit       money.Amount
	creditFrozen      bool
	status            identity.Status
}

func (s *Store) Wallet(ctx context.Context, accountID string) (ledger.Wallet, error) {
	state, updatedAt, err := scanWalletState(s.pool.QueryRow(ctx, `
		SELECT la.id::text, COALESCE(la.identity_account_id::text, ''), la.kind,
		       la.posted_balance_nano, la.asset_reserved_nano, la.spend_authorized_nano, COALESCE(a.credit_limit_nano, 0),
		       COALESCE(a.credit_frozen, false), COALESCE(a.status, 'active'), la.updated_at
		FROM ledger_accounts la
		LEFT JOIN accounts a ON a.id = la.identity_account_id
		WHERE la.identity_account_id = $1`, accountID))
	if err != nil {
		return ledger.Wallet{}, mapLedgerError(err)
	}
	return walletFromState(state, updatedAt)
}

func scanWalletState(row scanner) (ledgerAccountState, time.Time, error) {
	var state ledgerAccountState
	var kind, status string
	var posted, assetReserved, spendAuthorized, credit int64
	var updatedAt time.Time
	err := row.Scan(&state.id, &state.identityAccountID, &kind, &posted, &assetReserved, &spendAuthorized, &credit, &state.creditFrozen, &status, &updatedAt)
	state.kind = ledger.AccountKind(kind)
	state.postedBalance = money.FromNano(posted)
	state.assetReserved = money.FromNano(assetReserved)
	state.spendAuthorized = money.FromNano(spendAuthorized)
	state.creditLimit = money.FromNano(credit)
	state.status = identity.Status(status)
	return state, updatedAt, err
}

func walletFromState(state ledgerAccountState, updatedAt time.Time) (ledger.Wallet, error) {
	effectiveCredit := state.creditLimit
	if state.creditFrozen || state.kind != ledger.AccountUser {
		effectiveCredit = 0
	}
	return ledger.Wallet{
		LedgerAccountID: state.id, IdentityAccountID: state.identityAccountID, Kind: state.kind,
		PostedBalance: state.postedBalance, AssetReserved: state.assetReserved, SpendAuthorized: state.spendAuthorized,
		CreditLimit: state.creditLimit, CreditFrozen: state.creditFrozen,
		EffectiveCredit: effectiveCredit, SpendableCapacity: ledger.SpendableCapacity(state.postedBalance, effectiveCredit, state.assetReserved, state.spendAuthorized),
		OverLimit: ledger.IsOverLimit(state.postedBalance, effectiveCredit, state.assetReserved, state.spendAuthorized), Status: state.status, UpdatedAt: updatedAt,
	}, nil
}

func (s *Store) Entries(ctx context.Context, accountID string, beforeID int64, limit int) ([]ledger.Entry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.transaction_id::text, e.ledger_account_id::text, la.kind,
		       COALESCE(la.identity_account_id::text, ''), e.entry_ordinal, e.amount_nano,
		       e.posted_balance_before_nano, e.posted_balance_after_nano, e.created_at
		FROM ledger_entries e
		JOIN ledger_accounts la ON la.id = e.ledger_account_id
		WHERE la.identity_account_id = $1 AND ($2::bigint = 0 OR e.id < $2)
		ORDER BY e.id DESC LIMIT $3`, accountID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]ledger.Entry, 0, limit)
	for rows.Next() {
		entry, err := scanLedgerEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) Metrics(ctx context.Context) (ledger.Metrics, error) {
	var total, positive, negative, credit, usedCredit, assetReserved, spendAuthorized, incentive, loss string
	var overLimit, frozenAccounts, accountCount int64
	err := s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(sum(la.posted_balance_nano::numeric), 0)::text,
			COALESCE(sum(GREATEST(la.posted_balance_nano, 0)::numeric), 0)::text,
			COALESCE(sum(LEAST(la.posted_balance_nano, 0)::numeric), 0)::text,
			COALESCE(sum(CASE WHEN la.kind = 'user' THEN a.credit_limit_nano::numeric ELSE 0 END), 0)::text,
			COALESCE(sum(CASE WHEN la.kind = 'user' THEN GREATEST(0::numeric, la.asset_reserved_nano::numeric + la.spend_authorized_nano::numeric - la.posted_balance_nano::numeric) ELSE 0 END), 0)::text,
			COALESCE(sum(la.asset_reserved_nano::numeric), 0)::text,
			COALESCE(sum(la.spend_authorized_nano::numeric), 0)::text,
			COALESCE(max(la.posted_balance_nano) FILTER (WHERE la.kind = 'platform_incentive'), 0)::text,
			COALESCE(max(la.posted_balance_nano) FILTER (WHERE la.kind = 'platform_loss'), 0)::text,
			count(*) FILTER (WHERE la.kind = 'user' AND la.posted_balance_nano::numeric + CASE WHEN a.credit_frozen THEN 0 ELSE a.credit_limit_nano END::numeric < la.asset_reserved_nano::numeric + la.spend_authorized_nano::numeric),
			count(*) FILTER (WHERE la.kind = 'user' AND a.credit_frozen),
			count(*)
		FROM ledger_accounts la LEFT JOIN accounts a ON a.id = la.identity_account_id`).Scan(
		&total, &positive, &negative, &credit, &usedCredit, &assetReserved, &spendAuthorized, &incentive, &loss,
		&overLimit, &frozenAccounts, &accountCount,
	)
	if err != nil {
		return ledger.Metrics{}, err
	}
	return ledger.Metrics{
		TotalPostedBalance: nanoIntegerToPoints(total), PositivePostedBalance: nanoIntegerToPoints(positive),
		NegativePostedBalance: nanoIntegerToPoints(negative), TotalCreditLimit: nanoIntegerToPoints(credit),
		UsedCredit: nanoIntegerToPoints(usedCredit), AssetReserved: nanoIntegerToPoints(assetReserved), SpendAuthorized: nanoIntegerToPoints(spendAuthorized),
		IncentivePostedBalance: nanoIntegerToPoints(incentive), LossPostedBalance: nanoIntegerToPoints(loss),
		OverLimitAccounts: overLimit, CreditFrozenAccounts: frozenAccounts, AccountCount: accountCount,
	}, nil
}

func nanoIntegerToPoints(value string) string {
	integer := new(big.Int)
	if _, ok := integer.SetString(value, 10); !ok {
		panic("PostgreSQL returned an invalid numeric aggregate")
	}
	negative := integer.Sign() < 0
	integer.Abs(integer)
	scale := big.NewInt(money.Scale)
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(integer, scale, fraction)
	result := whole.String()
	if fraction.Sign() != 0 {
		fractionText := fraction.String()
		fractionText = strings.Repeat("0", 9-len(fractionText)) + fractionText
		fractionText = strings.TrimRight(fractionText, "0")
		result += "." + fractionText
	}
	if negative && result != "0" {
		result = "-" + result
	}
	return result
}

func (s *Store) Post(ctx context.Context, request ledger.PostRequest, hash [32]byte) (ledger.Transaction, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ledger.Transaction{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	operation := "transaction:" + string(request.Kind)
	existingID, replay, err := reserveLedgerCommand(ctx, tx, request.IdempotencyKey, operation, hash)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if replay {
		return loadTransaction(ctx, tx, existingID)
	}
	if request.Kind == ledger.TransactionBadDebt {
		return ledger.Transaction{}, ledger.ErrInvalidInput
	}
	created, err := postLocked(ctx, tx, request, hash, nil, "")
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ledger.Transaction{}, mapLedgerError(err)
	}
	return created, nil
}

func (s *Store) Reverse(ctx context.Context, key, originalID, reason, referenceID, actorID string, hash [32]byte) (ledger.Transaction, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ledger.Transaction{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	existingID, replay, err := reserveLedgerCommand(ctx, tx, key, "transaction:reversal", hash)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if replay {
		return loadTransaction(ctx, tx, existingID)
	}
	var originalKind string
	if err := tx.QueryRow(ctx, `SELECT kind FROM ledger_transactions WHERE id = $1 AND sealed FOR UPDATE`, originalID).Scan(&originalKind); err != nil {
		return ledger.Transaction{}, mapLedgerError(err)
	}
	if ledger.TransactionKind(originalKind) == ledger.TransactionReversal {
		return ledger.Transaction{}, ledger.ErrInvalidInput
	}
	var alreadyReversed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ledger_transactions WHERE reversal_of_transaction_id = $1)`, originalID).Scan(&alreadyReversed); err != nil {
		return ledger.Transaction{}, err
	}
	if alreadyReversed {
		return ledger.Transaction{}, ledger.ErrConflict
	}
	original, err := loadTransaction(ctx, tx, originalID)
	if err != nil {
		return ledger.Transaction{}, err
	}
	postings := make([]ledger.Posting, 0, len(original.Entries))
	for _, entry := range original.Entries {
		amount, negateErr := ledger.Negate(entry.Amount)
		if negateErr != nil {
			return ledger.Transaction{}, negateErr
		}
		ref := ledger.SystemAccount(entry.AccountKind)
		if entry.IdentityAccountID != "" {
			ref = ledger.UserAccount(entry.IdentityAccountID)
		}
		postings = append(postings, ledger.Posting{Account: ref, Amount: amount})
	}
	request := ledger.PostRequest{
		IdempotencyKey: key, Kind: ledger.TransactionReversal, Reason: reason,
		ReferenceType: "reversal", ReferenceID: referenceID, ActorAccountID: actorID,
		ReversalOfTransactionID: originalID, Entries: postings,
	}
	created, err := postLocked(ctx, tx, request, hash, nil, "")
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ledger.Transaction{}, mapLedgerError(err)
	}
	return created, nil
}

func postLocked(ctx context.Context, tx pgx.Tx, request ledger.PostRequest, _ [32]byte, prelocked map[string]ledgerAccountState, reservedDebitAccountID string) (ledger.Transaction, error) {
	resolved, states, err := resolveAndLockPostings(ctx, tx, request.Entries, prelocked)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := validateSystemAccountUse(request.Kind, resolved, states); err != nil {
		return ledger.Transaction{}, err
	}
	finalBalances, err := validatePostingChanges(resolved, states, reservedDebitAccountID)
	if err != nil {
		return ledger.Transaction{}, err
	}
	var transactionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO ledger_transactions (idempotency_key, kind, reason, reference_type, reference_id, actor_account_id, reversal_of_transaction_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, NULLIF($7, '')::uuid)
		RETURNING id::text`, request.IdempotencyKey, request.Kind, request.Reason, request.ReferenceType, request.ReferenceID, request.ActorAccountID, request.ReversalOfTransactionID).Scan(&transactionID); err != nil {
		return ledger.Transaction{}, mapLedgerError(err)
	}
	running := make(map[string]money.Amount, len(states))
	for id, state := range states {
		running[id] = state.postedBalance
	}
	for index, posting := range resolved {
		before := running[posting.ledgerAccountID]
		after, err := ledger.Add(before, posting.amount)
		if err != nil {
			return ledger.Transaction{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, amount_nano, posted_balance_before_nano, posted_balance_after_nano)
			VALUES ($1, $2, $3, $4, $5, $6)`, transactionID, posting.ledgerAccountID, index+1, posting.amount.Nano(), before.Nano(), after.Nano()); err != nil {
			return ledger.Transaction{}, mapLedgerError(err)
		}
		running[posting.ledgerAccountID] = after
	}
	for id, balance := range finalBalances {
		if _, err := tx.Exec(ctx, `
			UPDATE ledger_accounts SET posted_balance_nano = $2, version = version + 1, updated_at = now() WHERE id = $1`, id, balance.Nano()); err != nil {
			return ledger.Transaction{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE ledger_transactions SET sealed = true WHERE id = $1`, transactionID); err != nil {
		return ledger.Transaction{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE ledger_commands SET result_id = $2 WHERE idempotency_key = $1`, request.IdempotencyKey, transactionID); err != nil {
		return ledger.Transaction{}, err
	}
	return loadTransaction(ctx, tx, transactionID)
}

type resolvedPosting struct {
	ledgerAccountID string
	amount          money.Amount
}

func resolveAndLockPostings(ctx context.Context, tx pgx.Tx, postings []ledger.Posting, prelocked map[string]ledgerAccountState) ([]resolvedPosting, map[string]ledgerAccountState, error) {
	resolved := make([]resolvedPosting, len(postings))
	ids := make([]string, 0, len(postings))
	for index, posting := range postings {
		var id string
		var err error
		if posting.Account.IdentityAccountID != "" {
			err = tx.QueryRow(ctx, `SELECT id::text FROM ledger_accounts WHERE identity_account_id = $1`, posting.Account.IdentityAccountID).Scan(&id)
		} else {
			err = tx.QueryRow(ctx, `SELECT id::text FROM ledger_accounts WHERE system_code = $1`, posting.Account.SystemKind).Scan(&id)
		}
		if err != nil {
			return nil, nil, mapLedgerError(err)
		}
		resolved[index] = resolvedPosting{ledgerAccountID: id, amount: posting.Amount}
		ids = append(ids, id)
	}
	states, err := lockLedgerAccounts(ctx, tx, ids, prelocked)
	return resolved, states, err
}

func lockLedgerAccounts(ctx context.Context, tx pgx.Tx, ids []string, prelocked map[string]ledgerAccountState) (map[string]ledgerAccountState, error) {
	unique := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		unique[id] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	states := make(map[string]ledgerAccountState, len(ordered))
	for id, state := range prelocked {
		states[id] = state
	}
	for _, id := range ordered {
		if _, ok := states[id]; ok {
			continue
		}
		state, err := lockLedgerAccount(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		states[id] = state
	}
	return states, nil
}

func lockLedgerAccount(ctx context.Context, tx pgx.Tx, id string) (ledgerAccountState, error) {
	var state ledgerAccountState
	var kind string
	var posted, assetReserved, spendAuthorized int64
	err := tx.QueryRow(ctx, `
		SELECT id::text, COALESCE(identity_account_id::text, ''), kind,
		       posted_balance_nano, asset_reserved_nano, spend_authorized_nano
		FROM ledger_accounts WHERE id = $1 FOR UPDATE`, id).Scan(
		&state.id, &state.identityAccountID, &kind, &posted, &assetReserved, &spendAuthorized,
	)
	if err != nil {
		return ledgerAccountState{}, mapLedgerError(err)
	}
	state.kind = ledger.AccountKind(kind)
	state.postedBalance = money.FromNano(posted)
	state.assetReserved = money.FromNano(assetReserved)
	state.spendAuthorized = money.FromNano(spendAuthorized)
	state.status = identity.StatusActive
	if state.identityAccountID != "" {
		var credit int64
		var status string
		if err := tx.QueryRow(ctx, `SELECT credit_limit_nano, credit_frozen, status FROM accounts WHERE id = $1`, state.identityAccountID).Scan(&credit, &state.creditFrozen, &status); err != nil {
			return ledgerAccountState{}, mapLedgerError(err)
		}
		state.creditLimit = money.FromNano(credit)
		state.status = identity.Status(status)
	}
	return state, nil
}

func validateSystemAccountUse(kind ledger.TransactionKind, postings []resolvedPosting, states map[string]ledgerAccountState) error {
	for _, posting := range postings {
		state := states[posting.ledgerAccountID]
		if state.kind == ledger.AccountLoss && kind != ledger.TransactionBadDebt && kind != ledger.TransactionReversal {
			return ledger.ErrInvalidInput
		}
	}
	return nil
}

func validatePostingChanges(postings []resolvedPosting, states map[string]ledgerAccountState, reservedDebitAccountID string) (map[string]money.Amount, error) {
	deltas := make(map[string]money.Amount, len(states))
	for _, posting := range postings {
		var err error
		deltas[posting.ledgerAccountID], err = ledger.Add(deltas[posting.ledgerAccountID], posting.amount)
		if err != nil {
			return nil, err
		}
	}
	final := make(map[string]money.Amount, len(deltas))
	for id, delta := range deltas {
		state := states[id]
		balance, err := ledger.Add(state.postedBalance, delta)
		if err != nil {
			return nil, err
		}
		if balance < state.postedBalance && id != reservedDebitAccountID {
			if state.kind == ledger.AccountIncentive && balance < 0 {
				return nil, ledger.ErrInsufficientFunds
			}
			if state.kind == ledger.AccountUser {
				if state.status != identity.StatusActive {
					return nil, identity.ErrForbidden
				}
				if state.creditFrozen {
					return nil, ledger.ErrCreditFrozen
				}
				if ledger.IsOverLimit(balance, state.creditLimit, state.assetReserved, state.spendAuthorized) {
					return nil, ledger.ErrInsufficientFunds
				}
			}
		}
		final[id] = balance
	}
	return final, nil
}

func reserveLedgerCommand(ctx context.Context, tx pgx.Tx, key, operation string, hash [32]byte) (string, bool, error) {
	var insertedKey string
	err := tx.QueryRow(ctx, `
		INSERT INTO ledger_commands (idempotency_key, operation, payload_hash)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING
		RETURNING idempotency_key`, key, operation, hash[:]).Scan(&insertedKey)
	if err == nil {
		return "", false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, mapLedgerError(err)
	}
	var existingOperation, resultID string
	var existingHash []byte
	if err := tx.QueryRow(ctx, `
		SELECT operation, payload_hash, COALESCE(result_id::text, '')
		FROM ledger_commands WHERE idempotency_key = $1`, key).Scan(&existingOperation, &existingHash, &resultID); err != nil {
		return "", false, mapLedgerError(err)
	}
	if existingOperation != operation || !bytes.Equal(existingHash, hash[:]) || resultID == "" {
		return "", false, ledger.ErrConflict
	}
	return resultID, true, nil
}

func loadTransaction(ctx context.Context, queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, id string) (ledger.Transaction, error) {
	var result ledger.Transaction
	var kind string
	err := queryer.QueryRow(ctx, `
		SELECT id::text, idempotency_key, kind, reason, reference_type, reference_id,
		       COALESCE(actor_account_id::text, ''), COALESCE(reversal_of_transaction_id::text, ''), created_at
		FROM ledger_transactions WHERE id = $1 AND sealed`, id).Scan(
		&result.ID, &result.IdempotencyKey, &kind, &result.Reason, &result.ReferenceType,
		&result.ReferenceID, &result.ActorAccountID, &result.ReversalOfTransactionID, &result.CreatedAt,
	)
	if err != nil {
		return ledger.Transaction{}, mapLedgerError(err)
	}
	result.Kind = ledger.TransactionKind(kind)
	rows, err := queryer.Query(ctx, `
		SELECT e.id, e.transaction_id::text, e.ledger_account_id::text, la.kind,
		       COALESCE(la.identity_account_id::text, ''), e.entry_ordinal, e.amount_nano,
		       e.posted_balance_before_nano, e.posted_balance_after_nano, e.created_at
		FROM ledger_entries e JOIN ledger_accounts la ON la.id = e.ledger_account_id
		WHERE e.transaction_id = $1 ORDER BY e.entry_ordinal`, id)
	if err != nil {
		return ledger.Transaction{}, err
	}
	defer rows.Close()
	for rows.Next() {
		entry, err := scanLedgerEntry(rows)
		if err != nil {
			return ledger.Transaction{}, err
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, rows.Err()
}

func scanLedgerEntry(row scanner) (ledger.Entry, error) {
	var entry ledger.Entry
	var kind string
	var amount, before, after int64
	err := row.Scan(&entry.ID, &entry.TransactionID, &entry.LedgerAccountID, &kind,
		&entry.IdentityAccountID, &entry.Ordinal, &amount, &before, &after, &entry.CreatedAt)
	entry.AccountKind = ledger.AccountKind(kind)
	entry.Amount = money.FromNano(amount)
	entry.PostedBalanceBefore = money.FromNano(before)
	entry.PostedBalanceAfter = money.FromNano(after)
	return entry, err
}

func (s *Store) CreateHold(ctx context.Context, request ledger.CreateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ledger.Hold{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	existingID, replay, err := reserveLedgerCommand(ctx, tx, request.IdempotencyKey, "hold.create", hash)
	if err != nil {
		return ledger.Hold{}, err
	}
	if replay {
		return loadHold(ctx, tx, existingID, false)
	}
	var ledgerAccountID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM ledger_accounts WHERE identity_account_id = $1`, request.AccountID).Scan(&ledgerAccountID); err != nil {
		return ledger.Hold{}, mapLedgerError(err)
	}
	state, err := lockLedgerAccount(ctx, tx, ledgerAccountID)
	if err != nil {
		return ledger.Hold{}, err
	}
	if state.status != identity.StatusActive {
		return ledger.Hold{}, identity.ErrForbidden
	}
	if state.creditFrozen {
		return ledger.Hold{}, ledger.ErrCreditFrozen
	}
	newAssetReserved, newSpendAuthorized := state.assetReserved, state.spendAuthorized
	switch request.FundingPolicy {
	case ledger.HoldFundingSettledBalanceOnly:
		var err error
		newAssetReserved, err = ledger.Add(state.assetReserved, request.Amount)
		if err != nil || ledger.IsOverLimit(state.postedBalance, 0, newAssetReserved, state.spendAuthorized) {
			return ledger.Hold{}, ledger.ErrInsufficientFunds
		}
	case ledger.HoldFundingCreditAllowed:
		var err error
		newSpendAuthorized, err = ledger.Add(state.spendAuthorized, request.Amount)
		if err != nil || ledger.IsOverLimit(state.postedBalance, state.creditLimit, state.assetReserved, newSpendAuthorized) {
			return ledger.Hold{}, ledger.ErrInsufficientFunds
		}
	default:
		return ledger.Hold{}, ledger.ErrInvalidInput
	}
	var holdID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO ledger_holds (
			ledger_account_id, create_idempotency_key, purpose, funding_policy, amount_nano,
			remaining_nano, reason, business_type, business_id
		) VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8)
		RETURNING id::text`, ledgerAccountID, request.IdempotencyKey, request.Purpose,
		request.FundingPolicy, request.Amount.Nano(), request.Reason, request.BusinessType, request.BusinessID).Scan(&holdID); err != nil {
		return ledger.Hold{}, mapLedgerError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ledger_accounts SET asset_reserved_nano = $2, spend_authorized_nano = $3, version = version + 1, updated_at = now() WHERE id = $1`, ledgerAccountID, newAssetReserved.Nano(), newSpendAuthorized.Nano()); err != nil {
		return ledger.Hold{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE ledger_commands SET result_id = $2 WHERE idempotency_key = $1`, request.IdempotencyKey, holdID); err != nil {
		return ledger.Hold{}, err
	}
	hold, err := loadHold(ctx, tx, holdID, false)
	if err != nil {
		return ledger.Hold{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ledger.Hold{}, mapLedgerError(err)
	}
	return hold, nil
}

func (s *Store) ReleaseHold(ctx context.Context, request ledger.MutateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ledger.Hold{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	existingID, replay, err := reserveLedgerCommand(ctx, tx, request.IdempotencyKey, "hold.release", hash)
	if err != nil {
		return ledger.Hold{}, err
	}
	if replay {
		return loadHold(ctx, tx, existingID, false)
	}
	hold, err := loadHold(ctx, tx, request.HoldID, true)
	if err != nil {
		return ledger.Hold{}, err
	}
	amount, err := resolveHoldAmount(request.Amount, hold)
	if err != nil {
		return ledger.Hold{}, err
	}
	state, err := lockLedgerAccount(ctx, tx, hold.LedgerAccountID)
	if err != nil {
		return ledger.Hold{}, err
	}
	newReserved, err := holdReservationAfter(state, hold.Purpose, amount)
	if err != nil {
		return ledger.Hold{}, ledger.ErrConflict
	}
	if err := updateHoldReservation(ctx, tx, state, hold.Purpose, newReserved); err != nil {
		return ledger.Hold{}, err
	}
	if err := updateHoldTotals(ctx, tx, hold.ID, amount, false); err != nil {
		return ledger.Hold{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_hold_events (hold_id, idempotency_key, kind, amount_nano, reason)
		VALUES ($1, $2, 'release', $3, $4)`, hold.ID, request.IdempotencyKey, amount.Nano(), request.Reason); err != nil {
		return ledger.Hold{}, mapLedgerError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE ledger_commands SET result_id = $2 WHERE idempotency_key = $1`, request.IdempotencyKey, hold.ID); err != nil {
		return ledger.Hold{}, err
	}
	hold, err = loadHold(ctx, tx, hold.ID, false)
	if err != nil {
		return ledger.Hold{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ledger.Hold{}, mapLedgerError(err)
	}
	return hold, nil
}

func (s *Store) CaptureHold(ctx context.Context, request ledger.CaptureHoldRequest, hash [32]byte) (ledger.CaptureResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	existingID, replay, err := reserveLedgerCommand(ctx, tx, request.IdempotencyKey, "hold.capture", hash)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	if replay {
		hold, holdErr := loadHold(ctx, tx, request.HoldID, false)
		transaction, transactionErr := loadTransaction(ctx, tx, existingID)
		if holdErr != nil {
			return ledger.CaptureResult{}, holdErr
		}
		return ledger.CaptureResult{Hold: hold, Transaction: transaction}, transactionErr
	}
	hold, err := loadHold(ctx, tx, request.HoldID, true)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	amount, err := resolveHoldAmount(request.Amount, hold)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	postings := []ledger.Posting{
		{Account: ledger.UserAccount(hold.OwnerAccountID), Amount: -amount},
		{Account: request.Destination, Amount: amount},
	}
	resolved, states, err := resolveAndLockPostings(ctx, tx, postings, nil)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	source := states[resolved[0].ledgerAccountID]
	newReserved, err := holdReservationAfter(source, hold.Purpose, amount)
	if err != nil {
		return ledger.CaptureResult{}, ledger.ErrConflict
	}
	// Capture consumes only this hold's reservation. Reducing balance and frozen
	// by the same amount leaves available balance unchanged, so it remains legal
	// after a credit freeze, account disable, or administrative limit reduction.
	postRequest := ledger.PostRequest{
		IdempotencyKey: request.IdempotencyKey, Kind: ledger.TransactionCapture,
		Reason: request.Reason, ReferenceType: request.ReferenceType, ReferenceID: request.ReferenceID,
		Entries: postings,
	}
	transaction, err := postLocked(ctx, tx, postRequest, hash, states, source.id)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	if err := updateHoldReservation(ctx, tx, source, hold.Purpose, newReserved); err != nil {
		return ledger.CaptureResult{}, err
	}
	if err := updateHoldTotals(ctx, tx, hold.ID, amount, true); err != nil {
		return ledger.CaptureResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_hold_events (hold_id, idempotency_key, kind, amount_nano, transaction_id, reason)
		VALUES ($1, $2, 'capture', $3, $4, $5)`, hold.ID, request.IdempotencyKey, amount.Nano(), transaction.ID, request.Reason); err != nil {
		return ledger.CaptureResult{}, mapLedgerError(err)
	}
	hold, err = loadHold(ctx, tx, hold.ID, false)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ledger.CaptureResult{}, mapLedgerError(err)
	}
	return ledger.CaptureResult{Hold: hold, Transaction: transaction}, nil
}

func resolveHoldAmount(request ledger.HoldAmount, hold ledger.Hold) (money.Amount, error) {
	if hold.Status != "active" || hold.Remaining <= 0 {
		return 0, ledger.ErrHoldClosed
	}
	amount := request.Amount
	if request.Mode == ledger.HoldAmountAll {
		amount = hold.Remaining
	}
	if amount <= 0 || amount > hold.Remaining {
		return 0, ledger.ErrHoldAmountExceeded
	}
	return amount, nil
}

func holdReservationAfter(state ledgerAccountState, purpose ledger.HoldPurpose, amount money.Amount) (money.Amount, error) {
	var current money.Amount
	switch purpose {
	case ledger.HoldPurposeAssetReservation:
		current = state.assetReserved
	case ledger.HoldPurposeSpendAuthorization:
		current = state.spendAuthorized
	default:
		return 0, ledger.ErrInvalidInput
	}
	remaining, err := ledger.Subtract(current, amount)
	if err != nil || remaining < 0 {
		return 0, ledger.ErrConflict
	}
	return remaining, nil
}

func updateHoldReservation(ctx context.Context, tx pgx.Tx, state ledgerAccountState, purpose ledger.HoldPurpose, amount money.Amount) error {
	var query string
	switch purpose {
	case ledger.HoldPurposeAssetReservation:
		query = `UPDATE ledger_accounts SET asset_reserved_nano = $2, version = version + 1, updated_at = now() WHERE id = $1`
	case ledger.HoldPurposeSpendAuthorization:
		query = `UPDATE ledger_accounts SET spend_authorized_nano = $2, version = version + 1, updated_at = now() WHERE id = $1`
	default:
		return ledger.ErrInvalidInput
	}
	_, err := tx.Exec(ctx, query, state.id, amount.Nano())
	return err
}

func updateHoldTotals(ctx context.Context, tx pgx.Tx, holdID string, amount money.Amount, capture bool) error {
	capturedColumn, releasedColumn := int64(0), amount.Nano()
	if capture {
		capturedColumn, releasedColumn = amount.Nano(), 0
	}
	result, err := tx.Exec(ctx, `
		UPDATE ledger_holds
		SET remaining_nano = remaining_nano - $2,
		    captured_nano = captured_nano + $3,
		    released_nano = released_nano + $4,
		    status = CASE WHEN remaining_nano = $2 THEN 'closed' ELSE 'active' END,
		    updated_at = now()
		WHERE id = $1 AND status = 'active' AND remaining_nano >= $2`, holdID, amount.Nano(), capturedColumn, releasedColumn)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ledger.ErrConflict
	}
	return nil
}

func loadHold(ctx context.Context, queryer rowQueryer, id string, forUpdate bool) (ledger.Hold, error) {
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE OF h"
	}
	var hold ledger.Hold
	var policy, purpose string
	var amount, remaining, captured, released int64
	err := queryer.QueryRow(ctx, `
		SELECT h.id::text, h.ledger_account_id::text, COALESCE(la.identity_account_id::text, ''),
		       h.purpose, h.funding_policy, h.amount_nano, h.remaining_nano, h.captured_nano,
		       h.released_nano, h.status, h.business_type, h.business_id, h.created_at, h.updated_at
		FROM ledger_holds h JOIN ledger_accounts la ON la.id = h.ledger_account_id
		WHERE h.id = $1`+suffix, id).Scan(
		&hold.ID, &hold.LedgerAccountID, &hold.OwnerAccountID, &purpose, &policy,
		&amount, &remaining, &captured, &released, &hold.Status,
		&hold.BusinessType, &hold.BusinessID, &hold.CreatedAt, &hold.UpdatedAt,
	)
	if err != nil {
		return ledger.Hold{}, mapLedgerError(err)
	}
	hold.FundingPolicy = ledger.HoldFundingPolicy(policy)
	hold.Purpose = ledger.HoldPurpose(purpose)
	hold.Amount = money.FromNano(amount)
	hold.Remaining = money.FromNano(remaining)
	hold.Captured = money.FromNano(captured)
	hold.Released = money.FromNano(released)
	return hold, nil
}

func (s *Store) TransferBadDebt(ctx context.Context, accountID string, amount money.Amount, key, reason, referenceID, actorID string, hash [32]byte) (ledger.Transaction, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ledger.Transaction{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	existingID, replay, err := reserveLedgerCommand(ctx, tx, key, "transaction:"+string(ledger.TransactionBadDebt), hash)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if replay {
		return loadTransaction(ctx, tx, existingID)
	}
	postings := []ledger.Posting{
		{Account: ledger.UserAccount(accountID), Amount: amount},
		{Account: ledger.SystemAccount(ledger.AccountLoss), Amount: -amount},
	}
	resolved, states, err := resolveAndLockPostings(ctx, tx, postings, nil)
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
	created, err := postLocked(ctx, tx, request, hash, states, "")
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ledger.Transaction{}, mapLedgerError(err)
	}
	return created, nil
}

func mapLedgerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ledger.ErrConflict
		case "23514", "22003":
			return ledger.ErrInvalidInput
		}
	}
	return fmt.Errorf("ledger store: %w", err)
}
