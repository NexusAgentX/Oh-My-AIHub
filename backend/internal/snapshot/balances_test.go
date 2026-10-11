package snapshot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// bank is the database of the balance tests: it holds the balances and credit
// limits, counts reads and can hold a read back between looking at the data
// and returning it, which is how a read that straddles a commit is made.
type bank struct {
	mu        sync.Mutex
	points    map[string]ledger.Points
	reads     atomic.Int64
	err       error
	afterRead func(ids []string) // runs after the data was read, before it is returned
}

func newBank(balances map[string]int64) *bank {
	b := &bank{points: map[string]ledger.Points{}}
	for id, balance := range balances {
		b.points[id] = ledger.Points{Balance: money.FromNano(balance), CreditLimit: money.FromNano(1000)}
	}
	return b
}

func (b *bank) load(_ context.Context, ids []string) (map[string]ledger.Points, error) {
	b.reads.Add(1)
	b.mu.Lock()
	if b.err != nil {
		err := b.err
		b.mu.Unlock()
		return nil, err
	}
	result := make(map[string]ledger.Points, len(ids))
	for _, id := range ids {
		if points, found := b.points[id]; found {
			result[id] = points
		}
	}
	hook := b.afterRead
	b.mu.Unlock()
	if hook != nil {
		hook(ids)
	}
	return result, nil
}

// commit changes a balance the way a committed posting does.
func (b *bank) commit(id string, amount int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	points := b.points[id]
	points.Balance += money.FromNano(amount)
	b.points[id] = points
}

func (b *bank) balance(id string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.points[id].Balance.Nano()
}

func (b *bank) setLimit(id string, limit int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	points := b.points[id]
	points.CreditLimit = money.FromNano(limit)
	b.points[id] = points
}

func bankBook(b *bank) *balanceBook { return newBalanceBook(b.load) }

func mustGet(t *testing.T, book *balanceBook, id string) ledger.Points {
	t.Helper()
	points, err := book.get(context.Background(), id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return points
}

func charge(consumer, provider string, cost int64) []ledger.Change {
	return []ledger.Change{{AccountID: consumer, Amount: money.FromNano(-cost)}, {AccountID: provider, Amount: money.FromNano(cost)}}
}

func TestBalancesAreReadOnceAndThenServedFromMemory(t *testing.T) {
	db := newBank(map[string]int64{"a": 100})
	book := bankBook(db)
	for range 5 {
		if got := mustGet(t, book, "a"); got.Balance != 100 || got.CreditLimit != 1000 {
			t.Fatalf("points = %+v", got)
		}
	}
	if reads := db.reads.Load(); reads != 1 {
		t.Fatalf("database reads = %d, want 1", reads)
	}
}

func TestAnUnknownAccountIsNotFoundAndNotCached(t *testing.T) {
	db := newBank(nil)
	book := bankBook(db)
	for range 2 {
		if _, err := book.get(context.Background(), "ghost"); !errors.Is(err, ledger.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if reads := db.reads.Load(); reads != 2 {
		t.Fatalf("database reads = %d, want 2: nothing is cached for an unknown account", reads)
	}
	// The account appears later (it was created): the next request sees it.
	db.mu.Lock()
	db.points["ghost"] = ledger.Points{Balance: 7}
	db.mu.Unlock()
	if got := mustGet(t, book, "ghost"); got.Balance != 7 {
		t.Fatalf("balance = %v", got.Balance)
	}
}

func TestAFailedReadFailsTheRequestAndCachesNothing(t *testing.T) {
	db := newBank(map[string]int64{"a": 100})
	book := bankBook(db)
	db.err = errors.New("database down")
	if _, err := book.get(context.Background(), "a"); err == nil {
		t.Fatal("get succeeded with the database down")
	}
	db.err = nil
	if got := mustGet(t, book, "a"); got.Balance != 100 {
		t.Fatalf("balance = %v", got.Balance)
	}
}

func TestACommittedSettlementIsAddedToTheLoadedBalances(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 10})
	book := bankBook(db)
	mustGet(t, book, "consumer")
	mustGet(t, book, "provider")
	reads := db.reads.Load()

	changes := charge("consumer", "provider", 30)
	end := book.Posting(ledger.TypeAPICall, changes)
	db.commit("consumer", -30)
	db.commit("provider", 30)
	end(ledger.Committed)

	if got := mustGet(t, book, "consumer"); got.Balance != 70 {
		t.Fatalf("consumer = %v, want 70", got.Balance)
	}
	if got := mustGet(t, book, "provider"); got.Balance != 40 {
		t.Fatalf("provider = %v, want 40", got.Balance)
	}
	if db.reads.Load() != reads {
		t.Fatal("the settlement made the cache read the database again")
	}
}

func TestASettlementOfAccountsNobodyHasAskedForIsNotAnEntryWithAValue(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 10})
	book := bankBook(db)
	end := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 30))
	db.commit("consumer", -30)
	db.commit("provider", 30)
	end(ledger.Committed)
	// Nothing was loaded, so nothing is served from the settlement; the first
	// request reads the database, which has it.
	if got := mustGet(t, book, "consumer"); got.Balance != 70 {
		t.Fatalf("consumer = %v, want 70", got.Balance)
	}
}

