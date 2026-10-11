package snapshot

import (
	"container/list"
	"sync"
)

// responseLRU remembers which channel answered a Responses turn, so the next
// turn that continues from it (previous_response_id) goes back to the same
// channel without a query. It holds the most recently used entries up to its
// capacity.
type responseLRU struct {
	mu       sync.Mutex
	capacity int
	order    *list.List // front is the most recent
	entries  map[responseKey]*list.Element
}

type responseKey struct {
	account  string
	response string
}

type responseEntry struct {
	key     responseKey
	channel string
}

func newResponseLRU(capacity int) *responseLRU {
	return &responseLRU{capacity: capacity, order: list.New(), entries: make(map[responseKey]*list.Element, capacity)}
}

func (l *responseLRU) get(account, response string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	element, found := l.entries[responseKey{account: account, response: response}]
	if !found {
		return "", false
	}
	l.order.MoveToFront(element)
	return element.Value.(*responseEntry).channel, true
}

func (l *responseLRU) put(account, response, channel string) {
	key := responseKey{account: account, response: response}
	l.mu.Lock()
	defer l.mu.Unlock()
	if element, found := l.entries[key]; found {
		element.Value.(*responseEntry).channel = channel
		l.order.MoveToFront(element)
		return
	}
	l.entries[key] = l.order.PushFront(&responseEntry{key: key, channel: channel})
	if l.order.Len() > l.capacity {
		oldest := l.order.Back()
		l.order.Remove(oldest)
		delete(l.entries, oldest.Value.(*responseEntry).key)
	}
}
