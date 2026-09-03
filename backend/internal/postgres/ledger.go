package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

type ledgerQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// LedgerTransaction is a transaction-bound ledger store. Later business
// features can write their own state through the embedded pgx transaction and
// execute the validated ledger service against this value before one commit.
type LedgerTransaction struct {
	pgx.Tx
}

var _ ledger.Store = (*LedgerTransaction)(nil)

func (s *Store) WithLedgerTransaction(ctx context.Context, work func(*LedgerTransaction) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	ledgerTx := &LedgerTransaction{Tx: tx}
	if err := work(ledgerTx); err != nil {
		return err
	}
	return mapLedgerError(tx.Commit(ctx))
}

func (t *LedgerTransaction) Wallet(ctx context.Context, accountID string) (ledger.Wallet, error) {
	return t.WalletByRef(ctx, ledger.UserAccount(accountID))
}

func (t *LedgerTransaction) WalletByRef(ctx context.Context, account ledger.AccountRef) (ledger.Wallet, error) {
	return walletByRef(ctx, t.Tx, account)
}

func (t *LedgerTransaction) Entries(ctx context.Context, accountID string, beforeID int64, limit int) ([]ledger.Entry, error) {
	return t.EntriesByRef(ctx, ledger.UserAccount(accountID), beforeID, limit)
}

func (t *LedgerTransaction) EntriesByRef(ctx context.Context, account ledger.AccountRef, beforeID int64, limit int) ([]ledger.Entry, error) {
	return entriesByRef(ctx, t.Tx, account, beforeID, limit)
}

func (t *LedgerTransaction) Metrics(ctx context.Context) (ledger.Metrics, error) {
	return ledgerMetrics(ctx, t.Tx)
}

func (s *Store) Wallet(ctx context.Context, accountID string) (ledger.Wallet, error) {
	return s.WalletByRef(ctx, ledger.UserAccount(accountID))
}

func (s *Store) WalletByRef(ctx context.Context, account ledger.AccountRef) (ledger.Wallet, error) {
	return walletByRef(ctx, s.pool, account)
}

func walletByRef(ctx context.Context, queryer rowQueryer, account ledger.AccountRef) (ledger.Wallet, error) {
	where, argument, err := ledgerAccountWhere(account)
	if err != nil {
		return ledger.Wallet{}, err
	}
	state, updatedAt, err := scanWalletState(queryer.QueryRow(ctx, `
		SELECT la.id::text, COALESCE(la.identity_account_id::text, ''), la.kind,
		       la.posted_balance_nano, la.asset_reserved_nano, la.spend_authorized_nano, COALESCE(a.credit_limit_nano, 0),
		       COALESCE(a.credit_frozen, false), COALESCE(a.status, 'active'), la.updated_at
		FROM ledger_accounts la
		LEFT JOIN accounts a ON a.id = la.identity_account_id
		WHERE `+where, argument))
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
	spendableCapacity := ledger.SpendableCapacity(state.postedBalance, effectiveCredit, state.assetReserved, state.spendAuthorized)
	if state.creditFrozen {
		spendableCapacity = 0
	}
	return ledger.Wallet{
		LedgerAccountID: state.id, IdentityAccountID: state.identityAccountID, Kind: state.kind,
		PostedBalance: state.postedBalance, AssetReserved: state.assetReserved, SpendAuthorized: state.spendAuthorized,
		CreditLimit: state.creditLimit, CreditFrozen: state.creditFrozen,
		EffectiveCredit: effectiveCredit, SpendableCapacity: spendableCapacity,
		OverLimit: ledger.IsOverLimit(state.postedBalance, effectiveCredit), Status: state.status, UpdatedAt: updatedAt,
	}, nil
}

func (s *Store) Entries(ctx context.Context, accountID string, beforeID int64, limit int) ([]ledger.Entry, error) {
	return s.EntriesByRef(ctx, ledger.UserAccount(accountID), beforeID, limit)
}

func (s *Store) EntriesByRef(ctx context.Context, account ledger.AccountRef, beforeID int64, limit int) ([]ledger.Entry, error) {
	return entriesByRef(ctx, s.pool, account, beforeID, limit)
}

