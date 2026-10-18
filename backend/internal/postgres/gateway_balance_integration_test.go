package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaybench"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
)

// The gateway checks balance and credit limit from process memory (ADR-0040,
// G8.2). These tests run the whole stack on the cached stores, as the server
// does, with no background reload: whatever the next gateway request sees
// after a booking or a credit-limit change can only come from what the cache
// was told when that operation committed.

// gatewayPoints is the balance and credit limit the gateway's check sees.
func (e *env) gatewayPoints(p person) ledger.Points {
	e.t.Helper()
	points, err := e.cached.Gateway.Points(context.Background(), p.id)
	if err != nil {
		e.t.Fatal(err)
	}
	return points
}

// databasePoints is what the database holds.
func (e *env) databasePoints(p person) ledger.Points {
	e.t.Helper()
	points, err := e.store.Ledger.Points(context.Background(), p.id)
	if err != nil {
		e.t.Fatal(err)
	}
	return points
}

func (e *env) assertGatewaySeesTheDatabase(step string, people ...person) {
	e.t.Helper()
	for _, p := range people {
		got, want := e.gatewayPoints(p), e.databasePoints(p)
		if got.Balance != want.Balance || got.CreditLimit != want.CreditLimit {
			e.t.Errorf("%s: %s: the gateway sees balance %s, credit limit %s; the database has %s and %s",
				step, p.username, got.Balance, got.CreditLimit, want.Balance, want.CreditLimit)
		}
	}
}

func (e *env) setCreditLimit(p person, limit string) {
	e.t.Helper()
	e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+p.id, map[string]any{"credit_limit": limit})
}

func (e *env) adjustBy(p person, amount string) {
	e.t.Helper()
	e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/accounts/"+p.id+"/adjust", map[string]string{"amount": amount, "reason": "测试"})
}

// tracedEnv is an environment whose database statements are counted.
func tracedEnv(t *testing.T) (*env, *gatewaybench.QueryCounter) {
	t.Helper()
	pool, _ := isolatedDatabase(t)
	counter := gatewaybench.NewQueryCounter()
	config := pool.Config()
	config.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	return newEnvOnMode(t, traced, storepg.New(traced), nil, true), counter
}

// balanceReads is how many times the balance cache read the database (the
// tests' own look at the database is GetPoints, which is not counted here).
func balanceReads(counter *gatewaybench.QueryCounter) int64 {
	counts, _ := counter.Counts()
	return counts["ListPoints"]
}

const listing = `{"amount":"%s","unit_price_fen":92,"payment_methods":[{"channel":"支付宝","account":"a@example.com"}]}`

