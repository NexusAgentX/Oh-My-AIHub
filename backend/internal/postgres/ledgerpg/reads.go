package ledgerpg

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// accountState is the locked or read projection of one ledger account plus the
// identity-side credit settings the posting rules need.
type accountState struct {
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

func toState(la LedgerAccount) accountState {
	state := accountState{
		id: la.ID, kind: ledger.AccountKind(la.Kind),
		postedBalance: la.PostedBalanceNano, assetReserved: la.AssetReservedNano, spendAuthorized: la.SpendAuthorizedNano,
		status: identity.StatusActive,
	}
	if la.IdentityAccountID != nil {
		state.identityAccountID = *la.IdentityAccountID
	}
	return state
}

// accountSelector maps an account reference to the (identity, system code)
// query arguments; exactly one is non-nil.
func accountSelector(account ledger.AccountRef) (identityAccountID, systemCode *string, err error) {
	if account.IdentityAccountID != "" && account.SystemKind == "" {
		return &account.IdentityAccountID, nil, nil
	}
	if account.IdentityAccountID == "" && (account.SystemKind == ledger.AccountIncentive || account.SystemKind == ledger.AccountLoss) {
		code := string(account.SystemKind)
		return nil, &code, nil
	}
	return nil, nil, ledger.ErrInvalidInput
}

func walletByRef(ctx context.Context, q *Queries, account ledger.AccountRef) (ledger.Wallet, error) {
	identityID, systemCode, err := accountSelector(account)
	if err != nil {
		return ledger.Wallet{}, err
	}
	row, err := q.GetWallet(ctx, GetWalletParams{IdentityAccountID: identityID, SystemCode: systemCode})
	if err != nil {
		return ledger.Wallet{}, MapError(err)
	}
	state := toState(row.LedgerAccount)
	state.creditLimit = money.FromNano(row.CreditLimitNano)
	state.creditFrozen = row.CreditFrozen
	state.status = identity.Status(row.AccountStatus)
	return walletFromState(state, row.LedgerAccount.UpdatedAt), nil
}

func walletFromState(state accountState, updatedAt time.Time) ledger.Wallet {
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
	}
}

type counterpartyRecord struct {
	AccountKind       string `json:"account_kind"`
	IdentityAccountID string `json:"identity_account_id"`
	BusinessRole      string `json:"business_role"`
	AmountNano        int64  `json:"amount_nano"`
}

func entriesByRef(ctx context.Context, q *Queries, account ledger.AccountRef, beforeID int64, limit int) ([]ledger.Entry, error) {
	identityID, systemCode, err := accountSelector(account)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListEntries(ctx, ListEntriesParams{
		IdentityAccountID: identityID, SystemCode: systemCode, BeforeID: beforeID, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	entries := make([]ledger.Entry, 0, limit)
	for _, row := range rows {
		entry := toEntry(row.LedgerEntry, row.AccountKind, row.IdentityAccountID)
		entry.TransactionKind = ledger.TransactionKind(row.TransactionKind)
		entry.Reason = row.TransactionReason
		entry.ReferenceType = row.ReferenceType
		entry.ReferenceID = row.ReferenceID
		entry.ActorAccountID = row.ActorAccountID
		entry.ReversalOfTransactionID = row.ReversalOfTransactionID
		entry.HoldID = row.HoldID
		var records []counterpartyRecord
		if err := json.Unmarshal(row.Counterparties, &records); err != nil {
			return nil, err
		}
		entry.Counterparties = make([]ledger.Counterparty, 0, len(records))
		for _, record := range records {
			entry.Counterparties = append(entry.Counterparties, ledger.Counterparty{
				AccountKind: ledger.AccountKind(record.AccountKind), IdentityAccountID: record.IdentityAccountID,
				BusinessRole: ledger.EntryRole(record.BusinessRole), Amount: money.FromNano(record.AmountNano),
			})
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func toEntry(e LedgerEntry, accountKind, identityAccountID string) ledger.Entry {
	return ledger.Entry{
		ID: e.ID, TransactionID: e.TransactionID, LedgerAccountID: e.LedgerAccountID,
		AccountKind: ledger.AccountKind(accountKind), IdentityAccountID: identityAccountID,
		Ordinal: int(e.EntryOrdinal), BusinessRole: ledger.EntryRole(e.BusinessRole),
		Amount: e.AmountNano, PostedBalanceBefore: e.PostedBalanceBeforeNano, PostedBalanceAfter: e.PostedBalanceAfterNano,
		CreatedAt: e.CreatedAt,
	}
}

func ledgerMetrics(ctx context.Context, q *Queries) (ledger.Metrics, error) {
	row, err := q.GetMetrics(ctx)
	if err != nil {
		return ledger.Metrics{}, err
	}
	return ledger.Metrics{
		TotalPostedBalance: NanoIntegerToPoints(row.TotalPosted), PositivePostedBalance: NanoIntegerToPoints(row.PositivePosted),
		NegativePostedBalance: NanoIntegerToPoints(row.NegativePosted), TotalCreditLimit: NanoIntegerToPoints(row.TotalCreditLimit),
		UsedCredit: NanoIntegerToPoints(row.UsedCredit), AssetReserved: NanoIntegerToPoints(row.AssetReserved), SpendAuthorized: NanoIntegerToPoints(row.SpendAuthorized),
		IncentivePostedBalance: NanoIntegerToPoints(row.IncentivePosted), LossPostedBalance: NanoIntegerToPoints(row.LossPosted),
		OverLimitAccounts: row.OverLimitAccounts, CreditFrozenAccounts: row.CreditFrozenAccounts, AccountCount: row.AccountCount,
		PostedProjectionDifference: NanoIntegerToPoints(row.PostedDifference), PostedProjectionMismatchAccounts: row.PostedMismatchAccounts,
		AssetReservationDifference: NanoIntegerToPoints(row.AssetDifference), SpendAuthorizationDifference: NanoIntegerToPoints(row.AuthorizationDifference),
		HoldProjectionMismatchAccounts: row.HoldMismatchAccounts,
	}, nil
}

// NanoIntegerToPoints renders an integer nano-point total (a PostgreSQL numeric
// aggregate that may exceed int64) as a decimal points string.
func NanoIntegerToPoints(value string) string {
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