func TestARolledBackSettlementChangesNothing(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 10})
	book := bankBook(db)
	mustGet(t, book, "consumer")
	reads := db.reads.Load()

	end := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 30))
	end(ledger.RolledBack)

	if got := mustGet(t, book, "consumer"); got.Balance != 100 {
		t.Fatalf("consumer = %v, want 100", got.Balance)
	}
	if db.reads.Load() != reads {
		t.Fatal("a rollback made the cache read the database again")
	}
}

func TestASettlementWhoseCommitIsUnknownDropsTheEntries(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 10})
	book := bankBook(db)
	mustGet(t, book, "consumer")
	end := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 30))
	db.commit("consumer", -30) // the commit did reach the database
	db.commit("provider", 30)
	end(ledger.Unknown)
	if got := mustGet(t, book, "consumer"); got.Balance != 70 {
		t.Fatalf("consumer = %v, want 70", got.Balance)
	}
}

func TestOtherPostingsDropTheEntriesAfterTheyCommit(t *testing.T) {
	for _, kind := range []ledger.TransactionType{ledger.TypeC2CList, ledger.TypeC2CRelease, ledger.TypeC2CReturn, ledger.TypeAdminAdjust, ledger.TypeBadDebtWriteOff} {
		t.Run(string(kind), func(t *testing.T) {
			db := newBank(map[string]int64{"a": 100, "b": 5})
			book := bankBook(db)
			mustGet(t, book, "a")
			mustGet(t, book, "b")

			end := book.Posting(kind, []ledger.Change{{AccountID: "a", Amount: -40}})
			// The posting is not visible yet: until the commit the old value is right.
			db.commit("a", -40)
			if got := mustGet(t, book, "a"); got.Balance != 100 {
				t.Fatalf("a = %v before the commit was reported, want the old 100", got.Balance)
			}
			end(ledger.Committed)

			if got := mustGet(t, book, "a"); got.Balance != 60 {
				t.Fatalf("a = %v after the commit, want 60", got.Balance)
			}
			reads := db.reads.Load()
			mustGet(t, book, "b")
			if db.reads.Load() != reads {
				t.Fatal("an unrelated account was dropped")
			}
		})
	}
}

func TestOtherPostingsThatRollBackKeepTheEntries(t *testing.T) {
	db := newBank(map[string]int64{"a": 100})
	book := bankBook(db)
	mustGet(t, book, "a")
	reads := db.reads.Load()
	end := book.Posting(ledger.TypeC2CList, []ledger.Change{{AccountID: "a", Amount: -40}})
	end(ledger.RolledBack)
	mustGet(t, book, "a")
	if db.reads.Load() != reads {
		t.Fatal("a rolled back posting dropped the entry")
	}
	end = book.Posting(ledger.TypeC2CList, []ledger.Change{{AccountID: "a", Amount: -40}})
	end(ledger.Unknown)
	mustGet(t, book, "a")
	if db.reads.Load() == reads {
		t.Fatal("a posting of unknown outcome kept the entry")
	}
}

func TestPostingsThatChangeNoUserAccountAreIgnored(t *testing.T) {
	book := bankBook(newBank(nil))
	if end := book.Posting(ledger.TypeAPICall, nil); end != nil {
		t.Fatal("a posting without user accounts is of no interest")
	}
}

