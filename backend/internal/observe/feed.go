package observe

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

// Live call kinds pushed to stream subscribers.
const (
	FeedStarted  = "call.started"
	FeedFinished = "call.finished"
)

// FeedMessage is a call at the moment it started or finished.
type FeedMessage struct {
	Kind string
	Call CallRecord
}

// Observer receives every gateway event, with the stored call for call
// events. It must not block.
type Observer interface {
	Observe(event gateway.Event, call *CallRecord)
}

// Events is the in-process gateway event bus.
type Events interface {
	Subscribe(buffer int) (<-chan gateway.Event, func())
}

// Feed turns gateway events into live call messages and drives the observers
// (the Prometheus metrics). Subscribers have a bounded buffer: a slow one
// loses messages instead of slowing the gateway (Epic #170, decision 11).
type Feed struct {
	service   *Service
	events    Events
	logger    *slog.Logger
	observers []Observer

	mutex       sync.Mutex
	next        int
	subscribers map[int]chan FeedMessage
	dropped     uint64
}

func NewFeed(service *Service, events Events, logger *slog.Logger) *Feed {
	if logger == nil {
		logger = slog.Default()
	}
	return &Feed{service: service, events: events, logger: logger, subscribers: map[int]chan FeedMessage{}}
}

// AddObserver registers an observer; call before Run.
func (f *Feed) AddObserver(observer Observer) { f.observers = append(f.observers, observer) }

// Subscribe returns a channel of live messages and a function that ends the
// subscription.
func (f *Feed) Subscribe(buffer int) (<-chan FeedMessage, func()) {
	if buffer < 1 {
		buffer = 64
	}
	channel := make(chan FeedMessage, buffer)
	f.mutex.Lock()
	id := f.next
	f.next++
	f.subscribers[id] = channel
	f.mutex.Unlock()
	return channel, func() {
		f.mutex.Lock()
		if _, ok := f.subscribers[id]; ok {
			delete(f.subscribers, id)
			close(channel)
		}
		f.mutex.Unlock()
	}
}

func (f *Feed) publish(message FeedMessage) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	for _, subscriber := range f.subscribers {
		select {
		case subscriber <- message:
		default:
			f.dropped++
		}
	}
}

func (f *Feed) hasSubscribers() bool {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return len(f.subscribers) > 0
}

// Dropped counts live messages lost to full subscriber buffers.
func (f *Feed) Dropped() uint64 {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.dropped
}

// Run processes events until ctx ends.
func (f *Feed) Run(ctx context.Context) {
	events, cancel := f.events.Subscribe(4096)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			f.handle(ctx, event)
		}
	}
}

func (f *Feed) handle(ctx context.Context, event gateway.Event) {
	var kind string
	switch event.Kind {
	case gateway.EventCallStarted:
		kind = FeedStarted
	case gateway.EventCallFinished:
		kind = FeedFinished
	}
	var record *CallRecord
	// A started call is only needed by live subscribers; the metrics learn of it from the event alone.
	if kind != "" && event.CallID != "" && (kind == FeedFinished || f.hasSubscribers()) {
		loadContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		loaded, err := f.service.Record(loadContext, event.CallID)
		cancel()
		if err != nil {
			f.logger.Warn("live feed: reading call failed", "request_id", event.CallID, "error", err)
		} else {
			record = &loaded
			f.publish(FeedMessage{Kind: kind, Call: loaded})
		}
	}
	for _, observer := range f.observers {
		observer.Observe(event, record)
	}
}
