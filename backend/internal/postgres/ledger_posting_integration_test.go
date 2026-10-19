package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
)

// These tests are about how Post behaves when many postings over the same
// accounts are in flight at once, which is what a busy gateway does: every API
// call locks the platform_revenue row until its transaction commits (Epic #360,
// G8.7 shortened the time it is held). Whatever order the postings arrive in and
// whichever accounts they share, the ledger must end up with balances equal to
// the sum of the entries, a zero total, a continuous balance_after per account
// and one booking per idempotency key.

func postWithin(ctx context.Context, pool *pgxpool.Pool, transaction ledger.Transaction, opts ...ledgerpg.Option) (ledger.Posted, error) {
	var posted ledger.Posted
	err := ledgerpg.NewBooks().InTx(ctx, pool, func(tx ledgerpg.Tx) error {
		var err error
		posted, err = ledgerpg.Post(ctx, tx, transaction, opts...)
		return err
	})
	return posted, err
}

// randomPosting is a balanced transaction over accounts drawn from the given
// ones: one payer and one to three receivers, the lines in a random order, so
// that the order of the lines is not the order in which the accounts are locked.
func randomPosting(random *rand.Rand, key string, accounts []ledger.AccountRef) ledger.Transaction {
	payer := accounts[random.Intn(len(accounts))]
	receivers := 1 + random.Intn(3)
	lines := make([]ledger.Line, 0, receivers+1)
	var total int64
	used := map[ledger.AccountRef]bool{payer: true}
	for len(lines) < receivers {
		receiver := accounts[random.Intn(len(accounts))]
		if used[receiver] {
			continue
		}
		used[receiver] = true
		amount := int64(random.Intn(900_000_000) + 1)
		total += amount
		lines = append(lines, ledger.Line{Account: receiver, Amount: money.FromNano(amount)})
	}
	lines = append(lines, ledger.Line{Account: payer, Amount: money.FromNano(-total)})
	random.Shuffle(len(lines), func(i, j int) { lines[i], lines[j] = lines[j], lines[i] })
	return ledger.Transaction{Type: ledger.TypeAdminAdjust, IdempotencyKey: key, Entries: lines}
}