func entriesByRef(ctx context.Context, queryer ledgerQueryer, account ledger.AccountRef, beforeID int64, limit int) ([]ledger.Entry, error) {
	where, argument, err := ledgerAccountWhere(account)
	if err != nil {
		return nil, err
	}
	rows, err := queryer.Query(ctx, `
		SELECT e.id, e.transaction_id::text, e.ledger_account_id::text, la.kind,
		       COALESCE(la.identity_account_id::text, ''), e.entry_ordinal, e.business_role, e.amount_nano,
		       e.posted_balance_before_nano, e.posted_balance_after_nano, e.created_at,
		       t.kind, t.reason, t.reference_type, t.reference_id,
		       COALESCE(t.actor_account_id::text, ''),
		       COALESCE(t.reversal_of_transaction_id::text, ''),
		       COALESCE(t.hold_id::text, ''),
		       COALESCE((
		           SELECT jsonb_agg(jsonb_build_object(
		               'account_kind', other_account.kind,
		               'identity_account_id', COALESCE(other_account.identity_account_id::text, ''),
		               'business_role', other_entry.business_role,
		               'amount_nano', other_entry.amount_nano
		           ) ORDER BY other_entry.entry_ordinal)
		           FROM ledger_entries other_entry
		           JOIN ledger_accounts other_account ON other_account.id = other_entry.ledger_account_id
		           WHERE other_entry.transaction_id = e.transaction_id AND other_entry.id <> e.id
		       ), '[]'::jsonb)
		FROM ledger_entries e
		JOIN ledger_accounts la ON la.id = e.ledger_account_id
		JOIN ledger_transactions t ON t.id = e.transaction_id
		WHERE `+where+` AND ($2::bigint = 0 OR e.id < $2)
		ORDER BY e.id DESC LIMIT $3`, argument, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]ledger.Entry, 0, limit)
	for rows.Next() {
		entry, err := scanTraceableLedgerEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func ledgerAccountWhere(account ledger.AccountRef) (string, any, error) {
	if account.IdentityAccountID != "" && account.SystemKind == "" {
		return "la.identity_account_id = $1", account.IdentityAccountID, nil
	}
	if account.IdentityAccountID == "" && (account.SystemKind == ledger.AccountIncentive || account.SystemKind == ledger.AccountLoss) {
		return "la.system_code = $1", account.SystemKind, nil
	}
	return "", nil, ledger.ErrInvalidInput
}

type counterpartyRecord struct {
	AccountKind       string `json:"account_kind"`
	IdentityAccountID string `json:"identity_account_id"`
	BusinessRole      string `json:"business_role"`
	AmountNano        int64  `json:"amount_nano"`
}

func scanTraceableLedgerEntry(row scanner) (ledger.Entry, error) {
	var entry ledger.Entry
	var accountKind, businessRole, transactionKind string
	var amount, before, after int64
	var counterpartiesJSON []byte
	if err := row.Scan(
		&entry.ID, &entry.TransactionID, &entry.LedgerAccountID, &accountKind,
		&entry.IdentityAccountID, &entry.Ordinal, &businessRole, &amount, &before, &after,
		&entry.CreatedAt, &transactionKind, &entry.Reason, &entry.ReferenceType, &entry.ReferenceID,
		&entry.ActorAccountID, &entry.ReversalOfTransactionID, &entry.HoldID,
		&counterpartiesJSON,
	); err != nil {
		return ledger.Entry{}, err
	}
	entry.AccountKind = ledger.AccountKind(accountKind)
	entry.BusinessRole = ledger.EntryRole(businessRole)
	entry.TransactionKind = ledger.TransactionKind(transactionKind)
	entry.Amount = money.FromNano(amount)
	entry.PostedBalanceBefore = money.FromNano(before)
	entry.PostedBalanceAfter = money.FromNano(after)
	var records []counterpartyRecord
	if err := json.Unmarshal(counterpartiesJSON, &records); err != nil {
		return ledger.Entry{}, err
	}
	entry.Counterparties = make([]ledger.Counterparty, 0, len(records))
	for _, record := range records {
		entry.Counterparties = append(entry.Counterparties, ledger.Counterparty{
			AccountKind: ledger.AccountKind(record.AccountKind), IdentityAccountID: record.IdentityAccountID,
			BusinessRole: ledger.EntryRole(record.BusinessRole), Amount: money.FromNano(record.AmountNano),
		})
	}
	return entry, nil
}

func (s *Store) Metrics(ctx context.Context) (ledger.Metrics, error) {
	return ledgerMetrics(ctx, s.pool)
}

func ledgerMetrics(ctx context.Context, queryer rowQueryer) (ledger.Metrics, error) {
	var total, positive, negative, credit, usedCredit, assetReserved, spendAuthorized, incentive, loss string
	var postedDifference, assetDifference, authorizationDifference string
	var overLimit, frozenAccounts, accountCount, postedMismatchAccounts, holdMismatchAccounts int64
	err := queryer.QueryRow(ctx, `
		WITH entry_totals AS (
			SELECT ledger_account_id, sum(amount_nano::numeric) AS posted_source
			FROM ledger_entries GROUP BY ledger_account_id
		), hold_totals AS (
			SELECT ledger_account_id,
			       COALESCE(sum(remaining_nano::numeric) FILTER (WHERE purpose = 'asset_reservation'), 0) AS asset_source,
			       COALESCE(sum(remaining_nano::numeric) FILTER (WHERE purpose = 'spend_authorization'), 0) AS authorization_source
			FROM ledger_holds GROUP BY ledger_account_id
		), reconciled AS (
			SELECT la.*, a.credit_limit_nano, a.credit_frozen,
			       COALESCE(et.posted_source, 0) AS posted_source,
			       COALESCE(ht.asset_source, 0) AS asset_source,
			       COALESCE(ht.authorization_source, 0) AS authorization_source
			FROM ledger_accounts la
			LEFT JOIN accounts a ON a.id = la.identity_account_id
			LEFT JOIN entry_totals et ON et.ledger_account_id = la.id
			LEFT JOIN hold_totals ht ON ht.ledger_account_id = la.id
		)
		SELECT
			COALESCE(sum(posted_balance_nano::numeric), 0)::text,
			COALESCE(sum(GREATEST(posted_balance_nano, 0)::numeric), 0)::text,
			COALESCE(sum(LEAST(posted_balance_nano, 0)::numeric), 0)::text,
			COALESCE(sum(CASE WHEN kind = 'user' THEN credit_limit_nano::numeric ELSE 0 END), 0)::text,
			COALESCE(sum(CASE WHEN kind = 'user' THEN GREATEST(0::numeric, -posted_balance_nano::numeric) ELSE 0 END), 0)::text,
			COALESCE(sum(asset_reserved_nano::numeric), 0)::text,
			COALESCE(sum(spend_authorized_nano::numeric), 0)::text,
			COALESCE(max(posted_balance_nano) FILTER (WHERE kind = 'platform_incentive'), 0)::text,
			COALESCE(max(posted_balance_nano) FILTER (WHERE kind = 'platform_loss'), 0)::text,
			count(*) FILTER (WHERE kind = 'user' AND GREATEST(0::numeric, -posted_balance_nano::numeric) > CASE WHEN credit_frozen THEN 0 ELSE credit_limit_nano END::numeric),
			count(*) FILTER (WHERE kind = 'user' AND credit_frozen),
			count(*),
			COALESCE(sum(abs(posted_balance_nano::numeric - posted_source)), 0)::text,
			count(*) FILTER (WHERE posted_balance_nano::numeric <> posted_source),
			COALESCE(sum(abs(asset_reserved_nano::numeric - asset_source)), 0)::text,
			COALESCE(sum(abs(spend_authorized_nano::numeric - authorization_source)), 0)::text,
			count(*) FILTER (WHERE asset_reserved_nano::numeric <> asset_source OR spend_authorized_nano::numeric <> authorization_source)
		FROM reconciled`).Scan(
		&total, &positive, &negative, &credit, &usedCredit, &assetReserved, &spendAuthorized, &incentive, &loss,
		&overLimit, &frozenAccounts, &accountCount, &postedDifference, &postedMismatchAccounts,
		&assetDifference, &authorizationDifference, &holdMismatchAccounts,
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
		PostedProjectionDifference: nanoIntegerToPoints(postedDifference), PostedProjectionMismatchAccounts: postedMismatchAccounts,
		AssetReservationDifference: nanoIntegerToPoints(assetDifference), SpendAuthorizationDifference: nanoIntegerToPoints(authorizationDifference),
		HoldProjectionMismatchAccounts: holdMismatchAccounts,
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
	var result ledger.Transaction
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		var err error
		result, err = tx.Post(ctx, request, hash)
		return err
	})
	return result, err
}