// Every kind of booking, and a change of the credit limit, is seen by the very
// next request: a balance that falls to the limit is refused at once, one that
// rises above it is let through at once.
func TestBookingsAndCreditLimitChangesReachTheNextRequestAtOnce(t *testing.T) {
	e := newGroupEnv(t)
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
	member := func(name, credit string) (person, probe) {
		p := e.member(name, credit)
		_, secret := e.defaultKey(p)
		return p, probe{e: e, secret: secret, upstrms: []*upstream{relay}}
	}

	t.Run("an administrator adjusts the balance", func(t *testing.T) {
		consumer, p := member("adjusted", "0")
		p.expect(t, "no balance, no credit", http.StatusPaymentRequired, false)
		e.adjustBy(consumer, "1")
		p.expect(t, "credited", http.StatusOK, true)
		e.adjustBy(consumer, "-5")
		p.expect(t, "debited below the limit", http.StatusPaymentRequired, false)
		e.adjustBy(consumer, "5")
		p.expect(t, "credited again", http.StatusOK, true)
		e.assertGatewaySeesTheDatabase("after the adjustments", consumer)
	})

	t.Run("an administrator writes off bad debt", func(t *testing.T) {
		consumer, p := member("indebted", "5")
		e.adjustBy(consumer, "-20")
		p.expect(t, "overdrawn past the credit limit", http.StatusPaymentRequired, false)
		e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/accounts/"+consumer.id+"/write-off", map[string]string{"reason": "无法收回"})
		p.expect(t, "after the write-off", http.StatusOK, true)
		e.assertGatewaySeesTheDatabase("after the write-off", consumer)
	})

	t.Run("an administrator changes the credit limit", func(t *testing.T) {
		consumer, p := member("limited", "5")
		e.adjustBy(consumer, "-3")
		p.expect(t, "inside the limit", http.StatusOK, true) // balance is now about -3.04
		e.setCreditLimit(consumer, "1")
		p.expect(t, "limit lowered below the debt", http.StatusPaymentRequired, false)
		e.setCreditLimit(consumer, "10")
		p.expect(t, "limit raised", http.StatusOK, true)
		e.assertGatewaySeesTheDatabase("after the limit changes", consumer)
	})

	t.Run("a C2C listing, its return, and a release", func(t *testing.T) {
		seller, sellerProbe := member("seller", "0")
		buyer, buyerProbe := member("buyer", "0")
		e.adjustBy(seller, "10")
		e.gatewayPoints(seller) // the entry is loaded and current: no request has used it yet
		order := e.apiJSON(seller, http.StatusCreated, http.MethodPost, "/api/c2c/orders", json.RawMessage(fmt.Sprintf(listing, "10")))["order"].(map[string]any)["id"].(string)
		sellerProbe.expect(t, "the whole balance is in escrow", http.StatusPaymentRequired, false)
		e.apiJSON(seller, http.StatusOK, http.MethodPost, "/api/c2c/orders/"+order+"/close", nil)
		sellerProbe.expect(t, "the order was closed and the points are back", http.StatusOK, true)

		buyerProbe.expect(t, "the buyer has nothing", http.StatusPaymentRequired, false)
		order = e.apiJSON(seller, http.StatusCreated, http.MethodPost, "/api/c2c/orders", json.RawMessage(fmt.Sprintf(listing, "5")))["order"].(map[string]any)["id"].(string)
		trade := e.apiJSON(buyer, http.StatusCreated, http.MethodPost, "/api/c2c/orders/"+order+"/trades", map[string]string{"amount": "5"})["trade"].(map[string]any)["id"].(string)
		e.apiJSON(buyer, http.StatusOK, http.MethodPost, "/api/c2c/trades/"+trade+"/paid", map[string]string{})
		buyerProbe.expect(t, "paid but not released", http.StatusPaymentRequired, false)
		e.apiJSON(seller, http.StatusOK, http.MethodPost, "/api/c2c/trades/"+trade+"/release", map[string]string{})
		buyerProbe.expect(t, "released to the buyer", http.StatusOK, true)
		e.assertGatewaySeesTheDatabase("after the C2C round", seller, buyer)
	})

	t.Run("an arbitration gives escrowed points to the buyer", func(t *testing.T) {
		seller, _ := member("arbitrated-seller", "0")
		buyer, buyerProbe := member("arbitrated-buyer", "0")
		e.adjustBy(seller, "5")
		order := e.apiJSON(seller, http.StatusCreated, http.MethodPost, "/api/c2c/orders", json.RawMessage(fmt.Sprintf(listing, "5")))["order"].(map[string]any)["id"].(string)
		trade := e.apiJSON(buyer, http.StatusCreated, http.MethodPost, "/api/c2c/orders/"+order+"/trades", map[string]string{"amount": "5"})["trade"].(map[string]any)["id"].(string)
		e.apiJSON(buyer, http.StatusOK, http.MethodPost, "/api/c2c/trades/"+trade+"/paid", map[string]string{})
		e.apiJSON(seller, http.StatusOK, http.MethodPost, "/api/c2c/trades/"+trade+"/dispute", map[string]string{"statement": "没有收到款项"})
		buyerProbe.expect(t, "the points are in escrow", http.StatusPaymentRequired, false)
		e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/c2c/trades/"+trade+"/resolve", map[string]string{"result": "buyer", "reason": "转账记录属实"})
		buyerProbe.expect(t, "ruled for the buyer", http.StatusOK, true)
		e.assertGatewaySeesTheDatabase("after the arbitration", seller, buyer)
	})
	e.assertZeroSum()
}

