package event

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBus_PublishSubscribe(t *testing.T) {
	bus := NewBus()
	received := make(chan Event, 1)

	unsub := bus.Subscribe(TypeTodoUpdated, func(ctx context.Context, e Event) error {
		received <- e
		return nil
	})
	defer unsub()

	expected := Event{
		Type:      TypeTodoUpdated,
		SessionID: "test-session",
		Data: TodoUpdatedData{
			SessionID: "test-session",
			Todos: []Todo{
				{ID: "task-1", Status: "running"},
			},
		},
	}

	err := bus.Publish(context.Background(), expected)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case got := <-received:
		if got.Type != expected.Type {
			t.Errorf("Expected type %s, got %s", expected.Type, got.Type)
		}
		if got.SessionID != expected.SessionID {
			t.Errorf("Expected SessionID %s, got %s", expected.SessionID, got.SessionID)
		}
		if got.ID == "" {
			t.Error("Expected auto-generated event ID")
		}
		if got.Timestamp.IsZero() {
			t.Error("Expected auto-generated timestamp")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for event")
	}
}

func TestBus_Unsubscribe(t *testing.T) {
	bus := NewBus()
	var count int32

	unsub := bus.Subscribe(TypeTodoUpdated, func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})

	// Publish first event
	_ = bus.Publish(context.Background(), Event{Type: TypeTodoUpdated})
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("Expected 1 event, got %d", count)
	}

	// Unsubscribe
	unsub()

	// Publish second event
	_ = bus.Publish(context.Background(), Event{Type: TypeTodoUpdated})
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("Expected still 1 event after unsubscribe, got %d", count)
	}
}

func TestBus_SubscribeAll(t *testing.T) {
	bus := NewBus()
	received := make(chan Event, 3)

	unsub := bus.SubscribeAll(func(ctx context.Context, e Event) error {
		received <- e
		return nil
	})
	defer unsub()

	// Publish different event types
	_ = bus.Publish(context.Background(), Event{Type: TypeTodoUpdated})
	_ = bus.Publish(context.Background(), Event{Type: TypeMessagePartDelta})
	_ = bus.Publish(context.Background(), Event{Type: TypeSessionError})

	// Should receive all 3 events
	for i := 0; i < 3; i++ {
		select {
		case <-received:
			// OK
		case <-time.After(1 * time.Second):
			t.Fatalf("Timeout waiting for event %d", i+1)
		}
	}
}

func TestBus_TypeSpecificSubscription(t *testing.T) {
	bus := NewBus()
	todoReceived := make(chan Event, 1)
	errorReceived := make(chan Event, 1)

	unsubTodo := bus.Subscribe(TypeTodoUpdated, func(ctx context.Context, e Event) error {
		todoReceived <- e
		return nil
	})
	defer unsubTodo()

	unsubError := bus.Subscribe(TypeSessionError, func(ctx context.Context, e Event) error {
		errorReceived <- e
		return nil
	})
	defer unsubError()

	// Publish both types
	_ = bus.Publish(context.Background(), Event{Type: TypeTodoUpdated})
	_ = bus.Publish(context.Background(), Event{Type: TypeSessionError})

	select {
	case e := <-todoReceived:
		if e.Type != TypeTodoUpdated {
			t.Error("Wrong event type in todo subscriber")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for todo event")
	}

	select {
	case e := <-errorReceived:
		if e.Type != TypeSessionError {
			t.Error("Wrong event type in error subscriber")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for error event")
	}
}

func TestBus_ConcurrentPublish(t *testing.T) {
	bus := NewBus()
	var count int32

	unsub := bus.Subscribe(TypeTodoUpdated, func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})
	defer unsub()

	// Publish concurrently from multiple goroutines
	// Note: Using smaller numbers to minimize event dropping
	const goroutines = 5
	const publishes = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < publishes; j++ {
				_ = bus.Publish(context.Background(), Event{Type: TypeTodoUpdated})
				time.Sleep(1 * time.Millisecond) // Small delay to reduce contention
			}
		}()
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	// We expect most events to be received, but some may be dropped under heavy load
	// This test primarily verifies that the bus doesn't deadlock or panic
	received := int(atomic.LoadInt32(&count))
	if received == 0 {
		t.Error("Expected at least some events to be received")
	}
	t.Logf("Received %d/%d events", received, goroutines*publishes)
}

func TestBus_SlowSubscriber(t *testing.T) {
	bus := NewBus()
	var count int32

	// Slow handler that blocks
	unsub := bus.Subscribe(TypeTodoUpdated, func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count, 1)
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	defer unsub()

	// Publish more events than the channel buffer
	const publishes = 100
	start := time.Now()

	for i := 0; i < publishes; i++ {
		_ = bus.Publish(context.Background(), Event{Type: TypeTodoUpdated})
	}

	publishTime := time.Since(start)
	if publishTime > 100*time.Millisecond {
		t.Errorf("Publish should not block for slow subscribers, took %v", publishTime)
	}

	// Wait for handler to catch up
	time.Sleep(2 * time.Second)

	// Note: Some events may be dropped due to full buffer
	// We just verify that the publisher doesn't block and some events get through
	if atomic.LoadInt32(&count) == 0 {
		t.Error("Expected at least some events to be processed")
	}
}

func TestBus_MultipleSubscribersSameType(t *testing.T) {
	bus := NewBus()
	var count1, count2 int32

	unsub1 := bus.Subscribe(TypeTodoUpdated, func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count1, 1)
		return nil
	})
	defer unsub1()

	unsub2 := bus.Subscribe(TypeTodoUpdated, func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count2, 1)
		return nil
	})
	defer unsub2()

	_ = bus.Publish(context.Background(), Event{Type: TypeTodoUpdated})
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&count1) != 1 {
		t.Error("Subscriber 1 didn't receive event")
	}
	if atomic.LoadInt32(&count2) != 1 {
		t.Error("Subscriber 2 didn't receive event")
	}
}