func (t *LedgerTransaction) Post(ctx context.Context, request ledger.PostRequest, hash [32]byte) (ledger.Transaction, error) {
	if err := ledger.ValidatePostRequest(request); err != nil {
		return ledger.Transaction{}, err
	}
	operation := "transaction:" + string(request.Kind)
	_, snapshot, replay, err := reserveLedgerCommand(ctx, t.Tx, request.IdempotencyKey, operation, hash)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if replay {
		if request.Kind == ledger.TransactionAdjustment {
			if err := requireActiveAdminLocked(ctx, t.Tx, request.ActorAccountID); err != nil {
				return ledger.Transaction{}, err
			}
		}
		return decodeCommandResult[ledger.Transaction](snapshot)
	}
	created, err := postLocked(ctx, t.Tx, operation, request, nil, "", "", false)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := completeLedgerCommand(ctx, t.Tx, operation, request.IdempotencyKey, created.ID, created); err != nil {
		return ledger.Transaction{}, err
	}
	return created, nil
}

func (s *Store) Reverse(ctx context.Context, key, originalID, reason, referenceID, actorID string, hash [32]byte) (ledger.Transaction, error) {
	var result ledger.Transaction
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		var err error
		result, err = tx.Reverse(ctx, key, originalID, reason, referenceID, actorID, hash)
		return err
	})
	return result, err
}

