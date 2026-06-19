package session

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
	"github.com/redis/go-redis/v9"
)

type Info struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	UserID    string `json:"user_id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Count     int    `json:"count"`
}

type Manager struct {
	client *redis.Client
	prefix string
}

func NewManager(client *redis.Client, prefix string) *Manager {
	return &Manager{client: client, prefix: prefix}
}

func (m *Manager) activeKey(userID string) string {
	return m.prefix + "active:" + userID
}

func (m *Manager) sessionKey(sessionID string) string {
	return m.prefix + "ses:" + sessionID
}

func (m *Manager) GetOrCreate(ctx context.Context, userID string, model string) (string, bool, error) {
	sessionID, err := m.client.Get(ctx, m.activeKey(userID)).Result()
	if err == nil && sessionID != "" {
		exists, _ := m.client.Exists(ctx, m.sessionKey(sessionID)).Result()
		if exists > 0 {
			return sessionID, false, nil
		}
	}
	return m.Create(ctx, userID, model)
}

func (m *Manager) Create(ctx context.Context, userID string, model string) (string, bool, error) {
	sessionID := randomID()
	now := time.Now().Format(time.RFC3339)
	err := m.client.HSet(ctx, m.sessionKey(sessionID), map[string]any{
		"title":      "新会话",
		"model":      model,
		"user_id":    userID,
		"created_at": now,
		"updated_at": now,
		"count":      0,
	}).Err()
	if err != nil {
		logger.Infof("session create failed: %v", err)
		return "", false, err
	}
	if err := m.client.Set(ctx, m.activeKey(userID), sessionID, 0).Err(); err != nil {
		m.client.Del(ctx, m.sessionKey(sessionID))
		logger.Infof("session active set failed: %v", err)
		return "", false, err
	}
	logger.Infof("session created: %s for user=%s model=%s", sessionID, userID, model)
	return sessionID, true, nil
}

func (m *Manager) SetTitle(ctx context.Context, sessionID, title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	runes := []rune(title)
	if len(runes) > 20 {
		title = string(runes[:20]) + "…"
	}
	m.client.HSet(ctx, m.sessionKey(sessionID), "title", title)
}

func (m *Manager) List(ctx context.Context, userID string) ([]Info, error) {
	pattern := m.prefix + "ses:*"
	var cursor uint64
	var sessions []Info
	for {
		keys, next, err := m.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			data, err := m.client.HGetAll(ctx, key).Result()
			if err != nil || len(data) == 0 {
				continue
			}
			if data["user_id"] != userID {
				continue
			}
			sessions = append(sessions, infoFromHash(key, data))
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return sessions, nil
}

func (m *Manager) Get(ctx context.Context, sessionID string) (Info, error) {
	key := m.sessionKey(sessionID)
	data, err := m.client.HGetAll(ctx, key).Result()
	if err != nil {
		return Info{}, err
	}
	if len(data) == 0 {
		return Info{}, fmt.Errorf("session not found: %s", sessionID)
	}
	return infoFromHash(key, data), nil
}

func (m *Manager) UpdateAfterChat(ctx context.Context, sessionID string, count int) {
	m.client.HSet(ctx, m.sessionKey(sessionID), map[string]any{
		"updated_at": time.Now().Format(time.RFC3339),
		"count":      count,
	})
}

type EpochResult int

const (
	EpochUnchanged EpochResult = iota
	EpochInitialized
	EpochUpdated
)

func (m *Manager) epochKey(sessionID string) string {
	return m.sessionKey(sessionID) + ":epoch"
}

func (m *Manager) ReconcileEpoch(ctx context.Context, sessionID string, today string) EpochResult {
	baseline, err := m.client.Get(ctx, m.epochKey(sessionID)).Result()
	if err == redis.Nil || baseline == "" {
		return EpochInitialized
	}
	if err != nil {
		logger.Infof("epoch read failed: %v", err)
		return EpochUnchanged
	}
	if baseline == today {
		return EpochUnchanged
	}
	return EpochUpdated
}

func (m *Manager) CommitEpoch(ctx context.Context, sessionID string, today string) {
	m.client.Set(ctx, m.epochKey(sessionID), today, 0)
}

func (m *Manager) LoadMessages(ctx context.Context, sessionID string) []*schema.Message {
	key := m.prefix + sessionID
	data, err := m.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil
	}
	var msgs []*schema.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil
	}
	return msgs
}

func infoFromHash(key string, data map[string]string) Info {
	parts := strings.Split(key, ":")
	id := parts[len(parts)-1]
	count := 0
	fmt.Sscanf(data["count"], "%d", &count)
	return Info{
		ID:        id,
		Title:     data["title"],
		Model:     data["model"],
		UserID:    data["user_id"],
		CreatedAt: data["created_at"],
		UpdatedAt: data["updated_at"],
		Count:     count,
	}
}

func randomID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return "ses_" + string(b)
}