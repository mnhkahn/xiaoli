package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mnhkahn/gogogo/logger"
	"github.com/redis/go-redis/v9"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	agentmedia "xiaoli/server/internal/agent/media"
	agentmcp "xiaoli/server/internal/agent/tool/mcp"
	agentskill "xiaoli/server/internal/agent/tool/skill"
)

type SpeechRecognizer = agentmedia.SpeechRecognizer
type VisionAnalyzer = agentmedia.VisionAnalyzer

func newOpenAITranscriber(cfg Config) SpeechRecognizer {
	return agentmedia.NewOpenAITranscriber(agentmedia.ASRConfig{
		URL:     cfg.GoASRURL,
		APIKey:  cfg.GoASRAPIKey,
		Model:   cfg.GoASRModel,
		Timeout: cfg.GoASRTimeout,
	})
}

func newGoVisionClient(cfg Config) VisionAnalyzer {
	return agentmedia.NewOpenAIVisionClient(agentmedia.VisionConfig{
		URL:     cfg.GoVLLMURL,
		APIKey:  cfg.GoVLLMAPIKey,
		Model:   cfg.GoVLLMModel,
		Timeout: cfg.GoVLLMTimeout,
	})
}

// ---------------------------------------------------------------------------
// Conversation memory backed by Redis
// ---------------------------------------------------------------------------

type redisMemory struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

type memoryReader interface {
	Enabled() bool
	Prefix() string
	List(ctx context.Context, limit int) ([]memoryKeyInfo, error)
	LoadRaw(ctx context.Context, deviceID string) (memoryValue, error)
}

type memoryKeyInfo struct {
	Key        string `json:"key"`
	DeviceID   string `json:"device_id"`
	TTLSeconds int64  `json:"ttl_seconds"`
	Bytes      int    `json:"bytes"`
}

type memoryValue struct {
	Key        string `json:"key"`
	DeviceID   string `json:"device_id"`
	TTLSeconds int64  `json:"ttl_seconds"`
	Bytes      int    `json:"bytes"`
	Raw        []byte `json:"-"`
}

func newRedisMemory(cfg Config) *redisMemory {
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
	return &redisMemory{client: client, prefix: cfg.RedisKeyPrefix, ttl: cfg.MemoryTTL}
}

func (m *redisMemory) Enabled() bool {
	return m != nil && m.client != nil
}

func (m *redisMemory) Prefix() string {
	if m == nil {
		return ""
	}
	return m.prefix
}