func TestACreditLimitChangeDropsTheEntry(t *testing.T) {
	db := newBank(map[string]int64{"a": 100})
	book := bankBook(db)
	mustGet(t, book, "a")
	db.setLimit("a", 5)
	book.invalidate("a")
	if got := mustGet(t, book, "a"); got.CreditLimit != 5 {
		t.Fatalf("credit limit = %v, want 5", got.CreditLimit)
	}
	book.invalidate("nobody") // an account without an entry is not an error
}

func TestAnOverflowingSettlementDropsTheEntryInsteadOfWrapping(t *testing.T) {
	db := newBank(map[string]int64{"a": math.MaxInt64 - 5})
	book := bankBook(db)
	mustGet(t, book, "a")
	end := book.Posting(ledger.TypeAPICall, []ledger.Change{{AccountID: "a", Amount: money.FromNano(100)}})
	end(ledger.Committed)
	reads := db.reads.Load()
	mustGet(t, book, "a")
	if db.reads.Load() == reads {
		t.Fatal("the entry was kept although the sum does not fit")
	}
}

// A read that is in flight when something changes the account must not store
// what it found: it may have looked at the old data.
func TestAReadStartedBeforeAnInvalidationDoesNotStoreItsResult(t *testing.T) {
	db := newBank(map[string]int64{"a": 100})
	book := bankBook(db)
	read, release := make(chan struct{}), make(chan struct{})
	db.afterRead = func([]string) {
		close(read)
		<-release
	}
	done := make(chan ledger.Points)
	go func() { done <- mustGet(t, book, "a") }()
	<-read // the load has looked at the data (balance 100) and is held back
	db.afterRead = nil
	db.commit("a", -40)
	book.invalidate("a")
	close(release)
	if got := <-done; got.Balance != 100 {
		t.Fatalf("the request that was in flight got %v, want what it read", got.Balance)
	}
	if got := mustGet(t, book, "a"); got.Balance != 60 {
		t.Fatalf("the next request got %v, want 60: the stale read must not have been stored", got.Balance)
	}
}

// The case the busy count exists for. A settlement commits; a load starts
// after the settlement began and reads the database after the commit, so its
// value already includes the settlement; it finishes before the settlement is
// reported. If it were stored, reporting the settlement would add it a second
// time.
func TestAReadThatSawTheCommitIsNotCountedTwice(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 0})
	book := bankBook(db)

	end := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 30))
	db.commit("consumer", -30)
	db.commit("provider", 30)
	if got := mustGet(t, book, "consumer"); got.Balance != 70 {
		t.Fatalf("the read during the settlement got %v, want 70", got.Balance)
	}
	end(ledger.Committed)

	if got := mustGet(t, book, "consumer"); got.Balance != 70 {
		t.Fatalf("consumer = %v, want 70: the settlement was counted twice", got.Balance)
	}
	if got := mustGet(t, book, "provider"); got.Balance != 30 {
		t.Fatalf("provider = %v, want 30", got.Balance)
	}
}

// The other side: a load that read before the commit and stores after the
// settlement was reported would put the old balance back.
func TestAReadThatMissedTheCommitDoesNotPutTheOldBalanceBack(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 0})
	book := bankBook(db)
	read, release := make(chan struct{}), make(chan struct{})
	db.afterRead = func([]string) {
		close(read)
		<-release
	}
	done := make(chan ledger.Points)
	go func() { done <- mustGet(t, book, "consumer") }()
	<-read // it has read 100

	db.afterRead = nil
	end := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 30))
	db.commit("consumer", -30)
	db.commit("provider", 30)
	end(ledger.Committed)
	close(release)
	<-done

	if got := mustGet(t, book, "consumer"); got.Balance != 70 {
		t.Fatalf("consumer = %v, want 70", got.Balance)
	}
}

// A load that finished before the settlement began is an ordinary entry that
// the settlement is added to.
func TestAReadThatFinishedBeforeTheSettlementIsUpdatedByIt(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 0})
	book := bankBook(db)
	mustGet(t, book, "consumer")
	end := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 30))
	// While the transaction is open the entry still answers with the balance
	// before it: the database has the same value until the commit.
	if got := mustGet(t, book, "consumer"); got.Balance != 100 {
		t.Fatalf("consumer = %v during the transaction, want 100", got.Balance)
	}
	db.commit("consumer", -30)
	db.commit("provider", 30)
	end(ledger.Committed)
	if got := mustGet(t, book, "consumer"); got.Balance != 70 {
		t.Fatalf("consumer = %v, want 70", got.Balance)
	}
}

