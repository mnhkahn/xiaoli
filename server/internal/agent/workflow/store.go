package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ReminderTriggerType 触发类型
type ReminderTriggerType string

const (
	ReminderOnce     ReminderTriggerType = "once"     // 一次性，到 At 触发一次
	ReminderDaily    ReminderTriggerType = "daily"    // 每天 AtHour:AtMinute
	ReminderInterval ReminderTriggerType = "interval" // 间隔 Every，带时间窗
)

// ReminderTrigger 触发配置（JSON 友好，时间用 RFC3339 字符串）
type ReminderTrigger struct {
	Type      ReminderTriggerType `json:"type"`
	At        string              `json:"at,omitempty"`         // once：RFC3339 绝对时间
	AtHour    *int                `json:"at_hour,omitempty"`    // daily
	AtMinute  *int                `json:"at_minute,omitempty"`  // daily
	Every     string              `json:"every,omitempty"`      // interval：如 "5m"
	StartHour int                 `json:"start_hour,omitempty"` // interval 时间窗
	EndHour   int                 `json:"end_hour,omitempty"`
	Timezone  string              `json:"timezone,omitempty"`
}

// Reminder 一条提醒记录
type Reminder struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Action    string          `json:"action"` // speak | agent | notify
	Trigger   ReminderTrigger `json:"trigger"`
	Text      string          `json:"text,omitempty"`
	Metadata  map[string]any  `json:"metadata,omitempty"`
	CreatedAt string          `json:"created_at,omitempty"`
	FiredAt   string          `json:"fired_at,omitempty"` // once 执行后写入，非空表示已触发
}

// ReminderStore 读写 reminders.json（JSON 数组），并发安全 + 原子写
type ReminderStore struct {
	path string
	mu   sync.Mutex
}

func NewReminderStore(path string) *ReminderStore {
	return &ReminderStore{path: path}
}

// Load 读取全部提醒；文件不存在返回空列表（不报错）
func (s *ReminderStore) Load() ([]Reminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *ReminderStore) loadLocked() ([]Reminder, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var reminders []Reminder
	if err := json.Unmarshal(data, &reminders); err != nil {
		return nil, fmt.Errorf("parse reminders %s: %w", s.path, err)
	}
	return reminders, nil
}

// Add 追加一条提醒
func (s *ReminderStore) Add(r Reminder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reminders, err := s.loadLocked()
	if err != nil {
		return err
	}
	reminders = append(reminders, r)
	return s.saveLocked(reminders)
}

// Delete 按 ID 删除，返回是否删除成功
func (s *ReminderStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reminders, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	out := reminders[:0]
	removed := false
	for _, r := range reminders {
		if r.ID == id {
			removed = true
			continue
		}
		out = append(out, r)
	}
	if !removed {
		return false, nil
	}
	return true, s.saveLocked(out)
}

// MarkFired 把指定 once 提醒标记为已触发（写入 fired_at）
func (s *ReminderStore) MarkFired(id string, firedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reminders, err := s.loadLocked()
	if err != nil {
		return err
	}
	changed := false
	for i := range reminders {
		if reminders[i].ID == id {
			reminders[i].FiredAt = firedAt.Format(time.RFC3339)
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked(reminders)
}

// saveLocked 原子写：写临时文件再 rename
func (s *ReminderStore) saveLocked(reminders []Reminder) error {
	if reminders == nil {
		reminders = []Reminder{}
	}
	data, err := json.MarshalIndent(reminders, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".reminders-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// ToDefinition 把 Reminder 转成调度用的 Definition
func (r Reminder) ToDefinition() (Definition, bool) {
	spec := CronSpec{Timezone: r.Trigger.Timezone}
	if spec.Timezone == "" {
		spec.Timezone = "Asia/Shanghai"
	}
	switch r.Trigger.Type {
	case ReminderOnce:
		at, err := time.Parse(time.RFC3339, r.Trigger.At)
		if err != nil {
			return Definition{}, false
		}
		spec.At = &at
	case ReminderDaily:
		spec.AtHour = r.Trigger.AtHour
		spec.AtMinute = r.Trigger.AtMinute
	case ReminderInterval:
		every, err := time.ParseDuration(r.Trigger.Every)
		if err != nil || every <= 0 {
			return Definition{}, false
		}
		spec.Every = every
		spec.StartHour = r.Trigger.StartHour
		spec.EndHour = r.Trigger.EndHour
	default:
		return Definition{}, false
	}

	metadata := r.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	if r.Text != "" {
		if _, ok := metadata["text"]; !ok {
			metadata["text"] = r.Text
		}
	}

	return Definition{
		ID:      r.ID,
		Name:    r.Name,
		Enabled: r.Enabled,
		Action:  r.Action,
		Trigger: Trigger{Kind: TriggerCron, Cron: &spec},
		Metadata: metadata,
	}, true
}

// IsOnceFired 一次性且已触发
func (r Reminder) IsOnceFired() bool {
	return r.Trigger.Type == ReminderOnce && r.FiredAt != ""
}
