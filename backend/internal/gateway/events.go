package gateway

import (
	"sync"
	"sync/atomic"
	"time"
)

// EventKind names an in-process gateway event. Feature G's live streams and
// Prometheus metrics subscribe to these; the gateway itself never blocks on
// them.
type EventKind string

const (
	EventCallStarted     EventKind = "call_started"
	EventAttemptFinished EventKind = "attempt_finished"
	EventCallFinished    EventKind = "call_finished"
	EventChannelCooldown EventKind = "channel_cooldown_started"
	EventChannelRecover  EventKind = "channel_cooldown_ended"
	EventChannelLimit    EventKind = "channel_limit_reached"
)

// Event carries identifiers and outcomes only: never request or response
// bodies and never any key.
type Event struct {
	Kind       EventKind
	At         time.Time
	CallID     string
	AccountID  string
	KeyID      string
	ChannelID  string
	Model      string
	Format     string
	Outcome    string
	StatusCode int
	EndReason  string
	DurationMS int
	TTFTMS     int
	Detail     string
}

// Publisher receives gateway events. Implementations must not block.
type Publisher interface {
	Publish(Event)
}

type discard struct{}

func (discard) Publish(Event) {}

// Bus is the in-process event bus: every subscriber has a bounded buffer and
// events that do not fit are dropped for that subscriber.
type Bus struct {
	mutex       sync.RWMutex
	next        int
	subscribers map[int]chan Event
	dropped     atomic.Uint64
}

func NewBus() *Bus { return &Bus{subscribers: map[int]chan Event{}} }

func (b *Bus) Publish(event Event) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	for _, subscriber := range b.subscribers {
		select {
		case subscriber <- event:
		default:
			b.dropped.Add(1)
		}
	}
}

// Subscribe returns a channel of events and a function that ends the
// subscription.
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer < 1 {
		buffer = 256
	}
	channel := make(chan Event, buffer)
	b.mutex.Lock()
	id := b.next
	b.next++
	b.subscribers[id] = channel
	b.mutex.Unlock()
	return channel, func() {
		b.mutex.Lock()
		if _, ok := b.subscribers[id]; ok {
			delete(b.subscribers, id)
			close(channel)
		}
		b.mutex.Unlock()
	}
}

// Dropped counts events discarded because a subscriber was full.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }
