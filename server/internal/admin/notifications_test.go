package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mnhkahn/xiaoli/server/internal/notification"
)

func TestNotificationAdminCreateListAndRevoke(t *testing.T) {
	cfg := testConfig()
	cfg.DataDir = t.TempDir()
	cfg.DirectDeviceServer = true
	cfg.AllowedDeviceIDs = []string{"device-1", "device-2"}
	srv := NewServer(cfg)
	user := map[string]any{"sub": "user-1", "email": "one@example.test"}
	expiresAt := cfg.now().Add(time.Hour).Format(time.RFC3339)
	body := `{"title":"学习提醒","content":"下午四点有英语课","all_devices":true,"expires_at":"` + expiresAt + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/notifications", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleNotifications(rr, req, user)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rr.Code, rr.Body.String())
	}

	snapshot, err := srv.notificationSt.List()
	if err != nil || len(snapshot.Notifications) != 1 || len(snapshot.Deliveries) != 2 {
		t.Fatalf("snapshot = %#v, err = %v", snapshot, err)
	}
	item := snapshot.Notifications[0]
	if item.Owner != "user-1" || len(item.TargetDeviceIDs) != 2 {
		t.Fatalf("notification = %#v", item)
	}

	rr = httptest.NewRecorder()
	srv.handleNotifications(rr, httptest.NewRequest(http.MethodGet, "/admin/api/notifications", nil), map[string]any{"sub": "user-2"})
	if rr.Code != http.StatusOK {
		t.Fatalf("other user list status = %d", rr.Code)
	}
	var otherList struct {
		Notifications []adminNotification `json:"notifications"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &otherList); err != nil || len(otherList.Notifications) != 0 {
		t.Fatalf("other user notifications = %#v, err = %v", otherList.Notifications, err)
	}

	revokePath := "/admin/api/notifications/" + item.ID + "/revoke"
	rr = httptest.NewRecorder()
	srv.handleNotificationRevoke(rr, httptest.NewRequest(http.MethodPost, revokePath, nil), map[string]any{"sub": "user-2"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("other user revoke status = %d, want 404", rr.Code)
	}
	rr = httptest.NewRecorder()
	srv.handleNotificationRevoke(rr, httptest.NewRequest(http.MethodPost, revokePath, nil), user)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, body = %s", rr.Code, rr.Body.String())
	}
	pending, err := srv.notificationSt.Pending("device-1", cfg.now())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after revoke = %#v, err = %v", pending, err)
	}
}

func TestNotificationAdminRejectsOtherUsersDevice(t *testing.T) {
	cfg := testConfig()
	cfg.DataDir = t.TempDir()
	cfg.DirectDeviceServer = true
	srv := NewServer(cfg)
	code, _, err := srv.deviceRegistry.CreatePairing("user-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.deviceRegistry.Claim(code, "paired-device-1", "学习平板", "android"); err != nil {
		t.Fatal(err)
	}
	body := `{"title":"越权通知","content":"不应创建","device_ids":["paired-device-1"]}`
	rr := httptest.NewRecorder()
	srv.handleNotifications(rr, httptest.NewRequest(http.MethodPost, "/admin/api/notifications", bytes.NewBufferString(body)), map[string]any{"sub": "user-2"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body = %s", rr.Code, rr.Body.String())
	}
}

type notificationWSFrame struct {
	Type         string `json:"type"`
	SessionID    string `json:"session_id"`
	Notification struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"notification"`
}

func readNotificationWSFrame(t *testing.T, conn *websocket.Conn) notificationWSFrame {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var frame notificationWSFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type == "notification" {
			return frame
		}
	}
}

func waitNotificationDelivery(t *testing.T, store *notification.Store, notificationID, deviceID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := store.List()
		if err != nil {
			t.Fatal(err)
		}
		for _, delivery := range snapshot.Deliveries {
			if delivery.NotificationID == notificationID && delivery.DeviceID == deviceID && delivery.DeliveredAt != nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("notification %s was not acknowledged by %s", notificationID, deviceID)
}

func TestNotificationWebSocketResendPushAndACK(t *testing.T) {
	cfg := testConfig()
	cfg.DataDir = t.TempDir()
	cfg.DirectDeviceServer = true
	cfg.DeviceAuthEnabled = false
	srv := NewServer(cfg)
	item := notification.Notification{
		ID: "ntf_offline", Owner: "user-1", Title: "离线通知", Content: "连接后补发",
		TargetDeviceIDs: []string{"device-1"}, PublishedAt: cfg.now(), CreatedAt: cfg.now(),
	}
	if err := srv.notificationSt.Create(item); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv)
	defer httpServer.Close()
	wsURL := strings.Replace(httpServer.URL, "http://", "ws://", 1) + "/xiaozhi/v1/"
	header := http.Header{"Device-Id": []string{"device-1"}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{
		"type": "hello", "version": 1, "transport": "websocket",
		"features": map[string]any{"mcp": true, "notifications": true},
	}); err != nil {
		t.Fatal(err)
	}

	frame := readNotificationWSFrame(t, conn)
	if frame.Notification.ID != item.ID || frame.Notification.Content != item.Content || frame.SessionID == "" {
		t.Fatalf("offline frame = %#v", frame)
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "notification_ack", "version": 1,
		"session_id": frame.SessionID, "notification_id": frame.Notification.ID,
	}); err != nil {
		t.Fatal(err)
	}
	waitNotificationDelivery(t, srv.notificationSt, item.ID, "device-1")

	online := notification.Notification{
		ID: "ntf_online", Owner: "user-1", Title: "在线通知", Content: "立即推送",
		TargetDeviceIDs: []string{"device-1"}, PublishedAt: cfg.now(), CreatedAt: cfg.now(),
	}
	if err := srv.notificationSt.Create(online); err != nil {
		t.Fatal(err)
	}
	srv.deviceHub.PublishNotification(online)
	frame = readNotificationWSFrame(t, conn)
	if frame.Notification.ID != online.ID || frame.Notification.Content != online.Content {
		t.Fatalf("online frame = %#v", frame)
	}
}
