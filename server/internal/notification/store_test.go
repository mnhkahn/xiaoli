package notification

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStorePendingDeliveryRevokeAndPersistence(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	path := filepath.Join(t.TempDir(), "notifications.json")
	store := NewStore(path)
	item := Notification{
		ID:              "ntf_1",
		Owner:           "user-1",
		Title:           "学习提醒",
		Content:         "下午四点有英语课",
		TargetDeviceIDs: []string{"device-1", "device-2", "device-1"},
		PublishedAt:     now,
		CreatedAt:       now,
	}
	if err := store.Create(item); err != nil {
		t.Fatal(err)
	}

	pending, err := store.Pending("device-1", now)
	if err != nil || len(pending) != 1 || pending[0].ID != item.ID {
		t.Fatalf("pending = %#v, err = %v", pending, err)
	}
	if found, err := store.MarkDelivered(item.ID, "device-1", now.Add(time.Second)); err != nil || !found {
		t.Fatalf("mark delivered found = %v, err = %v", found, err)
	}
	if found, err := store.MarkDelivered(item.ID, "device-1", now.Add(2*time.Second)); err != nil || !found {
		t.Fatalf("duplicate ack found = %v, err = %v", found, err)
	}

	reopened := NewStore(path)
	if pending, err := reopened.Pending("device-1", now); err != nil || len(pending) != 0 {
		t.Fatalf("delivered device pending = %#v, err = %v", pending, err)
	}
	if pending, err := reopened.Pending("device-2", now); err != nil || len(pending) != 1 {
		t.Fatalf("other device pending = %#v, err = %v", pending, err)
	}
	if _, ok, err := reopened.Revoke(item.ID, now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("revoke ok = %v, err = %v", ok, err)
	}
	if pending, err := reopened.Pending("device-2", now); err != nil || len(pending) != 0 {
		t.Fatalf("revoked pending = %#v, err = %v", pending, err)
	}
}

func TestStorePendingSkipsExpiredNotifications(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(-time.Second)
	store := NewStore(filepath.Join(t.TempDir(), "notifications.json"))
	if err := store.Create(Notification{
		ID: "ntf_expired", Title: "过期", Content: "不再发送",
		TargetDeviceIDs: []string{"device-1"}, PublishedAt: now.Add(-time.Hour), ExpiresAt: &expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Pending("device-1", now)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %#v, err = %v", pending, err)
	}
}
