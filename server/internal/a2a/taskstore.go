package a2a

import (
	"sync"
	"time"
)

// TaskStatus represents the state of an A2A task
type TaskStatus string

const (
	TaskStatusWorking   TaskStatus = "working"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// Task represents an A2A task with its result or error
type Task struct {
	ID        string     `json:"id"`
	Status    TaskStatus `json:"status"`
	Result    string     `json:"result,omitempty"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// TaskStore interface for task persistence
type TaskStore interface {
	Get(taskID string) (*Task, bool)
	Put(taskID string, task *Task)
}

type taskEntry struct {
	task     *Task
	expireAt time.Time
}

// MemoryTaskStore is an in-memory task store with TTL-based cleanup
type MemoryTaskStore struct {
	mu       sync.RWMutex
	tasks    map[string]taskEntry
	ttl      time.Duration
	stopChan chan struct{}
}

// NewMemoryTaskStore creates a new in-memory task store with TTL
// Background cleanup runs every minute to remove expired tasks
func NewMemoryTaskStore(ttlSeconds int) *MemoryTaskStore {
	store := &MemoryTaskStore{
		tasks:    make(map[string]taskEntry),
		ttl:      time.Duration(ttlSeconds) * time.Second,
		stopChan: make(chan struct{}),
	}

	// Start background cleanup goroutine
	go store.cleanupLoop()

	return store
}

// Stop terminates the background cleanup goroutine
func (s *MemoryTaskStore) Stop() {
	close(s.stopChan)
}

func (s *MemoryTaskStore) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.cleanupExpired()
		case <-s.stopChan:
			return
		}
	}
}

func (s *MemoryTaskStore) cleanupExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, entry := range s.tasks {
		if now.After(entry.expireAt) {
			delete(s.tasks, id)
		}
	}
}

// Get retrieves a task by ID. Returns (nil, false) if not found or expired.
func (s *MemoryTaskStore) Get(taskID string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.tasks[taskID]
	if !ok {
		return nil, false
	}

	if time.Now().After(entry.expireAt) {
		return nil, false
	}

	return entry.task, true
}

// Put stores a task with TTL
func (s *MemoryTaskStore) Put(taskID string, task *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tasks[taskID] = taskEntry{
		task:     task,
		expireAt: time.Now().Add(s.ttl),
	}
}