func TestManyPostingsOverTheSameAccountsNeverDeadlockAndStayConsistent(t *testing.T) {
	pool, store := isolatedDatabase(t)
	_, _, members := accounts(t, store, "0", "0", "0", "0", "0")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Five users and the three system accounts: with at most four lines per
	// posting, nearly every pair of postings overlaps.
	var refs []ledger.AccountRef
	for _, member := range members {
		refs = append(refs, ledger.User(member.ID))
	}
	refs = append(refs, ledger.System(ledger.SystemPlatformRevenue), ledger.System(ledger.SystemC2CEscrow), ledger.System(ledger.SystemBadDebt))

	const workers, perWorker = 12, 30
	type booking struct {
		transaction ledger.Transaction
		posted      ledger.Posted
	}
	var (
		mu       sync.Mutex
		bookings []booking
		group    sync.WaitGroup
	)
	for worker := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			random := rand.New(rand.NewSource(int64(worker) + 1))
			for index := range perWorker {
				transaction := randomPosting(random, fmt.Sprintf("overlap-%d-%d", worker, index), refs)
				posted, err := postWithin(ctx, pool, transaction)
				if err != nil {
					t.Errorf("posting %s: %v", transaction.IdempotencyKey, err)
					return
				}
				mu.Lock()
				bookings = append(bookings, booking{transaction, posted})
				mu.Unlock()
			}
		}()
	}
	group.Wait()
	if t.Failed() {
		return
	}
	if len(bookings) != workers*perWorker {
		t.Fatalf("%d postings booked, want %d", len(bookings), workers*perWorker)
	}

	// The balances are what the postings add up to, computed here and not by the ledger.
	want := map[ledger.AccountRef]int64{}
	for _, b := range bookings {
		for _, line := range b.transaction.Entries {
			want[line.Account] += line.Amount.Nano()
		}
	}
	for _, ref := range refs {
		if got := balanceOf(t, pool, ref).Nano(); got != want[ref] {
			t.Errorf("balance of %+v = %d, the postings add up to %d", ref, got, want[ref])
		}
	}
	assertLedgerConsistent(t, pool)

	// Every posting has one entry per line, in the order of the lines, with the
	// balance after that Post reported.
	for _, b := range bookings {
		rows, err := pool.Query(ctx, `SELECT ledger_account_id, amount_nano, balance_after_nano FROM ledger_entries WHERE transaction_id = $1 ORDER BY id`, b.posted.ID)
		if err != nil {
			t.Fatal(err)
		}
		var stored []ledger.PostedEntry
		for rows.Next() {
			var entry ledger.PostedEntry
			if err := rows.Scan(&entry.LedgerAccountID, &entry.Amount, &entry.BalanceAfter); err != nil {
				t.Fatal(err)
			}
			stored = append(stored, entry)
		}
		rows.Close()
		if len(stored) != len(b.transaction.Entries) || len(b.posted.Entries) != len(stored) {
			t.Fatalf("%s: %d entries stored, %d reported, %d lines", b.transaction.IdempotencyKey, len(stored), len(b.posted.Entries), len(b.transaction.Entries))
		}
		for i, entry := range stored {
			reported := b.posted.Entries[i]
			if entry.LedgerAccountID != reported.LedgerAccountID || entry.Amount != reported.Amount || entry.BalanceAfter != reported.BalanceAfter ||
				entry.Amount != b.transaction.Entries[i].Amount || reported.Account != b.transaction.Entries[i].Account {
				t.Fatalf("%s entry %d: stored %+v, reported %+v, line %+v", b.transaction.IdempotencyKey, i, entry, reported, b.transaction.Entries[i])
			}
		}
	}

	// A posting repeated afterwards is the stored one, entry for entry, and books nothing.
	for i := 0; i < len(bookings); i += 7 {
		b := bookings[i]
		replay, err := postWithin(ctx, pool, b.transaction)
		if err != nil || !replay.Replayed || replay.ID != b.posted.ID || !reflect.DeepEqual(replay.Entries, b.posted.Entries) {
			t.Fatalf("replay of %s = %+v, %v; first %+v", b.transaction.IdempotencyKey, replay, err, b.posted)
		}
	}
	var transactions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_transactions`).Scan(&transactions); err != nil || transactions != workers*perWorker {
		t.Fatalf("transactions = %d, %v", transactions, err)
	}
	assertLedgerConsistent(t, pool)
}

func TestConcurrentRetriesOfOnePostingBookItOnce(t *testing.T) {
	pool, store := isolatedDatabase(t)
	_, _, members := accounts(t, store, "0", "0")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	consumer, provider := members[0].ID, members[1].ID
	posting := func(key string) ledger.Transaction {
		return ledger.Transaction{
			Type: ledger.TypeAPICall, IdempotencyKey: key, Related: &ledger.Related{Type: "call", ID: "00000000-0000-4000-8000-000000000001"},
			Entries: []ledger.Line{
				{Account: ledger.User(consumer), Amount: mustAmount(t, "-1.001")},
				{Account: ledger.User(provider), Amount: mustAmount(t, "1")},
				{Account: ledger.System(ledger.SystemPlatformRevenue), Amount: mustAmount(t, "0.001")},
			},
		}
	}
	// The same key with other content: another type, or another business object.
	variants := []func(*ledger.Transaction){
		func(*ledger.Transaction) {},
		func(t *ledger.Transaction) { t.Type = ledger.TypeAdminAdjust },
		func(t *ledger.Transaction) {
			t.Related = &ledger.Related{Type: "call", ID: "00000000-0000-4000-8000-000000000002"}
		},
	}

	// race sends one participant per entry of who (an index into variants) at
	// the same moment. Whoever gets to the key first books it; the others with
	// the same content are replays of it and the others conflict with it.
	race := func(key string, who []int) {
		t.Helper()
		var (
			start   = make(chan struct{})
			group   sync.WaitGroup
			results = make([]ledger.Posted, len(who))
			errs    = make([]error, len(who))
		)
		for i, variant := range who {
			group.Add(1)
			go func() {
				defer group.Done()
				transaction := posting(key)
				variants[variant](&transaction)
				<-start
				results[i], errs[i] = postWithin(ctx, pool, transaction)
			}()
		}
		close(start)
		group.Wait()

		var storedType string
		var storedRelated *string
		if err := pool.QueryRow(ctx, `SELECT type, related_id::text FROM ledger_transactions WHERE idempotency_key = $1`, key).Scan(&storedType, &storedRelated); err != nil {
			t.Fatalf("%s: the key was not booked: %v", key, err)
		}
		var won ledger.Posted
		fresh := 0
		for i, variant := range who {
			transaction := posting(key)
			variants[variant](&transaction)
			sameContent := string(transaction.Type) == storedType && storedRelated != nil && transaction.Related.ID == *storedRelated
			switch {
			case sameContent && errs[i] != nil:
				t.Errorf("%s: participant %d has the content that was booked and got %v", key, i, errs[i])
			case !sameContent && !errors.Is(errs[i], ledger.ErrConflict):
				t.Errorf("%s: participant %d has other content than what was booked and got %v, want a conflict", key, i, errs[i])
			case sameContent:
				if !results[i].Replayed {
					fresh++
				}
				if won.ID == "" {
					won = results[i]
				}
				if results[i].ID != won.ID || !reflect.DeepEqual(results[i].Entries, won.Entries) {
					t.Errorf("%s: participants disagree about what was booked: %+v and %+v", key, results[i], won)
				}
			}
		}
		if fresh != 1 {
			t.Errorf("%s: %d participants booked it, want exactly one", key, fresh)
		}
		var transactions, entries int
		if err := pool.QueryRow(ctx, `SELECT count(*), (SELECT count(*) FROM ledger_entries e WHERE e.transaction_id = (SELECT id FROM ledger_transactions WHERE idempotency_key = $1)) FROM ledger_transactions WHERE idempotency_key = $1`, key).Scan(&transactions, &entries); err != nil || transactions != 1 || entries != 3 {
			t.Errorf("%s: %d transactions with %d entries, %v", key, transactions, entries, err)
		}
	}
	race("retried", []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	race("contended", []int{0, 1, 2, 0, 1, 2, 0, 1, 2, 0, 1, 2})
	race("contended-again", []int{2, 2, 1, 1, 0, 0, 2, 1, 0, 2, 1, 0})

	// Three keys, each booked once: three times the lines, not more.
	if balanceOf(t, pool, ledger.User(consumer)) != mustAmount(t, "-3.003") || balanceOf(t, pool, ledger.User(provider)) != mustAmount(t, "3") ||
		balanceOf(t, pool, ledger.System(ledger.SystemPlatformRevenue)) != mustAmount(t, "0.003") {
		t.Fatal("the retries booked a posting more than once")
	}
	assertLedgerConsistent(t, pool)
}

func TestKeyReusedForAnotherPostingConflictsAfterwards(t *testing.T) {
	pool, store := isolatedDatabase(t)
	_, _, members := accounts(t, store, "0", "0")
	ctx := context.Background()
	consumer, provider := members[0].ID, members[1].ID
	original := ledger.Transaction{
		Type: ledger.TypeAPICall, IdempotencyKey: "reused", Related: &ledger.Related{Type: "call", ID: "00000000-0000-4000-8000-000000000001"},
		Entries: []ledger.Line{{Account: ledger.User(consumer), Amount: -5}, {Account: ledger.User(provider), Amount: 5}},
	}
	first, err := postWithin(ctx, pool, original)
	if err != nil || first.Replayed {
		t.Fatalf("first = %+v, %v", first, err)
	}
	for name, changed := range map[string]func(*ledger.Transaction){
		"type": func(t *ledger.Transaction) { t.Type = ledger.TypeAdminAdjust },
		"related id": func(t *ledger.Transaction) {
			t.Related = &ledger.Related{Type: "call", ID: "00000000-0000-4000-8000-000000000002"}
		},
		"related type": func(t *ledger.Transaction) {
			t.Related = &ledger.Related{Type: "account", ID: "00000000-0000-4000-8000-000000000001"}
		},
		"no relation": func(t *ledger.Transaction) { t.Related = nil },
	} {
		other := original
		changed(&other)
		if _, err := postWithin(ctx, pool, other); !errors.Is(err, ledger.ErrConflict) {
			t.Errorf("same key with another %s: %v, want a conflict", name, err)
		}
	}
	// Other lines under the same key and the same identity are a replay of the
	// first posting: the key says what was booked, not the lines of the retry.
	retried := original
	retried.Entries = []ledger.Line{{Account: ledger.User(consumer), Amount: -9}, {Account: ledger.User(provider), Amount: 9}}
	replay, err := postWithin(ctx, pool, retried)
	if err != nil || !replay.Replayed || replay.ID != first.ID || !reflect.DeepEqual(replay.Entries, first.Entries) {
		t.Fatalf("retry with other amounts = %+v, %v", replay, err)
	}
	if balanceOf(t, pool, ledger.User(consumer)) != -5 || balanceOf(t, pool, ledger.User(provider)) != 5 {
		t.Fatal("a conflicting or repeated posting changed a balance")
	}
	assertLedgerConsistent(t, pool)
}

func TestWorkBeforeTheLockRunsBeforeTheAccountsAreLocked(t *testing.T) {
	pool, store := isolatedDatabase(t)
	_, _, members := accounts(t, store, "0", "0")
	ctx := context.Background()
	consumer, provider := members[0].ID, members[1].ID
	transaction := ledger.Transaction{
		Type: ledger.TypeAPICall, IdempotencyKey: "hooked",
		Entries: []ledger.Line{
			{Account: ledger.User(consumer), Amount: -11},
			{Account: ledger.User(provider), Amount: 10},
			{Account: ledger.System(ledger.SystemPlatformRevenue), Amount: 1},
		},
	}
	// From another connection: is the row of platform_revenue taken?
	taken := func() bool {
		_, err := pool.Exec(ctx, `SELECT 1 FROM ledger_accounts WHERE system_code = 'platform_revenue' FOR UPDATE NOWAIT`)
		if err == nil {
			return false
		}
		if !isSQLState(err, "55P03") {
			t.Fatalf("probing the lock: %v", err)
		}
		return true
	}

	var hookID string
	var hookCalls int
	var posted ledger.Posted
	err := ledgerpg.NewBooks().InTx(ctx, pool, func(tx ledgerpg.Tx) error {
		var err error
		posted, err = ledgerpg.Post(ctx, tx, transaction, ledgerpg.BeforeLock(func(ctx context.Context, transactionID string) error {
			hookCalls++
			hookID = transactionID
			if taken() {
				t.Error("the accounts were locked when the work before the lock ran")
			}
			// The transaction row exists already, in this transaction.
			var found string
			if err := tx.QueryRow(ctx, `SELECT id FROM ledger_transactions WHERE idempotency_key = 'hooked'`).Scan(&found); err != nil || found != transactionID {
				t.Errorf("transaction row seen by the hook = %q, %v; hook was given %q", found, err, transactionID)
			}
			return nil
		}))
		if err != nil {
			return err
		}
		if !taken() {
			t.Error("the accounts are not locked after Post")
		}
		return nil
	})
	if err != nil || hookCalls != 1 || hookID == "" || hookID != posted.ID {
		t.Fatalf("post = %+v, %v; hook ran %d times with %q", posted, err, hookCalls, hookID)
	}
	if taken() {
		t.Fatal("the accounts are still locked after the commit")
	}

	// A replay runs the work too, with the stored transaction's ID, and books nothing.
	hookID, hookCalls = "", 0
	replay, err := postWithin(ctx, pool, transaction, ledgerpg.BeforeLock(func(_ context.Context, transactionID string) error {
		hookCalls++
		hookID = transactionID
		return nil
	}))
	if err != nil || !replay.Replayed || replay.ID != posted.ID || hookCalls != 1 || hookID != posted.ID {
		t.Fatalf("replay = %+v, %v; hook ran %d times with %q", replay, err, hookCalls, hookID)
	}

	// An error of the work ends the posting, on a first posting and on a replay.
	failure := errors.New("the work before the lock failed")
	fail := ledgerpg.BeforeLock(func(context.Context, string) error { return failure })
	fresh := transaction
	fresh.IdempotencyKey = "hooked-failed"
	if _, err := postWithin(ctx, pool, fresh, fail); !errors.Is(err, failure) {
		t.Fatalf("failing work on a first posting = %v", err)
	}
	if _, err := postWithin(ctx, pool, transaction, fail); !errors.Is(err, failure) {
		t.Fatalf("failing work on a replay = %v", err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'hooked-failed'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("a posting whose work failed left %d transaction rows, %v", rows, err)
	}
	if taken() {
		t.Fatal("a failed posting left the accounts locked")
	}
	if balanceOf(t, pool, ledger.User(consumer)) != -11 || balanceOf(t, pool, ledger.User(provider)) != 10 || balanceOf(t, pool, ledger.System(ledger.SystemPlatformRevenue)) != 1 {
		t.Fatal("balances changed by postings that did not commit or that were replays")
	}
	assertLedgerConsistent(t, pool)
}

func TestAPostingThatCannotBeBookedLeavesNothingBehind(t *testing.T) {
	pool, store := isolatedDatabase(t)
	_, _, members := accounts(t, store, "0", "0")
	ctx := context.Background()
	rich, other := members[0].ID, members[1].ID
	fund := ledger.Transaction{Type: ledger.TypeAdminAdjust, IdempotencyKey: "fund", Entries: []ledger.Line{
		{Account: ledger.User(rich), Amount: money.FromNano(1<<62 + 1<<61)}, {Account: ledger.System(ledger.SystemBadDebt), Amount: money.FromNano(-(1<<62 + 1<<61))},
	}}
	if _, err := postWithin(ctx, pool, fund); err != nil {
		t.Fatal(err)
	}
	// The balance would overflow int64: refused before anything is written.
	overflow := ledger.Transaction{Type: ledger.TypeAdminAdjust, IdempotencyKey: "overflow", Entries: []ledger.Line{
		{Account: ledger.User(rich), Amount: money.FromNano(1 << 62)}, {Account: ledger.User(other), Amount: money.FromNano(-(1 << 62))},
	}}
	if _, err := postWithin(ctx, pool, overflow); !errors.Is(err, ledger.ErrAmountOverflow) {
		t.Fatalf("overflowing posting = %v", err)
	}
	// An account the ledger does not have.
	missing := ledger.Transaction{Type: ledger.TypeAdminAdjust, IdempotencyKey: "missing", Entries: []ledger.Line{
		{Account: ledger.User("00000000-0000-4000-8000-0000000000ff"), Amount: 1}, {Account: ledger.User(other), Amount: -1},
	}}
	if _, err := postWithin(ctx, pool, missing); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("posting to an unknown account = %v", err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_transactions WHERE idempotency_key IN ('overflow', 'missing')`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("refused postings left %d transaction rows, %v", rows, err)
	}
	assertLedgerConsistent(t, pool)
}