func (t *LedgerTransaction) Reverse(ctx context.Context, key, originalID, reason, referenceID, actorID string, hash [32]byte) (ledger.Transaction, error) {
	return t.reverse(ctx, key, originalID, reason, referenceID, actorID, hash, false)
}

// ReverseSystem is intentionally available only inside the PostgreSQL
// implementation. It allows an automatic delivery compensation to strictly
// negate one sealed settlement transaction without depending on a currently
// available human administrator. It cannot create arbitrary postings.
func (t *LedgerTransaction) ReverseSystem(ctx context.Context, key, originalID, reason, referenceID string, hash [32]byte) (ledger.Transaction, error) {
	return t.reverse(ctx, key, originalID, reason, referenceID, "", hash, true)
}

func (t *LedgerTransaction) reverse(ctx context.Context, key, originalID, reason, referenceID, actorID string, hash [32]byte, systemAuthorized bool) (ledger.Transaction, error) {
	if systemAuthorized && actorID != "" {
		return ledger.Transaction{}, ledger.ErrInvalidInput
	}
	operation := "transaction:reversal"
	_, snapshot, replay, err := reserveLedgerCommand(ctx, t.Tx, key, operation, hash)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if replay {
		if !systemAuthorized {
			if err := requireActiveAdminLocked(ctx, t.Tx, actorID); err != nil {
				return ledger.Transaction{}, err
			}
		}
		return decodeCommandResult[ledger.Transaction](snapshot)
	}
	var originalKind, originalReferenceType string
	if err := t.QueryRow(ctx, `SELECT kind, reference_type FROM ledger_transactions WHERE id = $1 AND sealed FOR UPDATE`, originalID).Scan(&originalKind, &originalReferenceType); err != nil {
		return ledger.Transaction{}, mapLedgerError(err)
	}
	if ledger.TransactionKind(originalKind) == ledger.TransactionReversal {
		return ledger.Transaction{}, ledger.ErrInvalidInput
	}
	if originalReferenceType == "api_call" && !systemAuthorized {
		return ledger.Transaction{}, ledger.ErrInvalidInput
	}
	if !systemAuthorized {
		var gatewaySettlement bool
		if err := t.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM api_call_settlements
				WHERE capture_transaction_id = $1 OR self_transaction_id = $1
			)`, originalID).Scan(&gatewaySettlement); err != nil {
			return ledger.Transaction{}, mapLedgerError(err)
		}
		if gatewaySettlement {
			return ledger.Transaction{}, ledger.ErrInvalidInput
		}
	}
	var alreadyReversed bool
	if err := t.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ledger_transactions WHERE reversal_of_transaction_id = $1)`, originalID).Scan(&alreadyReversed); err != nil {
		return ledger.Transaction{}, err
	}
	if alreadyReversed {
		return ledger.Transaction{}, ledger.ErrConflict
	}
	original, err := loadTransaction(ctx, t.Tx, originalID)
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
		postings = append(postings, ledger.Posting{Account: ref, BusinessRole: ledger.EntryRoleReversal, Amount: amount})
	}
	request := ledger.PostRequest{
		IdempotencyKey: key, Kind: ledger.TransactionReversal, Reason: reason,
		ReferenceType: "reversal", ReferenceID: referenceID, ActorAccountID: actorID,
		ReversalOfTransactionID: originalID, Entries: postings,
	}
	created, err := postLocked(ctx, t.Tx, operation, request, nil, "", "", systemAuthorized)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := completeLedgerCommand(ctx, t.Tx, operation, key, created.ID, created); err != nil {
		return ledger.Transaction{}, err
	}
	return created, nil
}

