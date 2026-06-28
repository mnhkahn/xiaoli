package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
	"github.com/redis/go-redis/v9"
)

const maxHistoryMessages = 40

type Memory struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

type MemoryReader interface {
	Enabled() bool
	Prefix() string
	Client() *redis.Client
	List(ctx context.Context, limit int) ([]MemoryKeyInfo, error)
	LoadRaw(ctx context.Context, conversationID string) (MemoryValue, error)
}

type MemoryKeyInfo struct {
	Key        string `json:"key"`
	DeviceID   string `json:"device_id"`
	TTLSeconds int64  `json:"ttl_seconds"`
	Bytes      int    `json:"bytes"`
}

type MemoryValue struct {
	Key        string `json:"key"`
	DeviceID   string `json:"device_id"`
	TTLSeconds int64  `json:"ttl_seconds"`
	Bytes      int    `json:"bytes"`
	Raw        []byte `json:"-"`
}

func NewRedisMemory(cfg Config) *Memory {
	if cfg.RedisURL == "" {
		return nil
	}
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Infof("redis url parse failed: %v", err)
		return nil
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		logger.Infof("redis ping failed: %v", err)
		return nil
	}
	logger.Infof("redis memory connected, prefix=%s ttl=%s", cfg.RedisKeyPrefix, cfg.MemoryTTL)
	return &Memory{client: client, prefix: cfg.RedisKeyPrefix, ttl: cfg.MemoryTTL}
}

func (m *Memory) Enabled() bool {
	return m != nil && m.client != nil
}

func (m *Memory) Prefix() string {
	if m == nil {
		return ""
	}
	return m.prefix
}

func (m *Memory) Client() *redis.Client {
	if m == nil {
		return nil
	}
	return m.client
}

func (m *Memory) List(ctx context.Context, limit int) ([]MemoryKeyInfo, error) {
	if !m.Enabled() {
		return nil, errors.New("redis memory is not configured")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	pattern := m.prefix + "*"
	var cursor uint64
	items := make([]MemoryKeyInfo, 0)
	for {
		keys, next, err := m.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			info := MemoryKeyInfo{
				Key:      key,
				DeviceID: strings.TrimPrefix(key, m.prefix),
			}
			if ttl, err := m.client.TTL(ctx, key).Result(); err == nil {
				info.TTLSeconds = ttlSeconds(ttl)
			}
			if size, err := m.client.StrLen(ctx, key).Result(); err == nil {
				info.Bytes = int(size)
			}
			items = append(items, info)
			if len(items) >= limit {
				return items, nil
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return items, nil
}

func (m *Memory) LoadRaw(ctx context.Context, conversationID string) (MemoryValue, error) {
	if !m.Enabled() {
		return MemoryValue{}, errors.New("redis memory is not configured")
	}
	key := m.prefix + conversationID
	data, err := m.client.Get(ctx, key).Bytes()
	if err != nil {
		return MemoryValue{}, err
	}
	value := MemoryValue{
		Key:      key,
		DeviceID: conversationID,
		Bytes:    len(data),
		Raw:      data,
	}
	if ttl, err := m.client.TTL(ctx, key).Result(); err == nil {
		value.TTLSeconds = ttlSeconds(ttl)
	}
	return value, nil
}

func ttlSeconds(ttl time.Duration) int64 {
	if ttl < 0 {
		return int64(ttl)
	}
	return int64(ttl.Seconds())
}

func (m *Memory) Load(ctx context.Context, conversationID string) []*schema.Message {
	if m == nil {
		return nil
	}
	data, err := m.client.Get(ctx, m.prefix+conversationID).Bytes()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		logger.Infof("redis load memory for %s: %v", conversationID, err)
		return nil
	}
	var msgs []*schema.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		logger.Infof("redis unmarshal memory for %s: %v", conversationID, err)
		return nil
	}
	return msgs
}

func (m *Memory) Save(ctx context.Context, conversationID string, msgs []*schema.Message) error {
	if m == nil {
		return nil
	}
	if len(msgs) > maxHistoryMessages {
		msgs = msgs[len(msgs)-maxHistoryMessages:]
	}
	// 给没有时间戳的消息加上当前时间
	now := time.Now().Format(time.RFC3339)
	for _, msg := range msgs {
		if msg.Extra == nil {
			msg.Extra = make(map[string]any)
		}
		if _, ok := msg.Extra["timestamp"]; !ok {
			msg.Extra["timestamp"] = now
		}
	}
	data, err := json.Marshal(msgs)
	if err != nil {
		logger.Infof("redis marshal memory for %s: %v", conversationID, err)
		return err
	}
	if err := m.client.Set(ctx, m.prefix+conversationID, data, m.ttl).Err(); err != nil {
		logger.Infof("redis save memory for %s: %v", conversationID, err)
		return err
	}
	return nil
}