// Every call is settled by a transaction that closes the call, books it and
// points the call at the booking. The gateway does this once per request at
// whatever rate the requests arrive, and a call can be finished more than
// once at the same moment (a timeout racing the end of the answer).
func TestConcurrentSettlementsBookEveryCallOnceAndLinkIt(t *testing.T) {
	e := newGroupEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	consumers := []person{e.member("consumer0", "100"), e.member("consumer1", "100"), e.member("consumer2", "100")}
	providers := []person{e.member("sharer0", "0"), e.member("sharer1", "0")}
	const calls, finishers = 120, 3

	prepared := make([]gateway.CallFinish, calls)
	expected := map[string]int64{} // account id (or "platform_revenue") -> what the calls add up to
	for i := range calls {
		consumer, provider := consumers[i%len(consumers)], providers[i%len(providers)]
		id, err := e.store.Gateway.InsertCall(ctx, gateway.CallInsert{AccountID: consumer.id, RequestedModel: "gpt-test", Format: channel.FormatOpenAIChat, Outcome: gateway.OutcomeInProgress})
		if err != nil {
			t.Fatal(err)
		}
		cost, fee := money.FromNano(int64(1_000_000+i)), money.FromNano(int64(1_000+i))
		prepared[i] = gateway.CallFinish{
			ID: id, AccountID: consumer.id, Outcome: gateway.OutcomeSucceeded, ModelID: "gpt-test", Cost: cost, Fee: fee,
			Bill: &gateway.Billing{ConsumerID: consumer.id, ProviderID: provider.id, Cost: cost, Fee: fee},
		}
		expected[consumer.id] -= (cost + fee).Nano()
		expected[provider.id] += cost.Nano()
		expected["platform_revenue"] += fee.Nano()
	}

	var (
		group    sync.WaitGroup
		finished atomic.Int64
		start    = make(chan struct{})
	)
	for _, finish := range prepared {
		for range finishers {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				result, err := e.store.Gateway.FinishCall(ctx, finish)
				if err != nil {
					t.Errorf("finishing %s: %v", finish.ID, err)
					return
				}
				if result.Finished {
					finished.Add(1)
					if result.TxID == "" {
						t.Errorf("finishing %s booked nothing", finish.ID)
					}
				}
			}()
		}
	}
	close(start)
	group.Wait()
	if t.Failed() {
		return
	}
	if finished.Load() != calls {
		t.Fatalf("%d finishes took effect, want one per call (%d)", finished.Load(), calls)
	}

	var transactions, linked, entries int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_transactions WHERE type = 'api_call'`).Scan(&transactions); err != nil || transactions != calls {
		t.Fatalf("transactions = %d, %v", transactions, err)
	}
	if err := e.pool.QueryRow(ctx, `
		SELECT count(*) FROM calls c JOIN ledger_transactions t ON t.id = c.ledger_tx_id AND t.idempotency_key = 'call:' || c.id::text AND t.related_id = c.id
		WHERE c.outcome = 'succeeded'`).Scan(&linked); err != nil || linked != calls {
		t.Fatalf("calls linked to the transaction booked for them = %d, %v", linked, err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries`).Scan(&entries); err != nil || entries != 3*calls {
		t.Fatalf("entries = %d, %v", entries, err)
	}
	for _, who := range append(append([]person{}, consumers...), providers...) {
		var got int64
		if err := e.pool.QueryRow(ctx, `SELECT balance_nano FROM ledger_accounts WHERE account_id = $1`, who.id).Scan(&got); err != nil || got != expected[who.id] {
			t.Errorf("balance of %s = %d, the calls add up to %d, %v", who.username, got, expected[who.id], err)
		}
	}
	if e.systemBalance("platform_revenue") != fmt.Sprint(expected["platform_revenue"]) {
		t.Errorf("platform_revenue = %s, the fees add up to %d", e.systemBalance("platform_revenue"), expected["platform_revenue"])
	}
	e.assertZeroSum()
	assertLedgerConsistent(t, e.pool)
}