func postLocked(ctx context.Context, tx pgx.Tx, operation string, request ledger.PostRequest, prelocked map[string]ledgerAccountState, reservedDebitAccountID, holdID string, systemReversal bool) (ledger.Transaction, error) {
	resolved, states, err := resolveAndLockPostings(ctx, tx, request.Entries, prelocked)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if request.Kind == ledger.TransactionAdjustment || request.Kind == ledger.TransactionBadDebt || (request.Kind == ledger.TransactionReversal && !systemReversal) {
		if err := requireActiveAdminLocked(ctx, tx, request.ActorAccountID); err != nil {
			return ledger.Transaction{}, err
		}
	}
	if err := validateSystemAccountUse(request.Kind, resolved, states); err != nil {
		return ledger.Transaction{}, err
	}
	finalBalances, err := validatePostingChanges(request.Kind, resolved, states, reservedDebitAccountID)
	if err != nil {
		return ledger.Transaction{}, err
	}
	var transactionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO ledger_transactions (command_operation, idempotency_key, kind, reason, reference_type, reference_id, actor_account_id, reversal_of_transaction_id, hold_id)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, '')::uuid, NULLIF($8, '')::uuid, NULLIF($9, '')::uuid)
		RETURNING id::text`, operation, request.IdempotencyKey, request.Kind, request.Reason, request.ReferenceType, request.ReferenceID, request.ActorAccountID, request.ReversalOfTransactionID, holdID).Scan(&transactionID); err != nil {
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
			INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, business_role, amount_nano, posted_balance_before_nano, posted_balance_after_nano)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, transactionID, posting.ledgerAccountID, index+1, posting.businessRole, posting.amount.Nano(), before.Nano(), after.Nano()); err != nil {
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
	return loadTransaction(ctx, tx, transactionID)
}

func requireActiveAdminLocked(ctx context.Context, tx pgx.Tx, actorID string) error {
	if actorID == "" {
		return identity.ErrForbidden
	}
	var isAdmin bool
	var status string
	if err := tx.QueryRow(ctx, `SELECT is_admin, status FROM accounts WHERE id = $1 FOR UPDATE`, actorID).Scan(&isAdmin, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.ErrForbidden
		}
		return mapLedgerError(err)
	}
	if !isAdmin || identity.Status(status) != identity.StatusActive {
		return identity.ErrForbidden
	}
	return nil
}

type resolvedPosting struct {
	ledgerAccountID string
	businessRole    ledger.EntryRole
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
		resolved[index] = resolvedPosting{ledgerAccountID: id, businessRole: posting.BusinessRole, amount: posting.Amount}
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

func validatePostingChanges(kind ledger.TransactionKind, postings []resolvedPosting, states map[string]ledgerAccountState, reservedDebitAccountID string) (map[string]money.Amount, error) {
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
		if balance.Nano() == math.MinInt64 {
			return nil, ledger.ErrAmountOverflow
		}
		if balance < state.postedBalance && id != reservedDebitAccountID {
			if state.kind == ledger.AccountIncentive && balance < 0 && kind != ledger.TransactionReversal {
				return nil, ledger.ErrInsufficientFunds
			}
			if state.kind == ledger.AccountUser && kind != ledger.TransactionReversal {
				if state.status != identity.StatusActive {
					return nil, identity.ErrForbidden
				}
				if state.creditFrozen {
					return nil, ledger.ErrCreditFrozen
				}
				if ledger.ExceedsSpendableCapacity(balance, state.creditLimit, state.assetReserved, state.spendAuthorized) {
					return nil, ledger.ErrInsufficientFunds
				}
			}
		}
		final[id] = balance
	}
	return final, nil
}

func reserveLedgerCommand(ctx context.Context, tx pgx.Tx, key, operation string, hash [32]byte) (string, []byte, bool, error) {
	var insertedKey string
	err := tx.QueryRow(ctx, `
		INSERT INTO ledger_commands (idempotency_key, operation, payload_hash)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING
		RETURNING idempotency_key`, key, operation, hash[:]).Scan(&insertedKey)
	if err == nil {
		return "", nil, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, mapLedgerError(err)
	}
	var existingOperation, resultID string
	var existingHash, snapshot []byte
	if err := tx.QueryRow(ctx, `
		SELECT operation, payload_hash, COALESCE(result_id::text, ''), result_payload
		FROM ledger_commands WHERE operation = $1 AND idempotency_key = $2`, operation, key).Scan(&existingOperation, &existingHash, &resultID, &snapshot); err != nil {
		return "", nil, false, mapLedgerError(err)
	}
	if existingOperation != operation || !bytes.Equal(existingHash, hash[:]) || resultID == "" || len(snapshot) == 0 {
		return "", nil, false, ledger.ErrConflict
	}
	return resultID, snapshot, true, nil
}

func completeLedgerCommand(ctx context.Context, tx pgx.Tx, operation, key, resultID string, result any) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode ledger command result: %w", err)
	}
	command, err := tx.Exec(ctx, `
		UPDATE ledger_commands
		SET result_id = $3, result_payload = $4, completed_at = now()
		WHERE operation = $1 AND idempotency_key = $2
		  AND result_id IS NULL AND result_payload IS NULL AND completed_at IS NULL`,
		operation, key, resultID, payload)
	if err != nil {
		return mapLedgerError(err)
	}
	if command.RowsAffected() != 1 {
		return ledger.ErrConflict
	}
	return nil
}

func decodeCommandResult[T any](snapshot []byte) (T, error) {
	var result T
	if len(snapshot) == 0 || json.Unmarshal(snapshot, &result) != nil {
		return result, ledger.ErrConflict
	}
	return result, nil
}

func loadTransaction(ctx context.Context, queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, id string) (ledger.Transaction, error) {
	var result ledger.Transaction
	var kind string
	err := queryer.QueryRow(ctx, `
		SELECT id::text, idempotency_key, kind, reason, reference_type, reference_id,
		       COALESCE(actor_account_id::text, ''), COALESCE(reversal_of_transaction_id::text, ''),
		       COALESCE(hold_id::text, ''), created_at
		FROM ledger_transactions WHERE id = $1 AND sealed`, id).Scan(
		&result.ID, &result.IdempotencyKey, &kind, &result.Reason, &result.ReferenceType,
		&result.ReferenceID, &result.ActorAccountID, &result.ReversalOfTransactionID, &result.HoldID, &result.CreatedAt,
	)
	if err != nil {
		return ledger.Transaction{}, mapLedgerError(err)
	}
	result.Kind = ledger.TransactionKind(kind)
	rows, err := queryer.Query(ctx, `
		SELECT e.id, e.transaction_id::text, e.ledger_account_id::text, la.kind,
		       COALESCE(la.identity_account_id::text, ''), e.entry_ordinal, e.business_role, e.amount_nano,
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
	if err := rows.Err(); err != nil {
		return ledger.Transaction{}, err
	}
	for index := range result.Entries {
		entry := &result.Entries[index]
		entry.TransactionKind = result.Kind
		entry.Reason = result.Reason
		entry.ReferenceType = result.ReferenceType
		entry.ReferenceID = result.ReferenceID
		entry.ActorAccountID = result.ActorAccountID
		entry.ReversalOfTransactionID = result.ReversalOfTransactionID
		entry.HoldID = result.HoldID
		for counterpartyIndex := range result.Entries {
			if counterpartyIndex == index {
				continue
			}
			counterparty := result.Entries[counterpartyIndex]
			entry.Counterparties = append(entry.Counterparties, ledger.Counterparty{
				AccountKind: counterparty.AccountKind, IdentityAccountID: counterparty.IdentityAccountID,
				BusinessRole: counterparty.BusinessRole, Amount: counterparty.Amount,
			})
		}
	}
	return result, nil
}

