package event

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Publisher is the interface for publishing events
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

// Subscriber is the interface for subscribing to events
type Subscriber interface {
	Subscribe(eventType string, handler Handler) UnsubscribeFunc
	SubscribeAll(handler Handler) UnsubscribeFunc
}

// Bus is the full event bus interface
type Bus interface {
	Publisher
	Subscriber
}

// bus is the in-memory implementation of the event bus
type bus struct {
	mu          sync.Mutex
	subs        map[string]map[*handlerEntry]struct{}
	allSubs     map[*handlerEntry]struct{}
	chanBuffer  int // Buffer size for handler channels
}

type handlerEntry struct {
	handler Handler
	ch      chan Event
	cancel  chan struct{}
}

// NewBus creates a new event bus
func NewBus() Bus {
	return &bus{
		subs:       map[string]map[*handlerEntry]struct{}{},
		allSubs:    map[*handlerEntry]struct{}{},
		chanBuffer: 8, // Reasonable buffer to prevent blocking publishers
	}
}

// Publish publishes an event to all subscribers
func (b *bus) Publish(ctx context.Context, e Event) error {
	// Auto-populate timestamp if not set
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	// Auto-generate ID if not set
	if e.ID == "" {
		e.ID = generateID()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Send to type-specific subscribers
	for entry := range b.subs[e.Type] {
		b.sendToHandler(entry, e)
	}

	// Send to global subscribers
	for entry := range b.allSubs {
		b.sendToHandler(entry, e)
	}

	return nil
}

// sendToHandler sends the event without blocking
// Uses non-blocking send, dropping old events if the buffer is full
func (b *bus) sendToHandler(entry *handlerEntry, e Event) {
	select {
	case entry.ch <- e:
		// Sent successfully
	default:
		// Channel is full, make room by dropping the oldest event
		select {
		case <-entry.ch:
			// Removed oldest event, now try sending again
			select {
			case entry.ch <- e:
			default:
				// Still full, drop this event too
			}
		default:
			// Channel was drained between checks, try sending once more
			select {
			case entry.ch <- e:
			default:
			}
		}
	}
}

// Subscribe subscribes to a specific event type
func (b *bus) Subscribe(eventType string, handler Handler) UnsubscribeFunc {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := &handlerEntry{
		handler: handler,
		ch:      make(chan Event, b.chanBuffer),
		cancel:  make(chan struct{}),
	}

	// Create handler map if not exists
	if b.subs[eventType] == nil {
		b.subs[eventType] = map[*handlerEntry]struct{}{}
	}
	b.subs[eventType][entry] = struct{}{}

	// Start handler goroutine
	go b.runHandler(entry)

	return b.unsubscribeFunc(eventType, entry)
}

// SubscribeAll subscribes to all event types
func (b *bus) SubscribeAll(handler Handler) UnsubscribeFunc {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := &handlerEntry{
		handler: handler,
		ch:      make(chan Event, b.chanBuffer),
		cancel:  make(chan struct{}),
	}

	b.allSubs[entry] = struct{}{}

	// Start handler goroutine
	go b.runHandler(entry)

	return b.unsubscribeAllFunc(entry)
}

// runHandler processes events for a single subscriber
func (b *bus) runHandler(entry *handlerEntry) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-entry.cancel
		cancel()
	}()

	for {
		select {
		case <-entry.cancel:
			return
		case e := <-entry.ch:
			// Ignore handler errors; it's the handler's responsibility to handle them
			_ = entry.handler(ctx, e)
		}
	}
}

func (b *bus) unsubscribeFunc(eventType string, entry *handlerEntry) UnsubscribeFunc {
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		if handlers, ok := b.subs[eventType]; ok {
			delete(handlers, entry)
			if len(handlers) == 0 {
				delete(b.subs, eventType)
			}
			close(entry.cancel)
		}
	}
}

func (b *bus) unsubscribeAllFunc(entry *handlerEntry) UnsubscribeFunc {
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		delete(b.allSubs, entry)
		close(entry.cancel)
	}
}

// generateID creates a short random ID for events
func generateID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "evt_" + base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)
}
