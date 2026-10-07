package ledgerpg

import (
	"context"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func (t *Tx) CreateHold(ctx context.Context, request ledger.CreateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	q := t.q
	operation := "hold.create"
	_, snapshot, replay, err := reserveCommand(ctx, q, request.IdempotencyKey, operation, hash)
	if err != nil {
		return ledger.Hold{}, err
	}
	if replay {
		return decodeCommandResult[ledger.Hold](snapshot)
	}
	ledgerAccountID, err := q.FindLedgerAccountIDByIdentity(ctx, &request.AccountID)
	if err != nil {
		return ledger.Hold{}, MapError(err)
	}
	state, err := lockAccount(ctx, q, ledgerAccountID)
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
	holdID, err := q.InsertHold(ctx, InsertHoldParams{
		LedgerAccountID: ledgerAccountID, CreateIdempotencyKey: request.IdempotencyKey,
		Purpose: string(request.Purpose), FundingPolicy: string(request.FundingPolicy), AmountNano: request.Amount,
		Reason: request.Reason, BusinessType: request.BusinessType, BusinessID: request.BusinessID,
	})
	if err != nil {
		return ledger.Hold{}, MapError(err)
	}
	if err := q.SetAccountReservations(ctx, SetAccountReservationsParams{
		ID: ledgerAccountID, AssetReservedNano: newAssetReserved, SpendAuthorizedNano: newSpendAuthorized,
	}); err != nil {
		return ledger.Hold{}, err
	}
	hold, err := loadHold(ctx, q, holdID, false)
	if err != nil {
		return ledger.Hold{}, err
	}
	if err := completeCommand(ctx, q, operation, request.IdempotencyKey, hold.ID, hold); err != nil {
		return ledger.Hold{}, err
	}
	return hold, nil
}

func (t *Tx) ReleaseHold(ctx context.Context, request ledger.MutateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	q := t.q
	operation := "hold.release"
	_, snapshot, replay, err := reserveCommand(ctx, q, request.IdempotencyKey, operation, hash)
	if err != nil {
		return ledger.Hold{}, err
	}
	if replay {
		return decodeCommandResult[ledger.Hold](snapshot)
	}
	hold, err := loadHold(ctx, q, request.HoldID, true)
	if err != nil {
		return ledger.Hold{}, err
	}
	amount, err := resolveHoldAmount(request.Amount, hold)
	if err != nil {
		return ledger.Hold{}, err
	}
	state, err := lockAccount(ctx, q, hold.LedgerAccountID)
	if err != nil {
		return ledger.Hold{}, err
	}
	newReserved, err := holdReservationAfter(state, hold.Purpose, amount)
	if err != nil {
		return ledger.Hold{}, ledger.ErrConflict
	}
	if err := updateHoldReservation(ctx, q, state, hold.Purpose, newReserved); err != nil {
		return ledger.Hold{}, err
	}
	if err := updateHoldTotals(ctx, q, hold.ID, amount, false); err != nil {
		return ledger.Hold{}, err
	}
	if err := q.InsertReleaseHoldEvent(ctx, InsertReleaseHoldEventParams{
		HoldID: hold.ID, CommandOperation: operation, IdempotencyKey: request.IdempotencyKey,
		BusinessID: request.BusinessID, AmountNano: amount, Reason: request.Reason,
	}); err != nil {
		return ledger.Hold{}, MapError(err)
	}
	hold, err = loadHold(ctx, q, hold.ID, false)
	if err != nil {
		return ledger.Hold{}, err
	}
	if err := completeCommand(ctx, q, operation, request.IdempotencyKey, hold.ID, hold); err != nil {
		return ledger.Hold{}, err
	}
	return hold, nil
}

func (t *Tx) CaptureHold(ctx context.Context, request ledger.CaptureHoldRequest, hash [32]byte) (ledger.CaptureResult, error) {
	q := t.q
	operation := "hold.capture"
	_, snapshot, replay, err := reserveCommand(ctx, q, request.IdempotencyKey, operation, hash)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	if replay {
		return decodeCommandResult[ledger.CaptureResult](snapshot)
	}
	hold, err := loadHold(ctx, q, request.HoldID, true)
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
	resolved, states, err := resolveAndLockPostings(ctx, q, postings, nil)
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
	transaction, err := postLocked(ctx, q, operation, postRequest, states, source.id, hold.ID, false)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	if err := updateHoldReservation(ctx, q, source, hold.Purpose, newReserved); err != nil {
		return ledger.CaptureResult{}, err
	}
	if err := updateHoldTotals(ctx, q, hold.ID, amount, true); err != nil {
		return ledger.CaptureResult{}, err
	}
	if err := q.InsertCaptureHoldEvent(ctx, InsertCaptureHoldEventParams{
		HoldID: hold.ID, CommandOperation: operation, IdempotencyKey: request.IdempotencyKey,
		BusinessID: request.BusinessID, AmountNano: amount, TransactionID: &transaction.ID, Reason: request.Reason,
	}); err != nil {
		return ledger.CaptureResult{}, MapError(err)
	}
	hold, err = loadHold(ctx, q, hold.ID, false)
	if err != nil {
		return ledger.CaptureResult{}, err
	}
	result := ledger.CaptureResult{Hold: hold, Transaction: transaction}
	if err := completeCommand(ctx, q, operation, request.IdempotencyKey, transaction.ID, result); err != nil {
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

func holdReservationAfter(state accountState, purpose ledger.HoldPurpose, amount money.Amount) (money.Amount, error) {
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

func updateHoldReservation(ctx context.Context, q *Queries, state accountState, purpose ledger.HoldPurpose, amount money.Amount) error {
	switch purpose {
	case ledger.HoldPurposeAssetReservation:
		return q.SetAssetReserved(ctx, SetAssetReservedParams{ID: state.id, AssetReservedNano: amount})
	case ledger.HoldPurposeSpendAuthorization:
		return q.SetSpendAuthorized(ctx, SetSpendAuthorizedParams{ID: state.id, SpendAuthorizedNano: amount})
	default:
		return ledger.ErrInvalidInput
	}
}

func updateHoldTotals(ctx context.Context, q *Queries, holdID string, amount money.Amount, capture bool) error {
	captured, released := money.Amount(0), amount
	if capture {
		captured, released = amount, 0
	}
	affected, err := q.ApplyHoldAmount(ctx, ApplyHoldAmountParams{ID: holdID, AmountNano: amount, CapturedNano: captured, ReleasedNano: released})
	if err != nil {
		return err
	}
	if affected != 1 {
		return ledger.ErrConflict
	}
	return nil
}

func loadHold(ctx context.Context, q *Queries, id string, forUpdate bool) (ledger.Hold, error) {
	var h LedgerHold
	var owner string
	if forUpdate {
		row, err := q.GetHoldForUpdate(ctx, id)
		if err != nil {
			return ledger.Hold{}, MapError(err)
		}
		h, owner = row.LedgerHold, row.OwnerAccountID
	} else {
		row, err := q.GetHold(ctx, id)
		if err != nil {
			return ledger.Hold{}, MapError(err)
		}
		h, owner = row.LedgerHold, row.OwnerAccountID
	}
	return ledger.Hold{
		ID: h.ID, LedgerAccountID: h.LedgerAccountID, OwnerAccountID: owner,
		FundingPolicy: ledger.HoldFundingPolicy(h.FundingPolicy), Purpose: ledger.HoldPurpose(h.Purpose),
		Amount: h.AmountNano, Remaining: h.RemainingNano, Captured: h.CapturedNano, Released: h.ReleasedNano,
		Status: h.Status, BusinessType: h.BusinessType, BusinessID: h.BusinessID,
		CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt,
	}, nil
}
