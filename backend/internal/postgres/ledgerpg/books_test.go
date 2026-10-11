package ledgerpg

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// events is the shared log of what the fake transaction and the observers did,
// in order.
type events struct{ log []string }

func (e *events) add(entry string) { e.log = append(e.log, entry) }

type fakeTx struct {
	pgx.Tx
	events    *events
	commitErr error
}

func (t *fakeTx) Commit(context.Context) error {
	t.events.add("commit")
	return t.commitErr
}

// Rollback is what pgkit.InTx defers: pgx makes it a no-op after a commit, and
// the log records it either way.
func (t *fakeTx) Rollback(context.Context) error {
	t.events.add("rollback")
	return nil
}

type fakeDB struct {
	events    *events
	beginErr  error
	commitErr error
}

func (d fakeDB) Begin(context.Context) (pgx.Tx, error) {
	if d.beginErr != nil {
		return nil, d.beginErr
	}
	return &fakeTx{events: d.events, commitErr: d.commitErr}, nil
}

type recorder struct {
	name   string
	events *events
	ignore bool // returns no function for the outcome
}

func (r recorder) Posting(kind ledger.TransactionType, changes []ledger.Change) func(ledger.Outcome) {
	r.events.add(r.name + ":posting:" + string(kind))
	if r.ignore {
		return nil
	}
	return func(outcome ledger.Outcome) {
		r.events.add(r.name + ":ended:" + map[ledger.Outcome]string{ledger.Committed: "committed", ledger.RolledBack: "rolled back", ledger.Unknown: "unknown"}[outcome])
	}
}

func book(t *testing.T, db fakeDB, observers []recorder, fn func(tx Tx) error) error {
	t.Helper()
	books := NewBooks()
	for _, observer := range observers {
		books.Observe(observer)
	}
	return books.InTx(context.Background(), db, fn)
}

func TestObserversHearOfACommitOnlyAfterTheCommit(t *testing.T) {
	log := &events{}
	err := book(t, fakeDB{events: log}, []recorder{{name: "a", events: log}}, func(tx Tx) error {
		tx.posted(ledger.TypeAPICall, []ledger.Change{{AccountID: "x", Amount: 1}})
		log.add("fn done")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a:posting:api_call", "fn done", "commit", "rollback", "a:ended:committed"}
	if !slices.Equal(log.log, want) {
		t.Fatalf("events = %v, want %v", log.log, want)
	}
}

func TestObserversHearOfARollback(t *testing.T) {
	log := &events{}
	failure := errors.New("business rule")
	err := book(t, fakeDB{events: log}, []recorder{{name: "a", events: log}}, func(tx Tx) error {
		tx.posted(ledger.TypeC2CList, nil)
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
	want := []string{"a:posting:c2c_list", "rollback", "a:ended:rolled back"}
	if !slices.Equal(log.log, want) {
		t.Fatalf("events = %v, want %v", log.log, want)
	}
}

func TestACommitThatFailsIsOfUnknownOutcome(t *testing.T) {
	log := &events{}
	lost := errors.New("connection lost")
	err := book(t, fakeDB{events: log, commitErr: lost}, []recorder{{name: "a", events: log}}, func(tx Tx) error {
		tx.posted(ledger.TypeAPICall, nil)
		return nil
	})
	if !errors.Is(err, lost) {
		t.Fatalf("err = %v, want the commit error unchanged", err)
	}
	want := []string{"a:posting:api_call", "commit", "rollback", "a:ended:unknown"}
	if !slices.Equal(log.log, want) {
		t.Fatalf("events = %v, want %v", log.log, want)
	}
}

func TestObserversHearOfATransactionThatPanics(t *testing.T) {
	log := &events{}
	defer func() {
		if recover() == nil {
			t.Fatal("the panic was swallowed")
		}
		want := []string{"a:posting:api_call", "rollback", "a:ended:unknown"}
		if !slices.Equal(log.log, want) {
			t.Fatalf("events = %v, want %v", log.log, want)
		}
	}()
	_ = book(t, fakeDB{events: log}, []recorder{{name: "a", events: log}}, func(tx Tx) error {
		tx.posted(ledger.TypeAPICall, nil)
		panic("boom")
	})
}

func TestNothingIsReportedWhenNoTransactionBegan(t *testing.T) {
	log := &events{}
	failure := errors.New("no connection")
	err := book(t, fakeDB{events: log, beginErr: failure}, []recorder{{name: "a", events: log}}, func(Tx) error {
		t.Fatal("fn ran")
		return nil
	})
	if !errors.Is(err, failure) || len(log.log) != 0 {
		t.Fatalf("err = %v, events = %v", err, log.log)
	}
}

func TestEveryObserverIsToldInTheOrderItSubscribedAndMayIgnoreTheOutcome(t *testing.T) {
	log := &events{}
	err := book(t, fakeDB{events: log}, []recorder{{name: "a", events: log}, {name: "b", events: log, ignore: true}, {name: "c", events: log}}, func(tx Tx) error {
		tx.posted(ledger.TypeAdminAdjust, nil)
		tx.posted(ledger.TypeBadDebtWriteOff, nil)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"a:posting:admin_adjust", "b:posting:admin_adjust", "c:posting:admin_adjust",
		"a:posting:bad_debt_writeoff", "b:posting:bad_debt_writeoff", "c:posting:bad_debt_writeoff",
		"commit", "rollback",
		"a:ended:committed", "c:ended:committed", "a:ended:committed", "c:ended:committed",
	}
	if !slices.Equal(log.log, want) {
		t.Fatalf("events = %v, want %v", log.log, want)
	}
}

func TestBooksWithoutObserversJustRunTheTransaction(t *testing.T) {
	log := &events{}
	if err := NewBooks().InTx(context.Background(), fakeDB{events: log}, func(tx Tx) error {
		tx.posted(ledger.TypeAPICall, nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(log.log, []string{"commit", "rollback"}) {
		t.Fatalf("events = %v", log.log)
	}
}

func TestOnlyUserAccountsAreReported(t *testing.T) {
	changes := userChanges([]ledger.Line{
		{Account: ledger.User("a"), Amount: -7},
		{Account: ledger.User("b"), Amount: 6},
		{Account: ledger.System(ledger.SystemPlatformRevenue), Amount: 1},
	})
	want := []ledger.Change{{AccountID: "a", Amount: -7}, {AccountID: "b", Amount: 6}}
	if !slices.Equal(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
}
