package ledgerpg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func (t *Tx) reverse(ctx context.Context, key, originalID, reason, referenceID, actorID string, hash [32]byte, systemAuthorized bool) (ledger.Transaction, error) {
	if systemAuthorized && actorID != "" {
		return ledger.Transaction{}, ledger.ErrInvalidInput
	}
	q := t.q
	operation := "transaction:reversal"
	_, snapshot, replay, err := reserveCommand(ctx, q, key, operation, hash)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if replay {
		if !systemAuthorized {
			if err := requireActiveAdminLocked(ctx, q, actorID); err != nil {
				return ledger.Transaction{}, err
			}
		}
		return decodeCommandResult[ledger.Transaction](snapshot)
	}
	original, err := q.LockSealedTransaction(ctx, originalID)
	if err != nil {
		return ledger.Transaction{}, MapError(err)
	}
	if ledger.TransactionKind(original.Kind) == ledger.TransactionReversal {
		return ledger.Transaction{}, ledger.ErrInvalidInput
	}
	if original.ReferenceType == "api_call" && !systemAuthorized {
		return ledger.Transaction{}, ledger.ErrInvalidInput
	}
	if !systemAuthorized {
		gatewaySettlement, err := q.IsGatewaySettlementTransaction(ctx, &originalID)
		if err != nil {
			return ledger.Transaction{}, MapError(err)
		}
		if gatewaySettlement {
			return ledger.Transaction{}, ledger.ErrInvalidInput
		}
	}
	alreadyReversed, err := q.IsTransactionReversed(ctx, &originalID)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if alreadyReversed {
		return ledger.Transaction{}, ledger.ErrConflict
	}
	originalTransaction, err := loadTransaction(ctx, q, originalID)
	if err != nil {
		return ledger.Transaction{}, err
	}
	postings := make([]ledger.Posting, 0, len(originalTransaction.Entries))
	for _, entry := range originalTransaction.Entries {
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
	created, err := postLocked(ctx, q, operation, request, nil, "", "", systemAuthorized)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if err := completeCommand(ctx, q, operation, key, created.ID, created); err != nil {
		return ledger.Transaction{}, err
	}
	return created, nil
}

func postLocked(ctx context.Context, q *Queries, operation string, request ledger.PostRequest, prelocked map[string]accountState, reservedDebitAccountID, holdID string, systemReversal bool) (ledger.Transaction, error) {
	resolved, states, err := resolveAndLockPostings(ctx, q, request.Entries, prelocked)
	if err != nil {
		return ledger.Transaction{}, err
	}
	if request.Kind == ledger.TransactionAdjustment || request.Kind == ledger.TransactionBadDebt || (request.Kind == ledger.TransactionReversal && !systemReversal) {
		if err := requireActiveAdminLocked(ctx, q, request.ActorAccountID); err != nil {
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
	transactionID, err := q.InsertTransaction(ctx, InsertTransactionParams{
		CommandOperation: operation, IdempotencyKey: request.IdempotencyKey, Kind: string(request.Kind),
		Reason: request.Reason, ReferenceType: request.ReferenceType, ReferenceID: request.ReferenceID,
		ActorAccountID: request.ActorAccountID, ReversalOfTransactionID: request.ReversalOfTransactionID, HoldID: holdID,
	})
	if err != nil {
		return ledger.Transaction{}, MapError(err)
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
		if err := q.InsertEntry(ctx, InsertEntryParams{
			TransactionID: transactionID, LedgerAccountID: posting.ledgerAccountID, EntryOrdinal: int32(index + 1),
			BusinessRole: string(posting.businessRole), AmountNano: posting.amount,
			PostedBalanceBeforeNano: before, PostedBalanceAfterNano: after,
		}); err != nil {
			return ledger.Transaction{}, MapError(err)
		}
		running[posting.ledgerAccountID] = after
	}
	for id, balance := range finalBalances {
		if err := q.SetPostedBalance(ctx, SetPostedBalanceParams{ID: id, PostedBalanceNano: balance}); err != nil {
			return ledger.Transaction{}, err
		}
	}
	if err := q.SealTransaction(ctx, transactionID); err != nil {
		return ledger.Transaction{}, err
	}
	return loadTransaction(ctx, q, transactionID)
}

func requireActiveAdminLocked(ctx context.Context, q *Queries, actorID string) error {
	if actorID == "" {
		return identity.ErrForbidden
	}
	row, err := q.LockAccountAdminState(ctx, actorID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.ErrForbidden
		}
		return MapError(err)
	}
	if !row.IsAdmin || identity.Status(row.Status) != identity.StatusActive {
		return identity.ErrForbidden
	}
	return nil
}

type resolvedPosting struct {
	ledgerAccountID string
	businessRole    ledger.EntryRole
	amount          money.Amount
}

func resolveAndLockPostings(ctx context.Context, q *Queries, postings []ledger.Posting, prelocked map[string]accountState) ([]resolvedPosting, map[string]accountState, error) {
	resolved := make([]resolvedPosting, len(postings))
	ids := make([]string, 0, len(postings))
	for index, posting := range postings {
		var id string
		var err error
		if posting.Account.IdentityAccountID != "" {
			id, err = q.FindLedgerAccountIDByIdentity(ctx, &posting.Account.IdentityAccountID)
		} else {
			systemCode := string(posting.Account.SystemKind)
			id, err = q.FindLedgerAccountIDBySystemCode(ctx, &systemCode)
		}
		if err != nil {
			return nil, nil, MapError(err)
		}
		resolved[index] = resolvedPosting{ledgerAccountID: id, businessRole: posting.BusinessRole, amount: posting.Amount}
		ids = append(ids, id)
	}
	states, err := lockAccounts(ctx, q, ids, prelocked)
	return resolved, states, err
}

// lockAccounts takes row locks in ascending id order, the global lock order for
// every ledger writer; accounts already locked by the caller are reused.
func lockAccounts(ctx context.Context, q *Queries, ids []string, prelocked map[string]accountState) (map[string]accountState, error) {
	unique := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		unique[id] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	states := make(map[string]accountState, len(ordered))
	for id, state := range prelocked {
		states[id] = state
	}
	for _, id := range ordered {
		if _, ok := states[id]; ok {
			continue
		}
		state, err := lockAccount(ctx, q, id)
		if err != nil {
			return nil, err
		}
		states[id] = state
	}
	return states, nil
}

func lockAccount(ctx context.Context, q *Queries, id string) (accountState, error) {
	row, err := q.LockLedgerAccount(ctx, id)
	if err != nil {
		return accountState{}, MapError(err)
	}
	state := toState(row.LedgerAccount)
	if state.identityAccountID != "" {
		credit, err := q.GetAccountCreditState(ctx, state.identityAccountID)
		if err != nil {
			return accountState{}, MapError(err)
		}
		state.creditLimit = credit.CreditLimitNano
		state.creditFrozen = credit.CreditFrozen
		state.status = identity.Status(credit.Status)
	}
	return state, nil
}

func validateSystemAccountUse(kind ledger.TransactionKind, postings []resolvedPosting, states map[string]accountState) error {
	for _, posting := range postings {
		state := states[posting.ledgerAccountID]
		if state.kind == ledger.AccountLoss && kind != ledger.TransactionBadDebt && kind != ledger.TransactionReversal {
			return ledger.ErrInvalidInput
		}
	}
	return nil
}

func validatePostingChanges(kind ledger.TransactionKind, postings []resolvedPosting, states map[string]accountState, reservedDebitAccountID string) (map[string]money.Amount, error) {
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

func reserveCommand(ctx context.Context, q *Queries, key, operation string, hash [32]byte) (string, []byte, bool, error) {
	_, err := q.ReserveLedgerCommand(ctx, ReserveLedgerCommandParams{IdempotencyKey: key, Operation: operation, PayloadHash: hash[:]})
	if err == nil {
		return "", nil, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, MapError(err)
	}
	existing, err := q.GetLedgerCommand(ctx, GetLedgerCommandParams{Operation: operation, IdempotencyKey: key})
	if err != nil {
		return "", nil, false, MapError(err)
	}
	if existing.Operation != operation || !bytes.Equal(existing.PayloadHash, hash[:]) || existing.ResultID == "" || len(existing.ResultPayload) == 0 {
		return "", nil, false, ledger.ErrConflict
	}
	return existing.ResultID, existing.ResultPayload, true, nil
}

func completeCommand(ctx context.Context, q *Queries, operation, key, resultID string, result any) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode ledger command result: %w", err)
	}
	affected, err := q.CompleteLedgerCommand(ctx, CompleteLedgerCommandParams{
		ResultID: &resultID, ResultPayload: payload, Operation: operation, IdempotencyKey: key,
	})
	if err != nil {
		return MapError(err)
	}
	if affected != 1 {
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

func loadTransaction(ctx context.Context, q *Queries, id string) (ledger.Transaction, error) {
	row, err := q.GetSealedTransaction(ctx, id)
	if err != nil {
		return ledger.Transaction{}, MapError(err)
	}
	t := row.LedgerTransaction
	result := ledger.Transaction{
		ID: t.ID, IdempotencyKey: t.IdempotencyKey, Kind: ledger.TransactionKind(t.Kind), Reason: t.Reason,
		ReferenceType: t.ReferenceType, ReferenceID: t.ReferenceID,
		ActorAccountID: deref(t.ActorAccountID), ReversalOfTransactionID: deref(t.ReversalOfTransactionID),
		HoldID: deref(t.HoldID), CreatedAt: t.CreatedAt,
	}
	rows, err := q.ListTransactionEntries(ctx, id)
	if err != nil {
		return ledger.Transaction{}, err
	}
	for _, entryRow := range rows {
		result.Entries = append(result.Entries, toEntry(entryRow.LedgerEntry, entryRow.AccountKind, entryRow.IdentityAccountID))
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

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