func scanLedgerEntry(row scanner) (ledger.Entry, error) {
	var entry ledger.Entry
	var kind, businessRole string
	var amount, before, after int64
	err := row.Scan(&entry.ID, &entry.TransactionID, &entry.LedgerAccountID, &kind,
		&entry.IdentityAccountID, &entry.Ordinal, &businessRole, &amount, &before, &after, &entry.CreatedAt)
	entry.AccountKind = ledger.AccountKind(kind)
	entry.BusinessRole = ledger.EntryRole(businessRole)
	entry.Amount = money.FromNano(amount)
	entry.PostedBalanceBefore = money.FromNano(before)
	entry.PostedBalanceAfter = money.FromNano(after)
	return entry, err
}

func (s *Store) CreateHold(ctx context.Context, request ledger.CreateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	var result ledger.Hold
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		var err error
		result, err = tx.CreateHold(ctx, request, hash)
		return err
	})
	return result, err
}

func (t *LedgerTransaction) CreateHold(ctx context.Context, request ledger.CreateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	operation := "hold.create"
	_, snapshot, replay, err := reserveLedgerCommand(ctx, t.Tx, request.IdempotencyKey, operation, hash)
	if err != nil {
		return ledger.Hold{}, err
	}
	if replay {
		return decodeCommandResult[ledger.Hold](snapshot)
	}
	var ledgerAccountID string
	if err := t.QueryRow(ctx, `SELECT id::text FROM ledger_accounts WHERE identity_account_id = $1`, request.AccountID).Scan(&ledgerAccountID); err != nil {
		return ledger.Hold{}, mapLedgerError(err)
	}
	state, err := lockLedgerAccount(ctx, t.Tx, ledgerAccountID)
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
		if err != nil || ledger.ExceedsSpendableCapacity(state.postedBalance, 0, newAssetReserved, state.spendAuthorized) {
			return ledger.Hold{}, ledger.ErrInsufficientFunds
		}
	case ledger.HoldFundingCreditAllowed:
		var err error
		newSpendAuthorized, err = ledger.Add(state.spendAuthorized, request.Amount)
		if err != nil || ledger.ExceedsSpendableCapacity(state.postedBalance, state.creditLimit, state.assetReserved, newSpendAuthorized) {
			return ledger.Hold{}, ledger.ErrInsufficientFunds
		}
	default:
		return ledger.Hold{}, ledger.ErrInvalidInput
	}
	var holdID string
	if err := t.QueryRow(ctx, `
		INSERT INTO ledger_holds (
			ledger_account_id, create_idempotency_key, purpose, funding_policy, amount_nano,
			remaining_nano, reason, business_type, business_id
		) VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8)
		RETURNING id::text`, ledgerAccountID, request.IdempotencyKey, request.Purpose,
		request.FundingPolicy, request.Amount.Nano(), request.Reason, request.BusinessType, request.BusinessID).Scan(&holdID); err != nil {
		return ledger.Hold{}, mapLedgerError(err)
	}
	if _, err := t.Exec(ctx, `
		UPDATE ledger_accounts SET asset_reserved_nano = $2, spend_authorized_nano = $3, version = version + 1, updated_at = now() WHERE id = $1`, ledgerAccountID, newAssetReserved.Nano(), newSpendAuthorized.Nano()); err != nil {
		return ledger.Hold{}, err
	}
	hold, err := loadHold(ctx, t.Tx, holdID, false)
	if err != nil {
		return ledger.Hold{}, err
	}
	if err := completeLedgerCommand(ctx, t.Tx, operation, request.IdempotencyKey, hold.ID, hold); err != nil {
		return ledger.Hold{}, err
	}
	return hold, nil
}