func TestWhileAnAccountIsBeingChargedAnInvalidEntryReadsTheDatabaseEachTime(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 0})
	book := bankBook(db)
	end := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 30))
	mustGet(t, book, "consumer")
	mustGet(t, book, "consumer")
	if reads := db.reads.Load(); reads != 2 {
		t.Fatalf("reads = %d, want 2: nothing is stored while a settlement is open", reads)
	}
	db.commit("consumer", -30)
	end(ledger.Committed)
	mustGet(t, book, "consumer")
	mustGet(t, book, "consumer")
	if reads := db.reads.Load(); reads != 3 {
		t.Fatalf("reads = %d, want 3: the entry is stored again once the settlement is over", reads)
	}
}

func TestOverlappingSettlementsOfOneAccount(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 0})
	book := bankBook(db)
	mustGet(t, book, "consumer")
	first := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 10))
	second := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 20))
	db.commit("consumer", -10)
	db.commit("provider", 10)
	first(ledger.Committed)
	second(ledger.RolledBack) // the second one failed
	if got := mustGet(t, book, "consumer"); got.Balance != 90 {
		t.Fatalf("consumer = %v, want 90", got.Balance)
	}
	third := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 5))
	db.commit("consumer", -5)
	db.commit("provider", 5)
	third(ledger.Committed)
	if got := mustGet(t, book, "consumer"); got.Balance != 85 {
		t.Fatalf("consumer = %v, want 85", got.Balance)
	}
}

func TestRefreshReloadsTheEntriesInUseAndForgetsTheOthers(t *testing.T) {
	db := newBank(map[string]int64{"busy": 100, "idle": 100})
	book := bankBook(db)
	mustGet(t, book, "busy")
	mustGet(t, book, "idle")

	// First round: both were used since the start, both are reloaded.
	db.commit("busy", -10)
	db.commit("idle", -10)
	if err := book.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, book, "busy"); got.Balance != 90 {
		t.Fatalf("busy = %v, want 90: a change made behind the cache's back is picked up", got.Balance)
	}
	// Second round: only "busy" was asked for since the first.
	if err := book.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if book.size() != 1 {
		t.Fatalf("entries = %d, want only the one in use", book.size())
	}
	reads := db.reads.Load()
	mustGet(t, book, "idle")
	if db.reads.Load() != reads+1 {
		t.Fatal("a forgotten account was not read again")
	}
}

func TestRefreshRepairsAnInvalidEntryBeforeAnyoneAsks(t *testing.T) {
	db := newBank(map[string]int64{"a": 100})
	book := bankBook(db)
	mustGet(t, book, "a")
	book.invalidate("a")
	if err := book.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	reads := db.reads.Load()
	mustGet(t, book, "a")
	if db.reads.Load() != reads {
		t.Fatal("the refresh left the invalidated entry unloaded")
	}
}

func TestAFailedRefreshKeepsWhatItHas(t *testing.T) {
	db := newBank(map[string]int64{"a": 100})
	book := bankBook(db)
	mustGet(t, book, "a")
	db.err = errors.New("database down")
	if err := book.refresh(context.Background()); err == nil {
		t.Fatal("refresh succeeded with the database down")
	}
	if got := mustGet(t, book, "a"); got.Balance != 100 {
		t.Fatalf("a = %v", got.Balance)
	}
}

func TestRefreshLeavesAnEntryAloneThatChangedMeanwhile(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 100, "provider": 0})
	book := bankBook(db)
	mustGet(t, book, "consumer")
	read, release := make(chan struct{}), make(chan struct{})
	db.afterRead = func([]string) {
		close(read)
		<-release
	}
	done := make(chan error)
	go func() { done <- book.refresh(context.Background()) }()
	<-read // the refresh has read 100

	db.afterRead = nil
	end := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 30))
	db.commit("consumer", -30)
	db.commit("provider", 30)
	end(ledger.Committed)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, book, "consumer"); got.Balance != 70 {
		t.Fatalf("consumer = %v, want 70: the refresh put its older read back", got.Balance)
	}
}

