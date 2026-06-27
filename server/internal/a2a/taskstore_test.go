package a2a

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMemoryTaskStore_PutAndGet(t *testing.T) {
	store := NewMemoryTaskStore(60) // 60s TTL
	defer store.Stop()

	task := &Task{
		ID:     "task_123",
		Status: TaskStatusCompleted,
		Result: "hello world",
	}

	store.Put("task_123", task)

	retrieved, ok := store.Get("task_123")
	assert.True(t, ok)
	assert.Equal(t, "task_123", retrieved.ID)
	assert.Equal(t, TaskStatusCompleted, retrieved.Status)
	assert.Equal(t, "hello world", retrieved.Result)
}

func TestMemoryTaskStore_GetNonExistent(t *testing.T) {
	store := NewMemoryTaskStore(60)
	defer store.Stop()

	_, ok := store.Get("nonexistent")
	assert.False(t, ok)
}

func TestMemoryTaskStore_TTLExpiry(t *testing.T) {
	store := NewMemoryTaskStore(1) // 1s TTL
	defer store.Stop()

	task := &Task{ID: "task_expire", Status: TaskStatusCompleted}
	store.Put("task_expire", task)

	// Immediately should exist
	_, ok := store.Get("task_expire")
	assert.True(t, ok)

	// Wait for TTL + cleanup interval
	time.Sleep(2 * time.Second)

	_, ok = store.Get("task_expire")
	assert.False(t, ok, "task should have expired after TTL")
}

func TestMemoryTaskStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryTaskStore(60)
	defer store.Stop()

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			task := &Task{ID: fmt.Sprintf("task_%d", idx), Status: TaskStatusWorking}
			store.Put(task.ID, task)
			_, _ = store.Get(task.ID)
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