func (s *Store) ReleaseHold(ctx context.Context, request ledger.MutateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	var result ledger.Hold
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		var err error
		result, err = tx.ReleaseHold(ctx, request, hash)
		return err
	})
	return result, err
}

func (t *LedgerTransaction) ReleaseHold(ctx context.Context, request ledger.MutateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	operation := "hold.release"
	_, snapshot, replay, err := reserveLedgerCommand(ctx, t.Tx, request.IdempotencyKey, operation, hash)
	if err != nil {
		return ledger.Hold{}, err
	}
	if replay {
		return decodeCommandResult[ledger.Hold](snapshot)
	}
	hold, err := loadHold(ctx, t.Tx, request.HoldID, true)
	if err != nil {
		return ledger.Hold{}, err
	}
	amount, err := resolveHoldAmount(request.Amount, hold)
	if err != nil {
		return ledger.Hold{}, err
	}
	state, err := lockLedgerAccount(ctx, t.Tx, hold.LedgerAccountID)
	if err != nil {
		return ledger.Hold{}, err
	}
	newReserved, err := holdReservationAfter(state, hold.Purpose, amount)
	if err != nil {
		return ledger.Hold{}, ledger.ErrConflict
	}
	if err := updateHoldReservation(ctx, t.Tx, state, hold.Purpose, newReserved); err != nil {
		return ledger.Hold{}, err
	}
	if err := updateHoldTotals(ctx, t.Tx, hold.ID, amount, false); err != nil {
		return ledger.Hold{}, err
	}
	if _, err := t.Exec(ctx, `
		INSERT INTO ledger_hold_events (hold_id, command_operation, idempotency_key, kind, business_id, amount_nano, reason)
		VALUES ($1, $2, $3, 'release', $4, $5, $6)`, hold.ID, operation, request.IdempotencyKey, request.BusinessID, amount.Nano(), request.Reason); err != nil {
		return ledger.Hold{}, mapLedgerError(err)
	}
	hold, err = loadHold(ctx, t.Tx, hold.ID, false)
	if err != nil {
		return ledger.Hold{}, err
	}
	if err := completeLedgerCommand(ctx, t.Tx, operation, request.IdempotencyKey, hold.ID, hold); err != nil {
		return ledger.Hold{}, err
	}
	return hold, nil
}

func (s *Store) CaptureHold(ctx context.Context, request ledger.CaptureHoldRequest, hash [32]byte) (ledger.CaptureResult, error) {
	var result ledger.CaptureResult
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		var err error
		result, err = tx.CaptureHold(ctx, request, hash)
		return err
	})
	return result, err
}

