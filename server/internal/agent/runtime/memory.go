package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
	"github.com/redis/go-redis/v9"

	agentbuiltin "xiaoli/server/internal/agent/tool/builtin"
)

const maxHistoryMessages = 40
const storageBackendLocal = "local"

type Memory struct {
	client             *redis.Client
	prefix             string
	ttl                time.Duration
	localDataDir       string
	conversationDir    string
	memoryFile         string
	historyMaxMessages int
}

type MemoryReader interface {
	Enabled() bool
	Prefix() string
	Client() *redis.Client
	MemoryBackends(channelName, deviceID string) *agentbuiltin.MemoryBackends
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

func NewMemory(cfg Config) *Memory {
	if strings.EqualFold(strings.TrimSpace(cfg.StorageBackend), storageBackendLocal) {
		return NewLocalMemory(cfg)
	}
	return NewRedisMemory(cfg)
}

func NewLocalMemory(cfg Config) *Memory {
	dataDir := expandHome(strings.TrimSpace(cfg.LocalDataDir))
	if dataDir == "" {
		dataDir = ".xiaoli"
	}
	conversationDir := strings.TrimSpace(cfg.LocalConversationDir)
	if conversationDir == "" {
		conversationDir = "conversations"
	}
	memoryFile := strings.TrimSpace(cfg.LocalMemoryFile)
	if memoryFile == "" {
		memoryFile = "Memory.md"
	}
	maxMessages := cfg.LocalHistoryMaxMessages
	if maxMessages <= 0 {
		maxMessages = maxHistoryMessages
	}
	m := &Memory{
		localDataDir:       dataDir,
		conversationDir:    conversationDir,
		memoryFile:         memoryFile,
		historyMaxMessages: maxMessages,
	}
	logger.Infof("local memory ready, data_dir=%s conversation_dir=%s memory_file=%s", dataDir, conversationDir, memoryFile)
	return m
}

func (m *Memory) Enabled() bool {
	return m != nil && (m.client != nil || m.localDataDir != "")
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

func (m *Memory) ConversationPath(conversationID string) string {
	if m == nil || m.localDataDir == "" {
		return ""
	}
	return filepath.Join(m.localDataDir, m.conversationDir, safeFileID(conversationID)+".json")
}

func (m *Memory) MemoryBackends(channelName, deviceID string) *agentbuiltin.MemoryBackends {
	if m == nil {
		return nil
	}
	if m.client != nil {
		return &agentbuiltin.MemoryBackends{
			Global:  agentbuiltin.NewMemoryBackendScoped(m.client, m.prefix, channelName, deviceID, "global"),
			Channel: agentbuiltin.NewMemoryBackend(m.client, m.prefix, channelName, deviceID),
		}
	}
	if m.localDataDir != "" {
		return &agentbuiltin.MemoryBackends{
			Global:  agentbuiltin.NewFileMemoryBackend(filepath.Join(m.localDataDir, m.memoryFile)),
			Channel: agentbuiltin.NewFileMemoryBackend(filepath.Join(m.localDataDir, "state", "memories", safeFileID(channelName+"-"+deviceID)+".md")),
		}
	}
	return nil
}

func (m *Memory) List(ctx context.Context, limit int) ([]MemoryKeyInfo, error) {
	if !m.Enabled() {
		return nil, errors.New("memory is not configured")
	}
	if m.localDataDir != "" {
		return m.listLocal(ctx, limit)
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
		return MemoryValue{}, errors.New("memory is not configured")
	}
	if m.localDataDir != "" {
		path := m.ConversationPath(conversationID)
		data, err := os.ReadFile(path)
		if err != nil {
			return MemoryValue{}, err
		}
		return MemoryValue{
			Key:      path,
			DeviceID: conversationID,
			Bytes:    len(data),
			Raw:      data,
		}, nil
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
	if m.localDataDir != "" {
		data, err := os.ReadFile(m.ConversationPath(conversationID))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			logger.Infof("local load memory for %s: %v", conversationID, err)
			return nil
		}
		var msgs []*schema.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			logger.Infof("local unmarshal memory for %s: %v", conversationID, err)
			return nil
		}
		return msgs
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
	limit := maxHistoryMessages
	if m.historyMaxMessages > 0 {
		limit = m.historyMaxMessages
	}
	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
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
		logger.Infof("marshal memory for %s: %v", conversationID, err)
		return err
	}
	if m.localDataDir != "" {
		path := m.ConversationPath(conversationID)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0600); err != nil {
			logger.Infof("local save memory for %s: %v", conversationID, err)
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			logger.Infof("local save memory for %s: %v", conversationID, err)
			return err
		}
		return nil
	}
	if err := m.client.Set(ctx, m.prefix+conversationID, data, m.ttl).Err(); err != nil {
		logger.Infof("redis save memory for %s: %v", conversationID, err)
		return err
	}
	return nil
}

func (m *Memory) listLocal(_ context.Context, limit int) ([]MemoryKeyInfo, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	root := filepath.Join(m.localDataDir, m.conversationDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]MemoryKeyInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info := MemoryKeyInfo{
			Key:        path,
			DeviceID:   strings.TrimSuffix(entry.Name(), ".json"),
			TTLSeconds: -1,
		}
		if stat, err := entry.Info(); err == nil {
			info.Bytes = int(stat.Size())
		}
		items = append(items, info)
		if len(items) >= limit {
			break
		}
	}
	return items, nil
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func safeFileID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", string(os.PathSeparator), "_")
	return replacer.Replace(id)
}
