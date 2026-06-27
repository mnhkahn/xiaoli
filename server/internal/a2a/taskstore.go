package a2a

import (
	"context"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

// authenticator extracts the authenticated key_id from request context.
// Returns empty string for unauthenticated requests; the InMemory store
// rejects List with ErrUnauthenticated in that case.
func authenticator(ctx context.Context) (string, error) {
	if k, ok := ctx.Value(keyIDContextKey).(string); ok {
		return k, nil
	}
	return "", nil
}

// keyPartitionedStore wraps taskstore.InMemory to enforce per-key_id task
// isolation and TTL-based expiry. Tasks created under one key_id cannot be
// queried via Get by a different key_id — Get returns ErrTaskNotFound for
// cross-key access. The inner store's Authenticator handles List filtering;
// this wrapper adds the same isolation to Get, which the inner store does not
// check.
type keyPartitionedStore struct {
	inner *taskstore.InMemory
	ttl   time.Duration

	mu    sync.RWMutex
	keys  map[a2a.TaskID]string
	times map[a2a.TaskID]time.Time
	stop  chan struct{}
}

var _ taskstore.Store = (*keyPartitionedStore)(nil)

func newKeyPartitionedStore(ttl time.Duration) *keyPartitionedStore {
	inner := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: authenticator,
	})
	s := &keyPartitionedStore{
		inner: inner,
		ttl:   ttl,
		keys:  make(map[a2a.TaskID]string),
		times: make(map[a2a.TaskID]time.Time),
		stop:  make(chan struct{}),
	}
	if ttl > 0 {
		go s.cleanupLoop()
	}
	return s
}

func (s *keyPartitionedStore) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

func (s *keyPartitionedStore) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.cleanupExpired()
		case <-s.stop:
			return
		}
	}
}

func (s *keyPartitionedStore) cleanupExpired() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for tid, created := range s.times {
		if s.ttl > 0 && now.Sub(created) > s.ttl {
			delete(s.keys, tid)
			delete(s.times, tid)
		}
	}
}

func (s *keyPartitionedStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	version, err := s.inner.Create(ctx, task)
	if err != nil {
		return version, err
	}
	keyID, _ := authenticator(ctx)
	s.mu.Lock()
	s.keys[task.ID] = keyID
	s.times[task.ID] = time.Now()
	s.mu.Unlock()
	return version, nil
}

func (s *keyPartitionedStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	return s.inner.Update(ctx, req)
}

func (s *keyPartitionedStore) Get(ctx context.Context, taskID a2a.TaskID) (*taskstore.StoredTask, error) {
	s.mu.RLock()
	ownerKey, exists := s.keys[taskID]
	created := s.times[taskID]
	s.mu.RUnlock()

	if !exists {
		return nil, a2a.ErrTaskNotFound
	}
	if s.ttl > 0 && time.Since(created) > s.ttl {
		return nil, a2a.ErrTaskNotFound
	}
	callerKey, _ := authenticator(ctx)
	if ownerKey != callerKey || callerKey == "" {
		return nil, a2a.ErrTaskNotFound
	}
	return s.inner.Get(ctx, taskID)
}

func (s *keyPartitionedStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return nil, a2a.ErrUnsupportedOperation
}

func (s *keyPartitionedStore) Cancel(ctx context.Context, taskID a2a.TaskID) error {
	return a2a.ErrUnsupportedOperation
}