func (t *LedgerTransaction) CaptureHold(ctx context.Context, request ledger.CaptureHoldRequest, hash [32]byte) (ledger.CaptureResult, error) {
	operation := "hold.capture"
	_, snapshot, replay, err := reserveLedgerCommand(ctx, t.Tx, request.IdempotencyKey, operation, hash)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	if replay {
		return decodeCommandResult[ledger.CaptureResult](snapshot)
	}
	hold, err := loadHold(ctx, t.Tx, request.HoldID, true)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	amount, err := resolveHoldAmount(request.Amount, hold)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	sourceRole := ledger.EntryRoleConsumer
	if hold.Purpose == ledger.HoldPurposeAssetReservation {
		sourceRole = ledger.EntryRoleSeller
	}
	postings := make([]ledger.Posting, 1, len(request.Credits)+1)
	postings[0] = ledger.Posting{
		Account: ledger.UserAccount(hold.OwnerAccountID), BusinessRole: sourceRole, Amount: -amount,
	}
	creditTotal := money.Amount(0)
	for _, credit := range request.Credits {
		if credit.Amount <= 0 {
			return ledger.CaptureResult{}, ledger.ErrInvalidInput
		}
		creditTotal, err = ledger.Add(creditTotal, credit.Amount)
		if err != nil {
			return ledger.CaptureResult{}, err
		}
		postings = append(postings, credit)
	}
	if creditTotal != amount {
		return ledger.CaptureResult{}, ledger.ErrUnbalanced
	}
	resolved, states, err := resolveAndLockPostings(ctx, t.Tx, postings, nil)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	source := states[resolved[0].ledgerAccountID]
	providerCount, buyerCount, feeCount := 0, 0, 0
	for index, credit := range request.Credits {
		state := states[resolved[index+1].ledgerAccountID]
		if state.id == source.id || state.kind == ledger.AccountLoss {
			return ledger.CaptureResult{}, ledger.ErrInvalidInput
		}
		switch credit.BusinessRole {
		case ledger.EntryRoleProvider:
			providerCount++
			if hold.Purpose != ledger.HoldPurposeSpendAuthorization || state.kind != ledger.AccountUser {
				return ledger.CaptureResult{}, ledger.ErrInvalidInput
			}
		case ledger.EntryRoleBuyer:
			buyerCount++
			if hold.Purpose != ledger.HoldPurposeAssetReservation || state.kind != ledger.AccountUser {
				return ledger.CaptureResult{}, ledger.ErrInvalidInput
			}
		case ledger.EntryRolePlatformFee:
			feeCount++
			if state.kind != ledger.AccountIncentive {
				return ledger.CaptureResult{}, ledger.ErrInvalidInput
			}
		default:
			return ledger.CaptureResult{}, ledger.ErrInvalidInput
		}
	}
	if (hold.Purpose == ledger.HoldPurposeSpendAuthorization && (providerCount != 1 || buyerCount != 0 || feeCount > 1)) ||
		(hold.Purpose == ledger.HoldPurposeAssetReservation && (buyerCount != 1 || providerCount != 0 || feeCount != 0)) {
		return ledger.CaptureResult{}, ledger.ErrInvalidInput
	}
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
	transaction, err := postLocked(ctx, t.Tx, operation, postRequest, states, source.id, hold.ID, false)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	if err := updateHoldReservation(ctx, t.Tx, source, hold.Purpose, newReserved); err != nil {
		return ledger.CaptureResult{}, err
	}
	if err := updateHoldTotals(ctx, t.Tx, hold.ID, amount, true); err != nil {
		return ledger.CaptureResult{}, err
	}
	if _, err := t.Exec(ctx, `
		INSERT INTO ledger_hold_events (hold_id, command_operation, idempotency_key, kind, business_id, amount_nano, transaction_id, reason)
		VALUES ($1, $2, $3, 'capture', $4, $5, $6, $7)`, hold.ID, operation, request.IdempotencyKey, request.BusinessID, amount.Nano(), transaction.ID, request.Reason); err != nil {
		return ledger.CaptureResult{}, mapLedgerError(err)
	}
	hold, err = loadHold(ctx, t.Tx, hold.ID, false)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	result := ledger.CaptureResult{Hold: hold, Transaction: transaction}
	if err := completeLedgerCommand(ctx, t.Tx, operation, request.IdempotencyKey, transaction.ID, result); err != nil {
		return ledger.CaptureResult{}, err
	}
	return result, nil
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
	var result ledger.Transaction
	err := s.WithLedgerTransaction(ctx, func(tx *LedgerTransaction) error {
		var err error
		result, err = tx.TransferBadDebt(ctx, accountID, amount, key, reason, referenceID, actorID, hash)
		return err
	})
	return result, err
}

func (t *LedgerTransaction) TransferBadDebt(ctx context.Context, accountID string, amount money.Amount, key, reason, referenceID, actorID string, hash [32]byte) (ledger.Transaction, error) {
	operation := "transaction:" + string(ledger.TransactionBadDebt)
	_, snapshot, replay, err := reserveLedgerCommand(ctx, t.Tx, key, operation, hash)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if replay {
		if err := requireActiveAdminLocked(ctx, t.Tx, actorID); err != nil {
			return ledger.Transaction{}, err
		}
		return decodeCommandResult[ledger.Transaction](snapshot)
	}
	postings := []ledger.Posting{
		{Account: ledger.UserAccount(accountID), BusinessRole: ledger.EntryRoleDebtor, Amount: amount},
		{Account: ledger.SystemAccount(ledger.AccountLoss), BusinessRole: ledger.EntryRolePlatformLoss, Amount: -amount},
	}
	resolved, states, err := resolveAndLockPostings(ctx, t.Tx, postings, nil)
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
	created, err := postLocked(ctx, t.Tx, operation, request, states, "", "", false)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := completeLedgerCommand(ctx, t.Tx, operation, key, created.ID, created); err != nil {
		return ledger.Transaction{}, err
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
