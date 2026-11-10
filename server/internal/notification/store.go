package notification

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Notification struct {
	ID              string     `json:"id"`
	Owner           string     `json:"owner"`
	Title           string     `json:"title"`
	Content         string     `json:"content"`
	TargetDeviceIDs []string   `json:"target_device_ids"`
	PublishedAt     time.Time  `json:"published_at"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
}

type Delivery struct {
	NotificationID string     `json:"notification_id"`
	DeviceID       string     `json:"device_id"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
}

type Snapshot struct {
	Notifications []Notification `json:"notifications"`
	Deliveries    []Delivery     `json:"deliveries"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func NewID() (string, error) {
	body := make([]byte, 16)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return "ntf_" + base64.RawURLEncoding.EncodeToString(body), nil
}

func (s *Store) Create(item Notification) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Content) == "" {
		return errors.New("notification id, title and content are required")
	}
	item.TargetDeviceIDs = uniqueDeviceIDs(item.TargetDeviceIDs)
	if len(item.TargetDeviceIDs) == 0 {
		return errors.New("at least one target device is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.loadLocked()
	if err != nil {
		return err
	}
	for _, existing := range snapshot.Notifications {
		if existing.ID == item.ID {
			return errors.New("notification id already exists")
		}
	}
	snapshot.Notifications = append(snapshot.Notifications, item)
	for _, deviceID := range item.TargetDeviceIDs {
		snapshot.Deliveries = append(snapshot.Deliveries, Delivery{NotificationID: item.ID, DeviceID: deviceID})
	}
	return s.saveLocked(snapshot)
}

func (s *Store) List() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.loadLocked()
	if err != nil {
		return Snapshot{}, err
	}
	sort.Slice(snapshot.Notifications, func(i, j int) bool {
		if snapshot.Notifications[i].PublishedAt.Equal(snapshot.Notifications[j].PublishedAt) {
			return snapshot.Notifications[i].ID > snapshot.Notifications[j].ID
		}
		return snapshot.Notifications[i].PublishedAt.After(snapshot.Notifications[j].PublishedAt)
	})
	return snapshot, nil
}

func (s *Store) Pending(deviceID string, now time.Time) ([]Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	pending := map[string]bool{}
	for _, delivery := range snapshot.Deliveries {
		if delivery.DeviceID == deviceID && delivery.DeliveredAt == nil {
			pending[delivery.NotificationID] = true
		}
	}
	items := make([]Notification, 0)
	for _, item := range snapshot.Notifications {
		if !pending[item.ID] || item.RevokedAt != nil || expired(item, now) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].PublishedAt.Equal(items[j].PublishedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].PublishedAt.Before(items[j].PublishedAt)
	})
	return items, nil
}

func (s *Store) MarkDelivered(notificationID, deviceID string, deliveredAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	for index := range snapshot.Deliveries {
		delivery := &snapshot.Deliveries[index]
		if delivery.NotificationID != notificationID || delivery.DeviceID != deviceID {
			continue
		}
		if delivery.DeliveredAt == nil {
			at := deliveredAt
			delivery.DeliveredAt = &at
			if err := s.saveLocked(snapshot); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	return false, nil
}

func (s *Store) Revoke(notificationID string, revokedAt time.Time) (Notification, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.loadLocked()
	if err != nil {
		return Notification{}, false, err
	}
	for index := range snapshot.Notifications {
		item := &snapshot.Notifications[index]
		if item.ID != notificationID {
			continue
		}
		if item.RevokedAt == nil {
			at := revokedAt
			item.RevokedAt = &at
			if err := s.saveLocked(snapshot); err != nil {
				return Notification{}, false, err
			}
		}
		return *item, true, nil
	}
	return Notification{}, false, nil
}

func (s *Store) loadLocked() (Snapshot, error) {
	snapshot := Snapshot{Notifications: []Notification{}, Deliveries: []Delivery{}}
	if s.path == "" {
		return snapshot, nil
	}
	body, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	if len(body) == 0 {
		return snapshot, nil
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Notifications == nil {
		snapshot.Notifications = []Notification{}
	}
	if snapshot.Deliveries == nil {
		snapshot.Deliveries = []Delivery{}
	}
	return snapshot, nil
}

func (s *Store) saveLocked(snapshot Snapshot) error {
	if s.path == "" {
		return nil
	}
	body, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".notifications-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

func expired(item Notification, now time.Time) bool {
	return item.ExpiresAt != nil && !now.Before(*item.ExpiresAt)
}

func uniqueDeviceIDs(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
