package runtime

import (
	"time"

	agentworkflow "github.com/mnhkahn/xiaoli/internal/agent/workflow"
)

type BashConfig struct {
	Enabled        bool
	Timeout        time.Duration
	MaxOutputBytes int64
	PolicyPath     string
}

// MCP 认证方式
const (
	MCPAuthNone   = "none"   // 无认证
	MCPAuthQuery  = "query"  // URL 拼 ?key=xxx（默认，兼容旧配置）
	MCPAuthBearer = "bearer" // Authorization: Bearer <key>
	MCPAuthHeader = "header" // 自定义请求头携带 key
	MCPAuthOAuth  = "oauth"  // OAuth2 client_credentials/refresh_token，自动取并刷新 access token
)

type MCPEndpoint struct {
	Name    string // MCP 服务名（settings.json 中的 name），用于 A2A allowlist
	URL     string
	APIKey  string
	Auth    string // 见 MCPAuth* 常量，空视为 query（兼容旧逻辑）
	HeaderN string // Auth=header 时的请求头名，如 X-API-Key
	// Timeout 是单次 MCP 调用（含同服务队列等待）的上限；零值使用客户端默认值。
	Timeout time.Duration

	// OAuth2（Auth=oauth）
	TokenURL     string
	ClientID     string
	ClientSecret string
	RefreshToken string
	Scope        string
}

type Config struct {
	LLMURL                  string
	LLMAPIKey               string
	LLMModel                string
	LLMModels               []string
	LLMModelConfigs         map[string]LLMModelConfig
	LLMPrompt               string
	LLMTimeout              time.Duration
	VLLMModel               string
	ASRModel                string
	TTSModel                string
	RedisURL                string
	RedisKeyPrefix          string
	MemoryTTL               time.Duration
	StorageBackend          string
	LocalDataDir            string
	LocalMemoryFile         string
	LocalConversationDir    string
	LocalHistoryMaxMessages int
	ExternalMCPEndpoints    []MCPEndpoint
	BuiltinWebFetchEnabled  bool
	SkillRoots              []string
	EnabledSkills           []string
	SkillMaxBytes           int64
	SkillExecTimeout        time.Duration
	SkillExecMaxOutputBytes int64
	SkillExecGlobalBinDirs  []string
	TaskAllowedRoots        []string
	BashConfig              BashConfig
	AgentFileRoots          []string
	ReminderStore           *agentworkflow.ReminderStore
	LogDir                  string
	Timezone                string
	A2AAllowedSkills        []string // A2A 通道允许的 skill 白名单
	A2ATraceEnabled         bool
	A2ATraceLogInputs       bool
	A2ATraceLogOutputs      bool
	A2ATraceMaxValueLength  int
}

type LLMModelConfig struct {
	ID            string
	DisplayName   string
	BaseURL       string
	Model         string
	APIKey        string
	MaxTokens     int
	ContextLength int
}

func (c Config) selectedLLMModelConfig() LLMModelConfig {
	return c.selectedLLMModelConfigFor(c.LLMModel)
}

func (c Config) selectedLLMModelConfigFor(id string) LLMModelConfig {
	if c.LLMModelConfigs != nil {
		if model, ok := c.LLMModelConfigs[id]; ok {
			if model.ID == "" {
				model.ID = id
			}
			return model
		}
	}
	return LLMModelConfig{
		ID:      id,
		BaseURL: c.LLMURL,
		Model:   id,
		APIKey:  c.LLMAPIKey,
	}
}
