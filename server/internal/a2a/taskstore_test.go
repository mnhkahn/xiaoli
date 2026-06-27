package a2a

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/stretchr/testify/assert"
)

func ctxWithKey(keyID string) context.Context {
	return context.WithValue(context.Background(), keyIDContextKey, keyID)
}

func newTestTask(id string) *a2a.Task {
	return &a2a.Task{
		ID:        a2a.TaskID(id),
		ContextID: "ctx_" + id,
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}
}

func TestKeyPartitionedStore_CreateAndGet_SameKey(t *testing.T) {
	store := newKeyPartitionedStore(time.Minute)
	defer store.Stop()

	ctx := ctxWithKey("partner_a")
	task := newTestTask("task_1")
	_, err := store.Create(ctx, task)
	assert.NoError(t, err)

	stored, err := store.Get(ctx, "task_1")
	assert.NoError(t, err)
	assert.Equal(t, "task_1", string(stored.Task.ID))
}

func TestKeyPartitionedStore_Get_DifferentKey_ReturnsNotFound(t *testing.T) {
	store := newKeyPartitionedStore(time.Minute)
	defer store.Stop()

	ctxA := ctxWithKey("partner_a")
	task := newTestTask("task_2")
	_, err := store.Create(ctxA, task)
	assert.NoError(t, err)

	// partner_b tries to query partner_a's task — must fail
	ctxB := ctxWithKey("partner_b")
	_, err = store.Get(ctxB, "task_2")
	assert.ErrorIs(t, err, a2a.ErrTaskNotFound)
}

func TestKeyPartitionedStore_Get_UnauthenticatedReturnsNotFound(t *testing.T) {
	store := newKeyPartitionedStore(time.Minute)
	defer store.Stop()

	ctxA := ctxWithKey("partner_a")
	task := newTestTask("task_3")
	_, _ = store.Create(ctxA, task)

	// No key_id in context — must fail
	_, err := store.Get(context.Background(), "task_3")
	assert.ErrorIs(t, err, a2a.ErrTaskNotFound)
}

func TestKeyPartitionedStore_TTLExpiry(t *testing.T) {
	store := newKeyPartitionedStore(100 * time.Millisecond)
	defer store.Stop()

	ctx := ctxWithKey("partner_a")
	task := newTestTask("task_ttl")
	_, _ = store.Create(ctx, task)

	// Immediately accessible
	_, err := store.Get(ctx, "task_ttl")
	assert.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// After TTL, must be gone
	_, err = store.Get(ctx, "task_ttl")
	assert.ErrorIs(t, err, a2a.ErrTaskNotFound)
}

func TestKeyPartitionedStore_ConcurrentAccess(t *testing.T) {
	store := newKeyPartitionedStore(time.Minute)
	defer store.Stop()

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			ctx := ctxWithKey(fmt.Sprintf("key_%d", idx))
			task := newTestTask(fmt.Sprintf("task_%d", idx))
			_, _ = store.Create(ctx, task)
			_, _ = store.Get(ctx, a2a.TaskID(task.ID))
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
