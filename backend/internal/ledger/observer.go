package ledger

import "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"

// Change is what one posting does to one user account. System accounts are
// not reported: nothing keeps their balances in memory.
type Change struct {
	AccountID string
	Amount    money.Amount
}

// Outcome is how the database transaction that carried a posting ended.
type Outcome uint8

const (
	// Committed: the posting is in the database.
	Committed Outcome = iota + 1
	// RolledBack: the transaction was abandoned, nothing was written.
	RolledBack
	// Unknown: the commit failed with an error that does not say whether the
	// database applied it (a connection lost at the wrong moment).
	Unknown
)

// Observer is told about every posting the process books, so that a copy of
// the balances kept in memory (the gateway's balance cache, ADR-0040) can
// follow the database. Only the backend writes the ledger (ADR-0039), so
// being told by the persistence layer is enough to know every change.
//
// Posting is called while the posting's transaction is still open, after the
// entries have been written and before the commit, with the changes the
// posting makes to user accounts. It returns the function that is called
// exactly once when the transaction has ended, with how it ended; a nil
// function means the observer has no interest in the outcome. Posting must
// not block and must not touch the database.
type Observer interface {
	Posting(kind TransactionType, changes []Change) (ended func(Outcome))
}