// A repaired call books its charge; the next request sees it.
func TestRepairingAMissedBookingReachesTheNextRequest(t *testing.T) {
	o := newObserved(t)
	// The consumer has called already, so its entry is loaded and current.
	before := o.gatewayPoints(o.consumer).Balance
	orphan := o.insertUnbookedCall(o.consumer, o.goodID, "succeeded", 100_000_000, 100_000)
	o.apiJSON(o.admin, http.StatusOK, http.MethodPost, "/api/admin/ledger/repair-call/"+orphan, map[string]string{"action": "charge", "reason": "记账失败补记"})
	if want := before - money.FromNano(100_100_000); o.gatewayPoints(o.consumer).Balance != want {
		t.Fatalf("the gateway sees %s after the repair, want %s", o.gatewayPoints(o.consumer).Balance, want)
	}
	o.assertGatewaySeesTheDatabase("after the repair", o.consumer, o.sharer)
	o.assertZeroSum()
}

// The gateway's own settlement is added to the cached balance of the consumer
// and of the channel owner when it commits: after warm-up the check never
// reads the database, and the cached values stay equal to the database's.
func TestSettlementsAreAddedLocallyWithoutReadingTheDatabase(t *testing.T) {
	e, counter := tracedEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
	_, secret := e.defaultKey(consumer)
	for range 3 {
		if recorder := chatRequest(e, secret); recorder.Code != http.StatusOK {
			t.Fatalf("warm-up = %d", recorder.Code)
		}
	}
	e.gatewayPoints(sharer) // the owner is cached as well

	const requests = 20
	reads := balanceReads(counter)
	before := e.databasePoints(consumer).Balance
	for range requests {
		if recorder := chatRequest(e, secret); recorder.Code != http.StatusOK {
			t.Fatalf("request = %d", recorder.Code)
		}
	}
	if got := balanceReads(counter); got != reads {
		t.Fatalf("%d requests read the balance %d times", requests, got-reads)
	}
	if spent := before - e.databasePoints(consumer).Balance; spent != money.FromNano(requests*40_040_000) {
		t.Fatalf("the consumer paid %s for %d calls", spent, requests)
	}
	e.assertGatewaySeesTheDatabase("after the calls", consumer, sharer)
	if got := balanceReads(counter); got != reads {
		t.Fatalf("looking at the cached values read the database %d times", got-reads)
	}
	e.assertZeroSum()
}

