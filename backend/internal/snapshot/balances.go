package snapshot

import (
	"context"
	"sync"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// balanceBook keeps the balance and credit limit of the accounts that call the
// gateway, for the check every request makes before it is let through
// (balance > -credit limit, ADR-0035 rule 2). It is a cache of the database,
// which stays the truth: bookings, ledger balances and the reconciliation
// never read it, and a stale value can only let one request through that the
// database would refuse, or refuse one it would let through, until the entry
// is corrected.
//
// Accounts are loaded one by one when a request first asks for them. Their
// values then follow the database in three ways:
//
//   - The gateway's own settlement (an api_call posting) is added to the
//     cached balance as soon as its transaction has committed, so the hot
//     path never rereads the account it has just charged.
//   - Every other posting (C2C, administrator adjustment, write-off) and every
//     change of a credit limit drops the entries concerned once it has
//     committed, before the operation returns; the next request reads the
//     account again. Those events are rare, and reading the truth again
//     corrects whatever drift there was.
//   - Every minute the entries that were used since the last round are
//     reloaded in one query and the others are forgotten, the safety net under
//     the two above (it also picks up changes made behind the process's back).
//
// Adding a settlement to a loaded value is only exact if no read of the
// database straddles the commit: a read that ran before the commit would
// bring in a value without the settlement, one that ran after it a value with
// it, and adding the settlement to the second would count it twice. Every
// entry therefore has an epoch that changes whenever a posting that touches it
// ends, and a count of the postings whose transactions are open. A load stores
// its result only if the epoch is the one it started with and no such
// transaction is open; otherwise the result is used for that one request and
// discarded. (A load that stores while a transaction is open is stopped by the
// count; one that stores after it ended, by the epoch, since it must have
// started before.) A request that arrives while an account is being charged may
// thus read the database itself, as every request did before this cache.
//
// All state is behind one mutex: the critical sections are a few loads and
// stores, and the database is never queried while it is held.
type balanceBook struct {
	// load reads the balances of the given accounts; accounts that do not
	// exist are missing from the result.
	load func(ctx context.Context, accountIDs []string) (map[string]ledger.Points, error)

	mu      sync.Mutex
	entries map[string]*balanceEntry
}

type balanceEntry struct {
	points ledger.Points
	// valid: points is current. A value that is not is read again.
	valid bool
	// epoch changes whenever a load that is in flight might have read a value
	// this entry no longer matches: when a posting that involves the account
	// ends or the entry is dropped (see above).
	epoch uint64
	// pending is the number of open transactions with an api_call posting that
	// involves the account.
	pending int
	// used: a request asked for the entry since the last periodic round.
	used bool
}

func newBalanceBook(load func(context.Context, []string) (map[string]ledger.Points, error)) *balanceBook {
	return &balanceBook{load: load, entries: map[string]*balanceEntry{}}
}

// entry returns the entry of an account, creating it; b.mu is held.
func (b *balanceBook) entry(accountID string) *balanceEntry {
	e, found := b.entries[accountID]
	if !found {
		e = &balanceEntry{}
		b.entries[accountID] = e
	}
	return e
}

// get returns the account's balance and credit limit: from memory when the
// entry is current, else from the database. A database error fails the request
// (the same as the plain read it replaces), and an unknown account is
// ledger.ErrNotFound.
func (b *balanceBook) get(ctx context.Context, accountID string) (ledger.Points, error) {
	b.mu.Lock()
	e := b.entry(accountID)
	e.used = true
	if e.valid {
		points := e.points
		b.mu.Unlock()
		return points, nil
	}
	epoch := e.epoch
	b.mu.Unlock()

	// Concurrent first requests of one account each read it; there is no
	// shared flight, so none of them can be handed a read that started before
	// a change they have seen.
	loaded, err := b.load(ctx, []string{accountID})
	if err != nil {
		return ledger.Points{}, err
	}
	points, found := loaded[accountID]
	if !found {
		return ledger.Points{}, ledger.ErrNotFound
	}
	b.mu.Lock()
	if e.epoch == epoch && e.pending == 0 {
		e.points, e.valid = points, true
	}
	b.mu.Unlock()
	return points, nil
}

// invalidate drops the entries of the accounts: the next request reads them
// again, and a read that is in flight does not store what it finds.
func (b *balanceBook) invalidate(accountIDs ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range accountIDs {
		if e, found := b.entries[id]; found {
			e.valid = false
			e.epoch++
		}
	}
}

// Posting is the ledger.Observer the persistence layer calls for every
// posting (see ledger.Observer).
//
// A call's settlement (api_call, which the repair of a call whose posting
// was missed is as well: the same transaction) is applied to the cached
// balances once its transaction has committed. Until then the accounts count
// as busy, which keeps a concurrent load from storing a value that might or
// might not include it. A transaction that rolled back changes nothing; one
// whose commit failed in an unknown way drops the entries.
//
// Every other kind of posting drops the entries concerned after its
// transaction has committed (or may have). A load that straddles the commit is
// rejected by the epoch the drop changes, and one that read the new value and
// is dropped as well only costs a read.
func (b *balanceBook) Posting(kind ledger.TransactionType, changes []ledger.Change) func(ledger.Outcome) {
	if len(changes) == 0 {
		return nil
	}
	if kind != ledger.TypeAPICall {
		return func(outcome ledger.Outcome) {
			if outcome == ledger.RolledBack {
				return
			}
			ids := make([]string, len(changes))
			for index, change := range changes {
				ids[index] = change.AccountID
			}
			b.invalidate(ids...)
		}
	}
	b.mu.Lock()
	for _, change := range changes {
		e := b.entry(change.AccountID)
		e.pending++
	}
	b.mu.Unlock()
	return func(outcome ledger.Outcome) {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, change := range changes {
			e := b.entry(change.AccountID)
			e.pending--
			e.epoch++
			switch outcome {
			case ledger.Committed:
				if !e.valid {
					break
				}
				balance, err := ledger.AddBalance(e.points.Balance, change.Amount)
				if err != nil {
					e.valid = false
					break
				}
				e.points.Balance = balance
			case ledger.Unknown:
				e.valid = false
			}
		}
	}
}

// refresh is the periodic round: the entries used since the previous round
// are read again, the rest is forgotten. An entry changed or busy meanwhile
// keeps what it has; the next round or its own events correct it. A failed
// read leaves everything as it is.
func (b *balanceBook) refresh(ctx context.Context) error {
	b.mu.Lock()
	epochs := make(map[string]uint64, len(b.entries))
	ids := make([]string, 0, len(b.entries))
	for id, e := range b.entries {
		if !e.used && e.pending == 0 {
			delete(b.entries, id)
			continue
		}
		e.used = false
		epochs[id] = e.epoch
		ids = append(ids, id)
	}
	b.mu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	loaded, err := b.load(ctx, ids)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, epoch := range epochs {
		e, found := b.entries[id]
		if !found || e.epoch != epoch || e.pending != 0 {
			continue
		}
		if points, exists := loaded[id]; exists {
			e.points, e.valid = points, true
		} else {
			delete(b.entries, id)
		}
	}
	return nil
}

// size is the number of entries, for tests.
func (b *balanceBook) size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}