func (m *redisMemory) List(ctx context.Context, limit int) ([]memoryKeyInfo, error) {
	if !m.Enabled() {
		return nil, errors.New("redis memory is not configured")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	pattern := m.prefix + "*"
	var cursor uint64
	items := make([]memoryKeyInfo, 0)
	for {
		keys, next, err := m.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			info := memoryKeyInfo{
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

func (m *redisMemory) LoadRaw(ctx context.Context, deviceID string) (memoryValue, error) {
	if !m.Enabled() {
		return memoryValue{}, errors.New("redis memory is not configured")
	}
	key := m.prefix + deviceID
	data, err := m.client.Get(ctx, key).Bytes()
	if err != nil {
		return memoryValue{}, err
	}
	value := memoryValue{
		Key:      key,
		DeviceID: deviceID,
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

const maxHistoryMessages = 40

func (m *redisMemory) Load(ctx context.Context, deviceID string) []*schema.Message {
	if m == nil {
		return nil
	}
	data, err := m.client.Get(ctx, m.prefix+deviceID).Bytes()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		logger.Infof("redis load memory for %s: %v", deviceID, err)
		return nil
	}
	var msgs []*schema.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		logger.Infof("redis unmarshal memory for %s: %v", deviceID, err)
		return nil
	}
	return msgs
}

func (m *redisMemory) Save(ctx context.Context, deviceID string, msgs []*schema.Message) {
	if m == nil {
		return
	}
	// Keep only last N messages to stay within budget
	if len(msgs) > maxHistoryMessages {
		msgs = msgs[len(msgs)-maxHistoryMessages:]
	}
	data, err := json.Marshal(msgs)
	if err != nil {
		logger.Infof("redis marshal memory for %s: %v", deviceID, err)
		return
	}
	if err := m.client.Set(ctx, m.prefix+deviceID, data, m.ttl).Err(); err != nil {
		logger.Infof("redis save memory for %s: %v", deviceID, err)
	}
}

// ---------------------------------------------------------------------------
// EinoAgent — Eino-powered LLM with memory and skill support
// ---------------------------------------------------------------------------

type EinoAgent struct {
	chatModel   *openai.ChatModel
	memory      *redisMemory
	cfg         Config
	hub         *DeviceHub
	extMCPs     []*agentmcp.Client
	extToolSets [][]tool.BaseTool
	skillMW     adk.ChatModelAgentMiddleware
}

func newEinoAgent(cfg Config) *EinoAgent {
	if cfg.GoLLMAPIKey == "" {
		return nil
	}
	baseURL := strings.TrimSuffix(cfg.GoLLMURL, "/chat/completions")
	baseURL = strings.TrimRight(baseURL, "/")

	ctx := context.Background()
	temp := float32(0.2)
	maxTokens := 180
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:     baseURL,
		APIKey:      cfg.GoLLMAPIKey,
		Model:       cfg.GoLLMModel,
		Timeout:     cfg.GoLLMTimeout,
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	})
	if err != nil {
		logger.Infof("eino chat model init failed: %v", err)
		return nil
	}

	memory := newRedisMemory(cfg)

	// Connect to external MCP servers and discover their tools
	var extMCPs []*agentmcp.Client
	var extToolSets [][]tool.BaseTool
	for _, mcpURL := range cfg.ExternalMCPURLs {
		mcpURL = strings.TrimSpace(mcpURL)
		if mcpURL == "" {
			continue
		}
		client, err := agentmcp.NewClient(ctx, mcpURL)
		if err != nil {
			logger.Infof("ext MCP connect failed %s: %v", mcpURL, err)
			continue
		}
		tools, err := client.ListTools(ctx)
		if err != nil {
			logger.Infof("ext MCP list tools failed %s: %v", mcpURL, err)
			continue
		}
		extMCPs = append(extMCPs, client)
		extToolSets = append(extToolSets, tools)
		logger.Infof("ext MCP ready: %s tools=%d", mcpURL, len(tools))
	}

	var skillMW adk.ChatModelAgentMiddleware
	if len(cfg.SkillRoots) > 0 {
		backend, err := agentskill.NewFileBackend(agentskill.BackendConfig{
			Roots:    cfg.SkillRoots,
			Enabled:  cfg.EnabledSkills,
			MaxBytes: cfg.SkillMaxBytes,
		})
		if err != nil {
			logger.Infof("skill backend init failed: %v", err)
		} else if backend.Count() > 0 {
			buildSkillContent := agentskill.NewContentBuilder(agentskill.ExecConfig{
				Timeout:        cfg.SkillExecTimeout,
				MaxOutputBytes: cfg.SkillExecMaxOutputBytes,
				GlobalBinDirs:  cfg.SkillExecGlobalBinDirs,
			})
			mw, err := einoskill.NewMiddleware(ctx, &einoskill.Config{
				Backend:               backend,
				UseChinese:            true,
				CustomToolDescription: agentskill.BuildToolDescription,
				CustomToolParams:      agentskill.BuildToolParams,
				BuildContent:          buildSkillContent,
			})
			if err != nil {
				logger.Infof("skill middleware init failed: %v", err)
			} else {
				skillMW = mw
				logger.Infof("skill backend ready: roots=%v skills=%d exec_bins=%v", cfg.SkillRoots, backend.Count(), cfg.SkillExecGlobalBinDirs)
			}
		} else {
			logger.Infof("skill backend empty: roots=%v", cfg.SkillRoots)
		}
	}

	logger.Infof("eino agent ready: model=%s base=%s redis=%v extMCPs=%d skills=%v", cfg.GoLLMModel, baseURL, memory != nil, len(extMCPs), skillMW != nil)
	return &EinoAgent{chatModel: chatModel, memory: memory, cfg: cfg, extMCPs: extMCPs, extToolSets: extToolSets, skillMW: skillMW}
}

func (a *EinoAgent) SetHub(hub *DeviceHub) {
	a.hub = hub
}

// Chat sends userText through the Eino agent with memory and optional MCP tools.
func (a *EinoAgent) Chat(ctx context.Context, deviceID string, userText string) (string, error) {
	return a.ChatWithContext(ctx, deviceID, deviceID, userText)
}

// ChatWithContext separates the conversation memory key from the optional
// device ID used for MCP tools. Device voice turns use the same value for both;
// text-only channels such as Lark use their channel conversation ID and leave
// deviceID empty unless they intentionally bind to a device.
func (a *EinoAgent) ChatWithContext(ctx context.Context, conversationID string, deviceID string, userText string) (string, error) {
	if conversationID == "" {
		conversationID = deviceID
	}
	logger.Infof("EinoAgent.Chat called: conversation=%s device=%s text=%q", conversationID, deviceID, userText)

	// Load conversation history
	history := a.memory.Load(ctx, conversationID)

	// Build message list: system + history + new user message
	msgs := make([]*schema.Message, 0, len(history)+2)
	if a.cfg.GoLLMPrompt != "" {
		msgs = append(msgs, schema.SystemMessage(a.cfg.GoLLMPrompt))
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, schema.UserMessage(userText))

	// Build tool list: device MCP tools + external MCP tools
	var einoTools []tool.BaseTool
	if a.hub != nil && deviceID != "" {
		if rawTools, ok := a.hub.ToolSnapshot(deviceID); ok {
			einoTools = agentmcp.NewDeviceTools(deviceID, rawTools, a.hub)
		}
	}
	for _, tools := range a.extToolSets {
		einoTools = append(einoTools, tools...)
	}

	// Create agent with tools
	agentCfg := &adk.ChatModelAgentConfig{
		Name:          "xiaoli",
		Instruction:   "", // already prepended as system message
		Model:         a.chatModel,
		MaxIterations: 10,
	}
	if a.skillMW != nil {
		agentCfg.Handlers = []adk.ChatModelAgentMiddleware{a.skillMW}
	}
	if len(einoTools) > 0 {
		agentCfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: einoTools,
			},
		}
	}

	agent, err := adk.NewChatModelAgent(ctx, agentCfg)
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent: agent,
	})

	iter := runner.Run(ctx, msgs)

	var result *schema.Message
	eventCount := 0
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		eventCount++
		if event.Err != nil {
			logger.Infof("EinoAgent.Chat event error: %v", event.Err)
			return "", fmt.Errorf("agent error: %w", event.Err)
		}
		logger.Infof("EinoAgent.Chat event[%d]: output=%v", eventCount, event.Output != nil)
		if event.Output != nil && event.Output.MessageOutput != nil &&
			event.Output.MessageOutput.Message != nil &&
			event.Output.MessageOutput.Role == schema.Assistant {
			result = event.Output.MessageOutput.Message
			logger.Infof("EinoAgent.Chat assistant: %q", result.Content)
		}
	}
	logger.Infof("EinoAgent.Chat done: events=%d hasResult=%v", eventCount, result != nil)
	if result == nil || result.Content == "" {
		return "", fmt.Errorf("agent returned empty response")
	}

	// Update conversation history
	updated := append(history,
		schema.UserMessage(userText),
		result,
	)
	a.memory.Save(ctx, conversationID, updated)

	return result.Content, nil
}

// Generate sends a system + user message to the LLM with external MCP tools but no history.
func (a *EinoAgent) Generate(ctx context.Context, system, user string) (string, error) {
	msgs := make([]*schema.Message, 0, 2)
	if system != "" {
		msgs = append(msgs, schema.SystemMessage(system))
	}
	msgs = append(msgs, schema.UserMessage(user))

	var einoTools []tool.BaseTool
	for _, tools := range a.extToolSets {
		einoTools = append(einoTools, tools...)
	}

	cfg := &adk.ChatModelAgentConfig{
		Name:          "xiaoli",
		Model:         a.chatModel,
		MaxIterations: 10,
	}
	if a.skillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.skillMW}
	}
	if len(einoTools) > 0 {
		cfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: einoTools,
			},
		}
	}

	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Run(ctx, msgs)

	var result string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			logger.Infof("EinoAgent.Generate event error: %v", event.Err)
			return "", fmt.Errorf("agent error: %w", event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil &&
			event.Output.MessageOutput.Message != nil &&
			event.Output.MessageOutput.Role == schema.Assistant {
			result = event.Output.MessageOutput.Message.Content
		}
	}
	if result == "" {
		return "", fmt.Errorf("agent returned empty response")
	}
	return result, nil
}
