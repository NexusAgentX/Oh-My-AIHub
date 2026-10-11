package gateway

import "sync"

// Inflight counts the gateway calls each account has in flight (ADR-0035
// rule 2). The gateway never reserves balance, so concurrent requests of one
// account can all pass the balance check before any of them is billed; capping
// how many may run at once bounds that overdraft to the cap times the cost of
// one request.
//
// The counts are in-process state of the single instance (ADR-0039): nothing
// is written to the database and a restart starts from zero.
type Inflight struct {
	mutex  sync.Mutex
	counts map[string]int
}

func NewInflight() *Inflight { return &Inflight{counts: map[string]int{}} }

// Acquire takes one slot for the account unless it already has limit calls in
// flight, in which case ok is false and nothing is held. A limit of zero or
// less means unlimited, but the call is still counted, so lowering the limit
// later also sees the calls that started while there was none.
//
// The caller must call release exactly when the call has ended and been
// booked. Calling it more than once is harmless.
func (i *Inflight) Acquire(accountID string, limit int) (release func(), ok bool) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if limit > 0 && i.counts[accountID] >= limit {
		return nil, false
	}
	i.counts[accountID]++
	var once sync.Once
	return func() { once.Do(func() { i.release(accountID) }) }, true
}

func (i *Inflight) release(accountID string) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.counts[accountID] <= 1 {
		delete(i.counts, accountID)
		return
	}
	i.counts[accountID]--
}

// Count is how many calls the account has in flight now.
func (i *Inflight) Count(accountID string) int {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	return i.counts[accountID]
}
