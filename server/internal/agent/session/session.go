package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
	"github.com/redis/go-redis/v9"
)

const sessionTTL = 72 * time.Hour

type Info struct {
	ID          string `json:"id"`
	ChannelName string `json:"channel_name"`
	ChannelUser string `json:"channel_user"`
	Title       string `json:"title"`
	Model       string `json:"model"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	Count       int    `json:"count"`
}

type ChannelEntry struct {
	ChannelName string `json:"channel_name"`
	ChannelUser string `json:"channel_user"`
	SessionID   string `json:"session_id"`
	TTLSeconds  int64  `json:"ttl_seconds"`
}

type Manager struct {
	client *redis.Client
	prefix string
}

func NewManager(client *redis.Client, prefix string) *Manager {
	return &Manager{client: client, prefix: prefix}
}

func (m *Manager) channelKey(channelName, channelUser string) string {
	return m.prefix + "channel:" + enc(channelName) + ":" + enc(channelUser)
}

func (m *Manager) sessionKey(sessionID string) string {
	return m.prefix + "ses:" + sessionID
}

func (m *Manager) metaKey(sessionID string) string {
	return m.sessionKey(sessionID) + ":meta"
}

func (m *Manager) GetOrCreate(ctx context.Context, channelName, channelUser, model string) (string, bool, error) {
	sessionID, err := m.client.Get(ctx, m.channelKey(channelName, channelUser)).Result()
	if err == nil && sessionID != "" {
		exists, _ := m.client.Exists(ctx, m.metaKey(sessionID)).Result()
		if exists > 0 {
			return sessionID, false, nil
		}
	}
	return m.Create(ctx, channelName, channelUser, model)
}

func (m *Manager) Create(ctx context.Context, channelName, channelUser, model string) (string, bool, error) {
	sessionID := randomID()
	now := time.Now().Format(time.RFC3339)
	pipe := m.client.Pipeline()
	pipe.HSet(ctx, m.metaKey(sessionID), map[string]any{
		"id":           sessionID,
		"channel_name": channelName,
		"channel_user": channelUser,
		"title":        "新会话",
		"model":        model,
		"created_at":   now,
		"updated_at":   now,
		"count":        0,
	})
	pipe.Expire(ctx, m.metaKey(sessionID), sessionTTL)
	pipe.Set(ctx, m.channelKey(channelName, channelUser), sessionID, sessionTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Infof("session create failed: %v", err)
		return "", false, err
	}
	logger.Infof("session created: %s channel=%s user=%s model=%s", sessionID, channelName, channelUser, model)
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
	m.client.HSet(ctx, m.metaKey(sessionID), "title", title)
	m.client.Expire(ctx, m.metaKey(sessionID), sessionTTL)
}

func (m *Manager) epochKey(channelName, channelUser string) string {
	return m.prefix + "epoch:" + enc(channelName) + ":" + enc(channelUser)
}

func (m *Manager) GetEpoch(ctx context.Context, channelName, channelUser string) string {
	val, err := m.client.Get(ctx, m.epochKey(channelName, channelUser)).Result()
	if err != nil {
		return ""
	}
	return val
}

func (m *Manager) SetEpoch(ctx context.Context, channelName, channelUser, day string) {
	m.client.Set(ctx, m.epochKey(channelName, channelUser), day, sessionTTL)
}

func (m *Manager) ListByChannel(ctx context.Context, channelName, channelUser string) ([]Info, error) {
	pattern := m.prefix + "ses:*:meta"
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
			if data["channel_name"] != channelName || data["channel_user"] != channelUser {
				continue
			}
			sessions = append(sessions, infoFromHash(key, data))
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	return sessions, nil
}

func (m *Manager) ListChannels(ctx context.Context) ([]ChannelEntry, error) {
	pattern := m.prefix + "channel:*"
	var cursor uint64
	var entries []ChannelEntry
	for {
		keys, next, err := m.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			sessionID, err := m.client.Get(ctx, key).Result()
			if err != nil {
				continue
			}
			rest := strings.TrimPrefix(key, m.prefix+"channel:")
			parts := strings.SplitN(rest, ":", 2)
			chName, chUser := "", ""
			if len(parts) >= 1 {
				chName = dec(parts[0])
			}
			if len(parts) >= 2 {
				chUser = dec(parts[1])
			}
			ttl, _ := m.client.TTL(ctx, key).Result()
			entries = append(entries, ChannelEntry{
				ChannelName: chName,
				ChannelUser: chUser,
				SessionID:   sessionID,
				TTLSeconds:  int64(ttl.Seconds()),
			})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ChannelName != entries[j].ChannelName {
			return entries[i].ChannelName < entries[j].ChannelName
		}
		return entries[i].ChannelUser < entries[j].ChannelUser
	})
	return entries, nil
}

func (m *Manager) GetChannelSession(ctx context.Context, channelName, channelUser string) string {
	sessionID, err := m.client.Get(ctx, m.channelKey(channelName, channelUser)).Result()
	if err != nil {
		return ""
	}
	return sessionID
}

func (m *Manager) Get(ctx context.Context, sessionID string) (Info, error) {
	data, err := m.client.HGetAll(ctx, m.metaKey(sessionID)).Result()
	if err != nil {
		return Info{}, err
	}
	if len(data) == 0 {
		return Info{}, fmt.Errorf("session not found: %s", sessionID)
	}
	return infoFromHash("", data), nil
}

func (m *Manager) UpdateAfterChat(ctx context.Context, sessionID string, count int) {
	m.client.HSet(ctx, m.metaKey(sessionID), map[string]any{
		"updated_at": time.Now().Format(time.RFC3339),
		"count":      count,
	})
	m.client.Expire(ctx, m.metaKey(sessionID), sessionTTL)
	data, err := m.client.HGetAll(ctx, m.metaKey(sessionID)).Result()
	if err == nil && data["channel_name"] != "" && data["channel_user"] != "" {
		m.client.Expire(ctx, m.channelKey(data["channel_name"], data["channel_user"]), sessionTTL)
		m.client.Expire(ctx, m.epochKey(data["channel_name"], data["channel_user"]), sessionTTL)
	}
}

func (m *Manager) LoadMessages(ctx context.Context, sessionID string) []*schema.Message {
	data, err := m.client.Get(ctx, m.sessionKey(sessionID)).Bytes()
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
	id := data["id"]
	if id == "" {
		id = data["channel_user"]
	}
	count := 0
	fmt.Sscanf(data["count"], "%d", &count)
	return Info{
		ID:          id,
		ChannelName: data["channel_name"],
		ChannelUser: data["channel_user"],
		Title:       data["title"],
		Model:       data["model"],
		CreatedAt:   data["created_at"],
		UpdatedAt:   data["updated_at"],
		Count:       count,
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

func enc(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func dec(s string) string {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	return string(b)
}