func TestRefreshForgetsAnAccountThatNoLongerExists(t *testing.T) {
	db := newBank(map[string]int64{"a": 100})
	book := bankBook(db)
	mustGet(t, book, "a")
	db.mu.Lock()
	delete(db.points, "a")
	db.mu.Unlock()
	if err := book.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if book.size() != 0 {
		t.Fatalf("entries = %d, want 0", book.size())
	}
}

// Whatever the interleaving of reads, settlements, other postings and
// invalidations, once the postings are over every account is answered with
// what the database holds. The reads are held back for a random moment after
// they looked at the data, which is what lets them straddle commits.
func TestTheBalancesEndUpWhereTheDatabaseIs(t *testing.T) {
	accounts := []string{"a", "b", "c", "d"}
	for round := range 20 {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			start := map[string]int64{}
			for _, id := range accounts {
				start[id] = 1000
			}
			db := newBank(start)
			db.afterRead = func([]string) {
				for range rand.IntN(4) {
					runtime.Gosched()
				}
			}
			book := bankBook(db)
			rowLocks := map[string]*sync.Mutex{}
			for _, id := range accounts {
				rowLocks[id] = &sync.Mutex{}
			}

			var wg sync.WaitGroup
			stop := make(chan struct{})
			for range 4 { // requests checking balances
				wg.Go(func() {
					for {
						select {
						case <-stop:
							return
						default:
						}
						if _, err := book.get(context.Background(), accounts[rand.IntN(len(accounts))]); err != nil {
							t.Error(err)
							return
						}
					}
				})
			}
			var posters sync.WaitGroup
			for worker := range 6 { // postings
				posters.Go(func() {
					for range 300 {
						consumer, provider := accounts[rand.IntN(len(accounts))], accounts[rand.IntN(len(accounts))]
						if consumer == provider {
							continue
						}
						first, second := consumer, provider
						if first > second {
							first, second = second, first
						}
						// A posting holds the rows of its accounts from its
						// writes to its commit, like the database does.
						rowLocks[first].Lock()
						rowLocks[second].Lock()
						amount := int64(1 + rand.IntN(50))
						switch worker % 3 {
						case 0: // the gateway's settlement
							end := book.Posting(ledger.TypeAPICall, charge(consumer, provider, amount))
							runtime.Gosched()
							db.commit(consumer, -amount)
							db.commit(provider, amount)
							rowLocks[second].Unlock()
							rowLocks[first].Unlock()
							runtime.Gosched()
							end(ledger.Committed)
						case 1: // something else, which drops the entries
							end := book.Posting(ledger.TypeC2CRelease, charge(consumer, provider, amount))
							db.commit(consumer, -amount)
							db.commit(provider, amount)
							rowLocks[second].Unlock()
							rowLocks[first].Unlock()
							runtime.Gosched()
							end(ledger.Committed)
						default: // a credit limit changes, or a settlement fails
							end := book.Posting(ledger.TypeAPICall, charge(consumer, provider, amount))
							runtime.Gosched()
							rowLocks[second].Unlock()
							rowLocks[first].Unlock()
							end(ledger.RolledBack)
							book.invalidate(consumer)
						}
					}
				})
			}
			refreshing := make(chan struct{})
			go func() {
				defer close(refreshing)
				for {
					select {
					case <-stop:
						return
					default:
					}
					if err := book.refresh(context.Background()); err != nil {
						t.Error(err)
						return
					}
					runtime.Gosched()
				}
			}()
			posters.Wait()
			close(stop)
			wg.Wait()
			<-refreshing

			for _, id := range accounts {
				if got, want := mustGet(t, book, id).Balance.Nano(), db.balance(id); got != want {
					t.Errorf("%s: the cache says %d, the database %d", id, got, want)
				}
			}
		})
	}
}

func TestOverlappingSettlementsOfOneAccountEndUpWhereTheDatabaseIs(t *testing.T) {
	db := newBank(map[string]int64{"consumer": 1_000_000, "provider": 0})
	db.afterRead = func([]string) { runtime.Gosched() }
	book := bankBook(db)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := book.get(context.Background(), "consumer"); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	var posters sync.WaitGroup
	for range 8 {
		posters.Go(func() {
			for range 500 {
				// No row lock: transactions of one account may overlap here,
				// which the busy count has to cope with as well.
				end := book.Posting(ledger.TypeAPICall, charge("consumer", "provider", 3))
				runtime.Gosched()
				db.commit("consumer", -3)
				db.commit("provider", 3)
				end(ledger.Committed)
			}
		})
	}
	posters.Wait()
	close(stop)
	wg.Wait()
	if got, want := mustGet(t, book, "consumer").Balance.Nano(), db.balance("consumer"); got != want {
		t.Fatalf("consumer: the cache says %d, the database %d", got, want)
	}
}

