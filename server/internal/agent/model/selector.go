package model

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Role string

const (
	RoleLLM  Role = "llm"
	RoleVLLM Role = "vllm"
	RoleASR  Role = "asr"
	RoleTTS  Role = "tts"
)

type Option struct {
	ID          string
	Role        Role
	DisplayName string
	Provider    string
}

type Selector struct {
	mu      sync.RWMutex
	current map[Role]string
	options map[Role]map[string]Option
}

func NewSelector(defaults map[Role]string, options map[Role][]Option) *Selector {
	s := &Selector{
		current: map[Role]string{},
		options: map[Role]map[string]Option{},
	}
	for role, items := range options {
		if s.options[role] == nil {
			s.options[role] = map[string]Option{}
		}
		for _, item := range items {
			item.ID = strings.TrimSpace(item.ID)
			if item.ID == "" {
				continue
			}
			if item.Role == "" {
				item.Role = role
			}
			s.options[role][item.ID] = item
		}
	}
	for role, id := range defaults {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		s.current[role] = id
		if s.options[role] == nil {
			s.options[role] = map[string]Option{}
		}
		if _, ok := s.options[role][id]; !ok {
			s.options[role][id] = Option{ID: id, Role: role}
		}
	}
	return s
}

func (s *Selector) Current(role Role) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current[role]
}

func (s *Selector) Use(role Role, id string) error {
	if s == nil {
		return fmt.Errorf("model selector is not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("model id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.options[role][id]; !ok {
		return fmt.Errorf("model %q is not configured for %s", id, role)
	}
	s.current[role] = id
	return nil
}

func (s *Selector) List(role Role) []Option {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Option, 0, len(s.options[role]))
	for _, item := range s.options[role] {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID < items[j].ID
	})
	return items
}

func OptionsFromIDs(role Role, ids []string) []Option {
	out := make([]Option, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Option{ID: id, Role: role})
	}
	return out
}
