package ledgerpg

import (
	"context"
	"slices"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

// Books is how the process learns what its ledger transactions did. The
// stores that book (gateway settlement, C2C, administrator postings, the
// repair of a missed posting) begin their transactions with Books.InTx, and
// Post accepts only such a transaction, so no posting can happen without the
// observers hearing of it: the in-memory balances of the gateway (ADR-0040)
// stay behind every path that changes a balance, not just behind the ones
// someone remembered to hook.
//
// An observer is told twice per posting (see ledger.Observer): inside the
// transaction after the entries are written, and once the transaction has
// committed, rolled back or failed to commit. The second call is what makes
// "after commit" exact: Books.InTx makes it after pgx's Commit returned, not
// when Post returns.
type Books struct {
	mu        sync.RWMutex
	observers []ledger.Observer // replaced as a whole, never edited in place
}

func NewBooks() *Books { return &Books{} }

// Observe adds an observer. Every store that shares these Books reports to it
// from then on.
func (b *Books) Observe(observer ledger.Observer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.observers = append(slices.Clone(b.observers), observer)
}

func (b *Books) current() []ledger.Observer {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.observers
}

// Tx is the transaction Post books in: one that Books.InTx begun. It is a
// pgx.Tx, so the caller uses it for its own statements as well (ADR-0020).
type Tx interface {
	pgx.Tx
	posted(kind ledger.TransactionType, changes []ledger.Change)
}

type bookedTx struct {
	pgx.Tx
	books *Books
	ended []func(ledger.Outcome)
}

func (t *bookedTx) posted(kind ledger.TransactionType, changes []ledger.Change) {
	for _, observer := range t.books.current() {
		if ended := observer.Posting(kind, changes); ended != nil {
			t.ended = append(t.ended, ended)
		}
	}
}

func (t *bookedTx) finish(outcome ledger.Outcome) {
	ended := t.ended
	t.ended = nil
	for _, end := range ended {
		end(outcome)
	}
}

// InTx is pgkit.InTx for transactions that may book: it runs fn inside one
// transaction, commits when fn returns nil and rolls back otherwise, and
// afterwards tells the observers how the transaction ended. The error is the
// one pgkit.InTx returns, the commit error unchanged.
func (b *Books) InTx(ctx context.Context, db pgkit.Beginner, fn func(tx Tx) error) error {
	var (
		tx       *bookedTx
		fnErr    error
		finished bool
	)
	// If fn panics, pgkit rolls the transaction back while the panic passes
	// through; the observers must still hear that it ended. They are told
	// "unknown", which they treat as the cautious case.
	defer func() {
		if tx != nil && !finished {
			tx.finish(ledger.Unknown)
		}
	}()
	err := pgkit.InTx(ctx, db, func(raw pgx.Tx) error {
		tx = &bookedTx{Tx: raw, books: b}
		fnErr = fn(tx)
		return fnErr
	})
	if tx != nil {
		finished = true
		switch {
		case err == nil:
			tx.finish(ledger.Committed)
		case fnErr != nil:
			tx.finish(ledger.RolledBack)
		default:
			tx.finish(ledger.Unknown)
		}
	}
	return err
}