// A posting whose transaction rolls back, or whose commit fails, leaves the
// cached balances right.
func TestAPostingThatDoesNotCommitDoesNotChangeTheCachedBalances(t *testing.T) {
	e, counter := tracedEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	e.adjustBy(consumer, "10")
	ctx := context.Background()
	entries := func(key string) ledger.Transaction {
		return ledger.Transaction{
			Type: ledger.TypeAPICall, IdempotencyKey: key,
			Entries: []ledger.Line{
				{Account: ledger.User(consumer.id), Amount: money.FromNano(-5_000_000_000)},
				{Account: ledger.User(sharer.id), Amount: money.FromNano(5_000_000_000)},
			},
		}
	}
	e.gatewayPoints(consumer)
	e.gatewayPoints(sharer)
	reads := balanceReads(counter)
	want := e.databasePoints(consumer).Balance

	t.Run("rolled back", func(t *testing.T) {
		boom := errors.New("business rule failed after the posting")
		err := e.store.Books.InTx(ctx, e.pool, func(tx ledgerpg.Tx) error {
			if _, err := ledgerpg.Post(ctx, tx, entries("rollback-test")); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if got := e.databasePoints(consumer).Balance; got != want {
			t.Fatalf("the database moved: %s", got)
		}
		if got := e.gatewayPoints(consumer).Balance; got != want {
			t.Fatalf("the gateway sees %s after a rollback, want %s", got, want)
		}
		if got := balanceReads(counter); got != reads {
			t.Fatalf("a rollback made the cache read the database %d times", got-reads)
		}
	})

	t.Run("commit refused by the database", func(t *testing.T) {
		err := e.store.Books.InTx(ctx, e.pool, func(tx ledgerpg.Tx) error {
			if _, err := ledgerpg.Post(ctx, tx, entries("refused-test")); err != nil {
				return err
			}
			// An unbalanced transaction of its own: the deferred zero-sum
			// trigger refuses the whole commit.
			var txID, userLedger, revenueLedger string
			if err := tx.QueryRow(ctx, `INSERT INTO ledger_transactions (type, idempotency_key) VALUES ('admin_adjust', 'raw-unbalanced-2') RETURNING id`).Scan(&txID); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT id FROM ledger_accounts WHERE account_id = $1`, consumer.id).Scan(&userLedger); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT id FROM ledger_accounts WHERE system_code = 'platform_revenue'`).Scan(&revenueLedger); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, ledger_account_id, amount_nano, balance_after_nano)
				VALUES ($1, $2, 5, 5), ($1, $3, -4, -4)`, txID, userLedger, revenueLedger)
			return err
		})
		if !isSQLState(err, "23514") {
			t.Fatalf("commit error = %v, want a check violation", err)
		}
		if got := e.databasePoints(consumer).Balance; got != want {
			t.Fatalf("the database moved: %s", got)
		}
		// The outcome of that commit was not known to the cache, so it asks
		// again, and what it learns is the database's.
		e.assertGatewaySeesTheDatabase("after a refused commit", consumer, sharer)
		if got := e.gatewayPoints(consumer).Balance; got != want {
			t.Fatalf("the gateway sees %s after a refused commit, want %s", got, want)
		}
	})
	e.assertZeroSum()
}

// Requests, settlements, adjustments and credit-limit changes all at once: when
// the work is over, the gateway sees exactly what the database holds.
func TestBalancesFollowTheDatabaseUnderConcurrentWork(t *testing.T) {
	e := newGroupEnv(t)
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
	var consumers []person
	var secrets []string
	for index := range 3 {
		p := e.member(fmt.Sprintf("consumer%d", index), "100000")
		_, secret := e.defaultKey(p)
		consumers = append(consumers, p)
		secrets = append(secrets, secret)
	}
	people := append([]person{sharer}, consumers...)

	stop := make(chan struct{})
	var traffic sync.WaitGroup
	for _, secret := range secrets {
		for range 3 {
			traffic.Go(func() {
				for {
					select {
					case <-stop:
						return
					default:
					}
					if code := chatRequest(e, secret).Code; code != http.StatusOK {
						t.Errorf("request = %d", code)
						return
					}
				}
			})
		}
	}
	// A reader of the gateway's view next to the traffic, like the next
	// request of every account.
	traffic.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, p := range people {
				if _, err := e.cached.Gateway.Points(context.Background(), p.id); err != nil {
					t.Error(err)
					return
				}
			}
		}
	})
	for round := range 15 {
		for _, p := range consumers {
			e.adjustBy(p, "-0.01")
		}
		e.setCreditLimit(consumers[round%len(consumers)], fmt.Sprintf("%d", 100000+round))
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	traffic.Wait()

	e.assertGatewaySeesTheDatabase("after the concurrent work", people...)
	e.assertZeroSum()
}

// A change made behind the server's back is picked up by the periodic
// reload, and until then it is not seen: the cache is really what the
// requests read.
func TestPeriodicReloadPicksUpBalancesChangedBehindTheServer(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	fast := e.store.Cached(slog.New(slog.NewTextHandler(io.Discard, nil)), func(c *snapshot.Config) { c.Interval = 50 * time.Millisecond })
	ctx := context.Background()
	if err := fast.Cache.Load(ctx); err != nil {
		t.Fatal(err)
	}
	read := func() ledger.Points {
		points, err := fast.Gateway.Points(ctx, consumer.id)
		if err != nil {
			t.Fatal(err)
		}
		return points
	}
	if got := read(); got.Balance != 0 || got.CreditLimit != mustAmount(t, "100") {
		t.Fatalf("setup: %+v", got)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE ledger_accounts SET balance_nano = 7000000000 WHERE account_id = $1`, consumer.id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE accounts SET credit_limit_nano = 3000000000 WHERE id = $1`, consumer.id); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Balance != 0 {
		t.Fatal("the cache followed a change nobody announced before any reload: it is not what the requests read")
	}
	runContext, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { fast.Cache.Run(runContext); close(done) }()
	defer func() { stop(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := read(); got.Balance == mustAmount(t, "7") && got.CreditLimit == mustAmount(t, "3") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the periodic reload never picked up the change: %+v", read())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The sum of the balances is no longer zero: the test broke it on purpose.
	if _, err := e.pool.Exec(ctx, `UPDATE ledger_accounts SET balance_nano = 0 WHERE account_id = $1`, consumer.id); err != nil {
		t.Fatal(err)
	}
}
