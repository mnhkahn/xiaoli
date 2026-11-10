package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mnhkahn/xiaoli/server/internal/notification"
)

type createNotificationRequest struct {
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	DeviceIDs  []string `json:"device_ids"`
	AllDevices bool     `json:"all_devices"`
	ExpiresAt  string   `json:"expires_at"`
}

type notificationDevice struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
}

type adminNotification struct {
	notification.Notification
	Deliveries []notification.Delivery `json:"deliveries"`
}

func (s *AdminServer) handleNotifications(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if s.notificationSt == nil {
		http.Error(w, "notification store is unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleListNotifications(w, r, user)
	case http.MethodPost:
		s.handleCreateNotification(w, r, user)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *AdminServer) handleListNotifications(w http.ResponseWriter, r *http.Request, user map[string]any) {
	owner := notificationOwner(user)
	if owner == "" {
		http.Error(w, "missing user subject", http.StatusBadRequest)
		return
	}
	snapshot, err := s.notificationSt.List()
	if err != nil {
		http.Error(w, "load notifications failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	deliveries := map[string][]notification.Delivery{}
	for _, delivery := range snapshot.Deliveries {
		deliveries[delivery.NotificationID] = append(deliveries[delivery.NotificationID], delivery)
	}
	items := make([]adminNotification, 0)
	for _, item := range snapshot.Notifications {
		if item.Owner != owner {
			continue
		}
		records := deliveries[item.ID]
		if records == nil {
			records = []notification.Delivery{}
		}
		items = append(items, adminNotification{Notification: item, Deliveries: records})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": items,
		"devices":       s.notificationDevices(r.Context(), user),
	})
}

func (s *AdminServer) handleCreateNotification(w http.ResponseWriter, r *http.Request, user map[string]any) {
	owner := notificationOwner(user)
	if owner == "" {
		http.Error(w, "missing user subject", http.StatusBadRequest)
		return
	}
	var request createNotificationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&request); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	request.Title = strings.TrimSpace(request.Title)
	request.Content = strings.TrimSpace(request.Content)
	if request.Title == "" || request.Content == "" {
		http.Error(w, "title and content are required", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(request.Title) > 100 || utf8.RuneCountInString(request.Content) > 2000 {
		http.Error(w, "title exceeds 100 characters or content exceeds 2000 characters", http.StatusBadRequest)
		return
	}
	now := s.cfg.now()
	var expiresAt *time.Time
	if value := strings.TrimSpace(request.ExpiresAt); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			http.Error(w, "expires_at must use RFC3339 format", http.StatusBadRequest)
			return
		}
		if !parsed.After(now) {
			http.Error(w, "expires_at must be in the future", http.StatusBadRequest)
			return
		}
		expiresAt = &parsed
	}

	var targetDeviceIDs []string
	if request.AllDevices {
		for _, device := range s.notificationDevices(r.Context(), user) {
			targetDeviceIDs = append(targetDeviceIDs, device.DeviceID)
		}
	} else {
		targetDeviceIDs = uniqueNotificationDeviceIDs(request.DeviceIDs)
		for _, deviceID := range targetDeviceIDs {
			if !s.userCanAccessDevice(user, deviceID) {
				http.Error(w, "device not found", http.StatusNotFound)
				return
			}
		}
	}
	if len(targetDeviceIDs) == 0 {
		http.Error(w, "at least one target device is required", http.StatusBadRequest)
		return
	}
	id, err := notification.NewID()
	if err != nil {
		http.Error(w, "create notification id failed", http.StatusInternalServerError)
		return
	}
	item := notification.Notification{
		ID:              id,
		Owner:           owner,
		Title:           request.Title,
		Content:         request.Content,
		TargetDeviceIDs: targetDeviceIDs,
		PublishedAt:     now,
		ExpiresAt:       expiresAt,
		CreatedBy:       firstNonEmpty(user, "email", "name", "sub"),
		CreatedAt:       now,
	}
	if err := s.notificationSt.Create(item); err != nil {
		http.Error(w, "save notification failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.deviceHub != nil {
		s.deviceHub.PublishNotification(item)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "notification": item})
}

func (s *AdminServer) handleNotificationRevoke(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.notificationSt == nil {
		http.Error(w, "notification store is unavailable", http.StatusServiceUnavailable)
		return
	}
	owner := notificationOwner(user)
	notificationID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/api/notifications/"), "/revoke")
	if owner == "" || notificationID == "" || strings.Contains(notificationID, "/") {
		http.NotFound(w, r)
		return
	}
	snapshot, err := s.notificationSt.List()
	if err != nil {
		http.Error(w, "load notifications failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	found := false
	for _, item := range snapshot.Notifications {
		if item.ID == notificationID && item.Owner == owner {
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	item, ok, err := s.notificationSt.Revoke(notificationID, s.cfg.now())
	if err != nil {
		http.Error(w, "revoke notification failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "notification": item})
}

func (s *AdminServer) notificationDevices(ctx context.Context, user map[string]any) []notificationDevice {
	owner := notificationOwner(user)
	devices := map[string]notificationDevice{}
	if s.deviceRegistry != nil {
		for _, device := range s.deviceRegistry.DevicesForOwner(owner) {
			devices[device.DeviceID] = notificationDevice{DeviceID: device.DeviceID, Name: device.Name}
		}
	}
	for _, deviceID := range s.cfg.AllowedDeviceIDs {
		if s.deviceRegistry == nil || !s.deviceRegistry.IsKnown(deviceID) {
			devices[deviceID] = notificationDevice{DeviceID: deviceID, Name: deviceID}
		}
	}
	online, err := s.deviceController().Devices(ctx)
	if err == nil {
		for _, device := range online {
			if !s.userCanAccessDevice(user, device.DeviceID) {
				continue
			}
			entry := devices[device.DeviceID]
			entry.DeviceID = device.DeviceID
			entry.Online = true
			if entry.Name == "" {
				entry.Name = device.DeviceID
			}
			devices[device.DeviceID] = entry
		}
	}
	result := make([]notificationDevice, 0, len(devices))
	for _, device := range devices {
		result = append(result, device)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DeviceID < result[j].DeviceID })
	return result
}

func notificationOwner(user map[string]any) string {
	return strings.TrimSpace(stringValue(user["sub"]))
}

func uniqueNotificationDeviceIDs(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 {
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
