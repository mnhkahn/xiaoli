package workflow

import (
	"fmt"
	"sort"
	"sync"
)

type Registry struct {
	mu    sync.RWMutex
	items map[string]Definition
}

func NewRegistry(definitions ...Definition) (*Registry, error) {
	r := &Registry{items: map[string]Definition{}}
	for _, def := range definitions {
		if err := r.Register(def); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Register(def Definition) error {
	if def.ID == "" {
		return fmt.Errorf("workflow id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[def.ID]; ok {
		return fmt.Errorf("workflow %q already registered", def.ID)
	}
	r.items[def.ID] = def
	return nil
}

func (r *Registry) Get(id string) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.items[id]
	return def, ok
}

func (r *Registry) List() []Definition {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.items))
	for id := range r.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Definition, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.items[id])
	}
	return out
}