// ---- the cache around the book ----

func TestTheGatewayStoreAnswersBalancesFromMemory(t *testing.T) {
	loader := &fakeLoader{points: map[string]ledger.Points{"a": {Balance: 5, CreditLimit: 7}}}
	cache := newTestCache(loader)
	store := cache.Gateway(&countingStore{})
	for range 3 {
		points, err := store.Points(context.Background(), "a")
		if err != nil || points.Balance != 5 || points.CreditLimit != 7 {
			t.Fatalf("Points = %+v, %v", points, err)
		}
	}
	if loader.loads != 1 {
		t.Fatalf("database reads = %d, want 1", loader.loads)
	}
}

func TestChangingACreditLimitThroughTheIdentityStoreDropsTheCachedEntry(t *testing.T) {
	loader := &fakeLoader{points: map[string]ledger.Points{"a": {Balance: 5, CreditLimit: 7}}}
	cache := newTestCache(loader)
	store := cache.Gateway(&countingStore{})
	if _, err := store.Points(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	loader.with(func(l *fakeLoader) { l.points["a"] = ledger.Points{Balance: 5, CreditLimit: 0} })
	if _, err := cache.Identity(stubIdentity{}).UpdateAccount(context.Background(), "admin", "a", identityLimitUpdate()); err != nil {
		t.Fatal(err)
	}
	points, err := store.Points(context.Background(), "a")
	if err != nil || points.CreditLimit != 0 {
		t.Fatalf("Points = %+v, %v: the new credit limit did not reach the next request", points, err)
	}
}

func TestAFailedIdentityUpdateKeepsTheCachedEntry(t *testing.T) {
	loader := &fakeLoader{points: map[string]ledger.Points{"a": {Balance: 5, CreditLimit: 7}}}
	cache := newTestCache(loader)
	store := cache.Gateway(&countingStore{})
	if _, err := store.Points(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	_, _ = cache.Identity(stubIdentity{fail: errors.New("rolled back")}).UpdateAccount(context.Background(), "admin", "a", identityLimitUpdate())
	if _, err := store.Points(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if loader.loads != 1 {
		t.Fatalf("database reads = %d, want 1", loader.loads)
	}
}

func TestRunReloadsTheBalancesOnItsInterval(t *testing.T) {
	loader := &fakeLoader{points: map[string]ledger.Points{"a": {Balance: 5, CreditLimit: 7}}}
	cache := New(Config{Loader: loader, Interval: 20 * time.Millisecond})
	store := cache.Gateway(&countingStore{})
	if _, err := store.Points(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	// Someone changes the balance behind the process's back.
	loader.with(func(l *fakeLoader) { l.points["a"] = ledger.Points{Balance: 50, CreditLimit: 7} })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cache.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		points, err := store.Points(context.Background(), "a")
		if err != nil {
			t.Fatal(err)
		}
		if points.Balance == 50 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("balance = %v after 5s of rebuilds, want 50", points.Balance)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTheCacheObservesPostingsThroughItsBalancesObserver(t *testing.T) {
	loader := &fakeLoader{points: map[string]ledger.Points{"a": {Balance: 100}, "b": {Balance: 0}}}
	cache := newTestCache(loader)
	store := cache.Gateway(&countingStore{})
	for _, id := range []string{"a", "b"} {
		if _, err := store.Points(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	end := cache.Balances().Posting(ledger.TypeAPICall, charge("a", "b", 30))
	end(ledger.Committed)
	if points, _ := store.Points(context.Background(), "a"); points.Balance != 70 {
		t.Fatalf("a = %v, want 70", points.Balance)
	}
	if points, _ := store.Points(context.Background(), "b"); points.Balance != 30 {
		t.Fatalf("b = %v, want 30", points.Balance)
	}
	if loader.loads != 2 {
		t.Fatalf("database reads = %d, want only the two first ones", loader.loads)
	}
}

func identityLimitUpdate() identity.AccountUpdate {
	limit := money.FromNano(0)
	return identity.AccountUpdate{CreditLimit: &limit}
}
