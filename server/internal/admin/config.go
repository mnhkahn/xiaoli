package admin

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/mnhkahn/gogogo/logger"
	"github.com/mnhkahn/xiaoli/internal/agent/modelcatalog"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
	agentskill "github.com/mnhkahn/xiaoli/internal/agent/tool/skill"
	agentworkflow "github.com/mnhkahn/xiaoli/internal/agent/workflow"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	fallbackLLMPrompt               = "你是一个叫小李的中文语音助手。回答要简短、自然、适合通过扬声器播放。"
	defaultGoLLMTimeoutSeconds      = 240
	defaultAgentRunTimeoutSeconds   = 600
	defaultA2ARequestTimeoutSeconds = 600
	defaultReminderChannel          = "lark"
)

type Config struct {
	Host                    string
	Port                    int
	PublicBaseURL           string
	SessionSecret           string
	SessionMaxAge           time.Duration
	LogtoEndpoint           string
	LogtoAppID              string
	LogtoAppSecret          string
	AllowedUsers            []string
	DirectDeviceServer      bool
	DeviceAuthEnabled       bool
	DeviceAuthKey           string
	AllowedDeviceIDs        []string
	BridgeBaseURL           string
	VisionProxyBaseURL      string
	InternalStreamToken     string
	MCPReadyWait            time.Duration
	GoASRURL                string
	GoASRAPIKey             string
	GoASRModel              string
	GoASRTimeout            time.Duration
	GoLLMURL                string
	GoLLMAPIKey             string
	GoLLMModel              string
	GoLLMModels             []string
	GoLLMModelConfigs       map[string]LLMModelConfig
	GoLLMPrompt             string
	GoLLMTimeout            time.Duration
	GoVLLMURL               string
	GoVLLMAPIKey            string
	GoVLLMModel             string
	GoVLLMTimeout           time.Duration
	GoTTSURL                string
	GoTTSAPIKey             string
	GoTTSModel              string
	GoTTSVoice              string
	GoTTSResponseFormat     string
	GoTTSTimeout            time.Duration
	ExternalMCPEndpoints    []agentruntime.MCPEndpoint
	MCPConfigPath           string
	BuiltinWebFetchEnabled  bool
	SkillRoots              []string
	EnabledSkills           []string
	SkillMaxBytes           int64
	SkillExecTimeout        time.Duration
	SkillExecMaxOutputBytes int64
	SkillExecGlobalBinDirs  []string
	TaskAllowedRoots        []string
	BashEnabled             bool
	BashTimeout             time.Duration
	BashMaxOutputBytes      int64
	AgentFileRoots          []string
	AgentsDir               string
	ChatReact               agentworkflow.AgentSpec
	Workflows               []agentworkflow.Definition
	ReminderDefaultChannel  string
	LarkWebhookURL          string
	LarkAppID               string
	LarkAppToken            string
	WeChatEnabled           bool
	WeChatBotToken          string
	WeChatBaseURL           string
	RedisURL                string
	RedisKeyPrefix          string
	MemoryTTL               time.Duration
	StorageBackend          string
	LocalDataDir            string
	LocalMemoryFile         string
	LocalConversationDir    string
	LocalHistoryMaxMessages int
	DataDir                 string
	LogDir                  string
	Timezone                string
	Now                     func() time.Time

	// A2A configuration
	A2A A2AConfig

	// Email configuration
	EmailSMTPHost    string
	EmailSMTPPort    int
	EmailIMAPHost    string
	EmailIMAPPort    int
	EmailUsername    string
	EmailPassword    string
	EmailUseSSL      bool
	EmailFromName    string
	EmailFromAddress string
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

// A2AConfig holds A2A (Agent-to-Agent) protocol configuration
type A2AConfig struct {
	Enabled             bool
	PublicAgentCard     bool
	APIKeys             map[string]string // key_id -> secret
	RateLimitPerKey     int
	RateLimitGlobal     int
	MaxConcurrentPerKey int
	MaxInputChars       int
	TimeoutSeconds      int
	TaskTTLSeconds      int
	AllowedSkills       []string
	PublicBaseURL       string
	Trace               A2ATraceConfig
	Profiles            map[string]A2AProfileConfig // profile 配置，可覆盖代码默认值
}

// A2AProfileConfig holds per-profile overrides (model, system prompt, etc.)
type A2AProfileConfig struct {
	Model      string `json:"model"`
	AllowTools *bool  `json:"allow_tools,omitempty"`
}

type A2ATraceConfig struct {
	Enabled        bool
	LogInputs      bool
	LogOutputs     bool
	MaxValueLength int
}

var validKeyIDChars = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func LoadConfig() Config {
	sessionSecret := env("ADMIN_SESSION_SECRET", "")
	settings, settingsPath := loadSettings(defaultSettingsPaths())
	goLLMModel := strings.TrimSpace(settings.Models.LLM.Default)
	goLLMModels := settings.Models.LLM.modelIDs()
	goLLMModelConfigs := map[string]LLMModelConfig{}
	for id, option := range settings.Models.LLM.Options {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		goLLMModelConfigs[id] = LLMModelConfig{
			ID:            id,
			DisplayName:   strings.TrimSpace(option.Name),
			BaseURL:       strings.TrimSpace(option.BaseURL),
			Model:         strings.TrimSpace(option.Model),
			APIKey:        settingsAPIKey(option.APIKeyEnv),
			MaxTokens:     option.MaxTokens,
			ContextLength: option.ContextLength,
		}
	}
	dataDir := env("XIAOLI_DATA_DIR", "/data")
	if catalog, err := modelcatalog.Load(context.Background(), settings.ModelCatalog.catalogConfig(), filepath.Join(dataDir, "model_catalog.json")); err != nil {
		logger.Infof("model catalog unavailable: %v", err)
	} else {
		for _, entry := range modelcatalog.Entries(catalog, settings.ModelCatalog.catalogConfig().Providers) {
			if _, exists := goLLMModelConfigs[entry.ID]; exists {
				continue // Explicit static options remain authoritative.
			}
			goLLMModelConfigs[entry.ID] = LLMModelConfig{ID: entry.ID, DisplayName: entry.DisplayName, BaseURL: entry.BaseURL, Model: entry.Model, APIKey: settingsAPIKey(entry.APIKeyEnv), MaxTokens: entry.MaxTokens, ContextLength: entry.ContextLength}
			goLLMModels = append(goLLMModels, entry.ID)
		}
	}
	if settings.ModelRanking.Enabled {
		if ranked, err := fetchTopOpenRouterFreeModel(context.Background(), settings.ModelRanking.URL, settings.ModelRanking.timeout()); err != nil {
			logger.Infof("model ranking unavailable; using configured default %s: %v", goLLMModel, err)
		} else if provider, ok := settings.ModelCatalog.Providers["openrouter"]; ok && strings.TrimSpace(provider.BaseURL) != "" {
			id := rankedOpenRouterModelID(goLLMModelConfigs, ranked.Model)
			if _, exists := goLLMModelConfigs[id]; !exists {
				goLLMModelConfigs[id] = LLMModelConfig{
					ID:            id,
					DisplayName:   "OpenRouter " + ranked.Model,
					BaseURL:       provider.BaseURL,
					Model:         ranked.Model,
					APIKey:        settingsAPIKey(provider.APIKeyEnv),
					MaxTokens:     provider.MaxTokens,
					ContextLength: ranked.ContextWindow,
				}
				goLLMModels = append(goLLMModels, id)
			}
			goLLMModel = id
			logger.Infof("selected top ranked OpenRouter free model: %s (code_rank=%d code_elo=%.0f text_rank=%d text_elo=%.0f)", ranked.Model, ranked.CodeRank, ranked.CodeELO, ranked.TextRank, ranked.TextELO)
		} else {
			logger.Infof("model ranking unavailable; OpenRouter provider is not configured")
		}
	}
	if goLLMModel == "" && len(goLLMModels) > 0 {
		goLLMModel = goLLMModels[0]
	}

	refreshModelLimitsOnce(goLLMModelConfigs)
	selectedLLM := goLLMModelConfigs[goLLMModel]
	if selectedLLM.Model == "" {
		selectedLLM.Model = goLLMModel
	}
	goLLMPrompt := loadAgentPrompt(defaultAgentPromptRoots())
	if goLLMPrompt == "" {
		goLLMPrompt = fallbackLLMPrompt
	}
	vision := settings.Models.Vision
	asr := settings.Models.ASR
	tts := settings.Models.TTS
	cfg := Config{
		Host:                    env("XIAOLI_ADMIN_HOST", "0.0.0.0"),
		Port:                    envInt("XIAOLI_ADMIN_PORT", 8004),
		PublicBaseURL:           strings.TrimRight(env("ADMIN_PUBLIC_BASE_URL", env("PUBLIC_BASE_URL", "https://xiaoli-server.fly.dev")), "/"),
		SessionSecret:           sessionSecret,
		SessionMaxAge:           time.Duration(envInt("ADMIN_SESSION_MAX_AGE_SECONDS", 604800)) * time.Second,
		LogtoEndpoint:           strings.TrimRight(env("LOGTO_ENDPOINT", ""), "/") + "/",
		LogtoAppID:              env("LOGTO_APP_ID", ""),
		LogtoAppSecret:          env("LOGTO_APP_SECRET", ""),
		AllowedUsers:            csv(env("ADMIN_ALLOWED_USERS", "")),
		DirectDeviceServer:      envBool("XIAOLI_DIRECT_DEVICE_SERVER", false),
		DeviceAuthEnabled:       envBool("ENABLE_SERVER_AUTH", false),
		DeviceAuthKey:           env("SERVER_AUTH_KEY", ""),
		AllowedDeviceIDs:        csv(firstNonEmptyEnv("ALLOWED_DEVICE_IDS", "ALLOWED_DEVICE_ID", "SERVER_AUTH_ALLOWED_DEVICE_IDS")),
		BridgeBaseURL:           strings.TrimRight(env("XIAOLI_BRIDGE_BASE_URL", "http://127.0.0.1:8005"), "/"),
		VisionProxyBaseURL:      strings.TrimRight(env("XIAOLI_VISION_PROXY_BASE_URL", "http://127.0.0.1:8003"), "/"),
		InternalStreamToken:     env("XIAOLI_ADMIN_INTERNAL_TOKEN", sessionSecret),
		MCPReadyWait:            time.Duration(envFloat("ADMIN_MCP_READY_WAIT_SECONDS", 5)) * time.Second,
		GoASRURL:                strings.TrimSpace(asr.BaseURL),
		GoASRAPIKey:             settingsAPIKey(asr.APIKeyEnv),
		GoASRModel:              strings.TrimSpace(asr.Model),
		GoASRTimeout:            time.Duration(envInt("XIAOLI_GO_ASR_TIMEOUT_SECONDS", 45)) * time.Second,
		GoLLMURL:                selectedLLM.BaseURL,
		GoLLMAPIKey:             selectedLLM.APIKey,
		GoLLMModel:              goLLMModel,
		GoLLMModels:             goLLMModels,
		GoLLMModelConfigs:       goLLMModelConfigs,
		GoLLMPrompt:             goLLMPrompt,
		GoLLMTimeout:            time.Duration(envInt("XIAOLI_GO_LLM_TIMEOUT_SECONDS", defaultGoLLMTimeoutSeconds)) * time.Second,
		GoVLLMURL:               strings.TrimSpace(vision.BaseURL),
		GoVLLMAPIKey:            settingsAPIKey(vision.APIKeyEnv),
		GoVLLMModel:             strings.TrimSpace(vision.Model),
		GoVLLMTimeout:           time.Duration(envInt("XIAOLI_GO_VLLM_TIMEOUT_SECONDS", 60)) * time.Second,
		GoTTSURL:                strings.TrimSpace(tts.BaseURL),
		GoTTSAPIKey:             settingsAPIKey(tts.APIKeyEnv),
		GoTTSModel:              strings.TrimSpace(tts.Model),
		GoTTSVoice:              strings.TrimSpace(tts.Voice),
		GoTTSResponseFormat:     strings.TrimSpace(tts.ResponseFormat),
		GoTTSTimeout:            time.Duration(envInt("XIAOLI_GO_TTS_TIMEOUT_SECONDS", 30)) * time.Second,
		MCPConfigPath:           settingsPath,
		ExternalMCPEndpoints:    settings.mcpEndpoints(),
		BuiltinWebFetchEnabled:  settings.webFetchEnabled(),
		SkillRoots:              csv(env("XIAOLI_SKILL_ROOTS", "/opt/xiaoli/skills")),
		EnabledSkills:           csv(env("XIAOLI_ENABLED_SKILLS", "*")),
		SkillMaxBytes:           int64(envInt("XIAOLI_SKILL_MAX_BYTES", int(agentskill.DefaultMaxBytes))),
		SkillExecTimeout:        capDuration(time.Duration(envInt("XIAOLI_SKILL_EXEC_TIMEOUT_SECONDS", int(agentskill.DefaultExecTimeout/time.Second)))*time.Second, agentskill.MaxExecTimeout),
		SkillExecMaxOutputBytes: int64(envInt("XIAOLI_SKILL_EXEC_MAX_OUTPUT_BYTES", agentskill.DefaultExecMaxOutputBytes)),
		SkillExecGlobalBinDirs:  csv(env("XIAOLI_SKILL_EXEC_GLOBAL_BIN_DIRS", "/usr/local/bin")),
		TaskAllowedRoots:        csv(env("XIAOLI_TASK_ALLOWED_ROOTS", "")),
		BashEnabled:             settings.bashEnabled(),
		BashTimeout:             settings.bashTimeout(),
		BashMaxOutputBytes:      int64(settings.bashMaxOutputBytes()),
		AgentFileRoots:          agentbuiltin.FileAgentRoots(),
		AgentsDir:               strings.TrimSpace(env("XIAOLI_AGENTS_DIR", "")),
		ChatReact:               parseChatReact(settings.ChatReact),
		Workflows:               parseWorkflows(settings.Workflows),
		ReminderDefaultChannel:  settings.reminderDefaultChannel(),
		LarkWebhookURL:          env("LARK_BOT_WEBHOOK_URL", ""),
		LarkAppID:               env("LARK_APP_ID", ""),
		LarkAppToken:            env("LARK_APP_TOKEN", ""),
		WeChatEnabled:           envBool("WECHAT_ENABLED", false),
		WeChatBotToken:          env("WECHAT_BOT_TOKEN", ""),
		WeChatBaseURL:           env("WECHAT_BASE_URL", wechatDefaultBaseURL),
		RedisURL:                env("XIAOLI_REDIS_URL", ""),
		RedisKeyPrefix:          env("XIAOLI_REDIS_KEY_PREFIX", "xiaoli:cp:"),
		MemoryTTL:               time.Duration(envInt("XIAOLI_MEMORY_TTL_HOURS", 24)) * time.Hour,
		DataDir:                 dataDir,
		Timezone:                env("TZ", "Asia/Shanghai"),

		// Email configuration (Gmail defaults)
		EmailSMTPHost:    env("EMAIL_SMTP_HOST", "smtp.gmail.com"),
		EmailSMTPPort:    envInt("EMAIL_SMTP_PORT", 465),
		EmailIMAPHost:    env("EMAIL_IMAP_HOST", "imap.gmail.com"),
		EmailIMAPPort:    envInt("EMAIL_IMAP_PORT", 993),
		EmailUsername:    env("EMAIL_USERNAME", ""),
		EmailPassword:    env("EMAIL_PASSWORD", ""),
		EmailUseSSL:      envBool("EMAIL_USE_SSL", true),
		EmailFromName:    env("EMAIL_FROM_NAME", ""),
		EmailFromAddress: env("EMAIL_FROM_ADDRESS", ""),
	}
	rawStorage := settings.Storage
	storage := settings.storageConfig()
	cfg.StorageBackend = storage.Backend
	if cfg.StorageBackend == "" && cfg.RedisURL != "" {
		cfg.StorageBackend = "redis"
	}
	cfg.LocalDataDir = storage.Local.DataDir
	cfg.LocalMemoryFile = storage.Local.MemoryFile
	cfg.LocalConversationDir = storage.Local.ConversationDir
	cfg.LocalHistoryMaxMessages = storage.Local.HistoryMaxMessages
	if rawStorage.Redis.URLEnv != "" {
		cfg.RedisURL = env(storage.Redis.URLEnv, cfg.RedisURL)
	}
	if rawStorage.Redis.KeyPrefix != "" {
		cfg.RedisKeyPrefix = storage.Redis.KeyPrefix
	}
	if rawStorage.Redis.MemoryTTLHours > 0 {
		cfg.MemoryTTL = time.Duration(storage.Redis.MemoryTTLHours) * time.Hour
	}
	cfg.A2A = A2AConfig{
		Enabled:             settings.a2aEnabled(),
		PublicAgentCard:     settings.a2aPublicAgentCard(),
		APIKeys:             settings.a2aAPIKeys(),
		RateLimitPerKey:     settings.a2aRateLimitPerKey(),
		RateLimitGlobal:     settings.a2aRateLimitGlobal(),
		MaxConcurrentPerKey: settings.a2aMaxConcurrentPerKey(),
		MaxInputChars:       settings.a2aMaxInputChars(),
		TimeoutSeconds:      settings.a2aTimeoutSeconds(),
		TaskTTLSeconds:      settings.a2aTaskTTLSeconds(),
		AllowedSkills:       settings.a2aAllowedSkills(),
		PublicBaseURL:       strings.TrimRight(env("PUBLIC_BASE_URL", ""), "/"),
		Profiles:            settings.A2A.Profiles,
		Trace: A2ATraceConfig{
			Enabled:        settings.a2aTraceEnabled(),
			LogInputs:      settings.a2aTraceLogInputs(),
			LogOutputs:     settings.a2aTraceLogOutputs(),
			MaxValueLength: settings.a2aTraceMaxValueLength(),
		},
	}
	cfg.LogDir = filepath.Join(cfg.DataDir, "logs")
	return cfg
}

func defaultSettingsPaths() []string {
	return []string{
		"settings.json",
		"/opt/xiaoli/settings.json",
	}
}

type settingsConfig struct {
	Models       settingsModels                 `json:"models"`
	ModelCatalog settingsModelCatalog           `json:"model_catalog"`
	ModelRanking settingsModelRanking           `json:"model_ranking"`
	MCPServers   []settingsMCPServer            `json:"mcp_servers"`
	Tools        settingsTools                  `json:"tools"`
	Storage      settingsStorage                `json:"storage"`
	Reminder     settingsReminder               `json:"reminder"`
	ChatReact    settingsWorkflowAgent          `json:"chat_react"`
	Workflows    map[string]settingsWorkflowDef `json:"cron"`
	A2A          settingsA2AConfig              `json:"a2a"`
}

type settingsReminder struct {
	DefaultChannel string `json:"default_channel"`
}

func normalizeReminderDeliveryChannel(channel, fallback string) string {
	switch strings.TrimSpace(strings.ToLower(channel)) {
	case "lark", "lark_text":
		return "lark"
	case "wechat", "wechat_text":
		return "wechat"
	case "esp32", "device", "device_voice":
		return "esp32"
	case "":
		return fallback
	default:
		return fallback
	}
}

type settingsStorage struct {
	Backend string               `json:"backend"`
	Redis   settingsStorageRedis `json:"redis"`
	Local   settingsStorageLocal `json:"local"`
}

type settingsStorageRedis struct {
	URLEnv         string `json:"url_env"`
	KeyPrefix      string `json:"key_prefix"`
	MemoryTTLHours int    `json:"memory_ttl_hours"`
}

type settingsStorageLocal struct {
	DataDir            string `json:"data_dir"`
	MemoryFile         string `json:"memory_file"`
	ConversationDir    string `json:"conversation_dir"`
	HistoryMaxMessages int    `json:"history_max_messages"`
}

type settingsTools struct {
	WebFetch settingsToolSwitch `json:"webfetch"`
	Bash     *settingsBash      `json:"bash"`
}

type settingsToolSwitch struct {
	Enabled *bool `json:"enabled"`
}

type settingsBash struct {
	Enabled        *bool `json:"enabled"`
	TimeoutSeconds *int  `json:"timeout_seconds"`
	MaxOutputKB    *int  `json:"max_output_kb"`
}

type settingsA2AKey struct {
	ID        string `json:"id"`
	SecretEnv string `json:"secret_env"`
}

type settingsA2AConfig struct {
	Enabled         *bool                       `json:"enabled"`
	PublicAgentCard *bool                       `json:"public_agent_card"`
	Keys            []settingsA2AKey            `json:"keys"`
	MaxInputChars   *int                        `json:"max_input_chars"`
	TimeoutSeconds  *int                        `json:"timeout_seconds"`
	RateLimitPerMin *int                        `json:"rate_limit_per_minute"`
	RateLimitGlobal *int                        `json:"rate_limit_global_per_minute"`
	MaxConcurrent   *int                        `json:"max_concurrent"`
	TaskTTLSeconds  *int                        `json:"task_ttl_seconds"`
	AllowedSkills   []string                    `json:"allowed_skills"`
	Trace           settingsA2ATrace            `json:"trace"`
	Profiles        map[string]A2AProfileConfig `json:"profiles"`
}

type settingsA2ATrace struct {
	Enabled        *bool `json:"enabled"`
	LogInputs      *bool `json:"log_inputs"`
	LogOutputs     *bool `json:"log_outputs"`
	MaxValueLength *int  `json:"max_value_length"`
}

type settingsModels struct {
	LLM    settingsLLMModel      `json:"llm"`
	Vision settingsModelEndpoint `json:"vision"`
	ASR    settingsModelEndpoint `json:"asr"`
	TTS    settingsModelEndpoint `json:"tts"`
}

type settingsLLMModel struct {
	Default string                           `json:"default"`
	Options map[string]settingsModelEndpoint `json:"options"`
}

type settingsModelEndpoint struct {
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	APIKeyEnv      string `json:"api_key_env"`
	MaxTokens      int    `json:"max_tokens"`
	ContextLength  int    `json:"context_length"`
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format"`
}

type settingsModelCatalog struct {
	Enabled         bool                             `json:"enabled"`
	URL             string                           `json:"url"`
	RefreshInterval string                           `json:"refresh_interval"`
	Timeout         string                           `json:"timeout"`
	Providers       map[string]modelcatalog.Provider `json:"providers"`
}

func (s settingsModelCatalog) catalogConfig() modelcatalog.Config {
	refresh, _ := time.ParseDuration(s.RefreshInterval)
	timeout, _ := time.ParseDuration(s.Timeout)
	return modelcatalog.Config{Enabled: s.Enabled, URL: s.URL, RefreshInterval: refresh, Timeout: timeout, Providers: s.Providers}
}

type settingsModelRanking struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Timeout string `json:"timeout"`
}

func (s settingsModelRanking) timeout() time.Duration {
	timeout, err := time.ParseDuration(s.Timeout)
	if err != nil || timeout <= 0 {
		return 5 * time.Second
	}
	return timeout
}

func rankedOpenRouterModelID(configs map[string]LLMModelConfig, model string) string {
	model = strings.TrimSpace(model)
	for id, config := range configs {
		if strings.EqualFold(strings.TrimSpace(config.Model), model) {
			return id
		}
	}
	return "openrouter:" + model
}

type settingsMCPServer struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	URLEnv    string `json:"url_env,omitempty"` // 从环境变量读取 URL（含敏感 token 时用，优先于 url）
	APIKeyEnv string `json:"api_key_env"`
	// 认证方式：none/query/bearer/header/oauth，留空兼容旧逻辑（有 key 走 query）
	AuthType   string `json:"auth_type,omitempty"`
	HeaderName string `json:"header_name,omitempty"`
	// OAuth2（auth_type=oauth）
	TokenURL        string `json:"token_url,omitempty"`
	ClientIDEnv     string `json:"client_id_env,omitempty"`
	ClientSecretEnv string `json:"client_secret_env,omitempty"`
	RefreshTokenEnv string `json:"refresh_token_env,omitempty"`
	Scope           string `json:"scope,omitempty"`
	// Timeout 是一次 MCP 调用（包含同服务的排队等待）的上限。
	Timeout string `json:"timeout,omitempty"`

	// stdio 模式（如 docker run）
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func loadSettings(paths []string) (settingsConfig, string) {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg settingsConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			logger.Infof("settings: skip %s (parse error: %v)", path, err)
			continue
		}
		return cfg, path
	}
	return settingsConfig{}, ""
}

func parseWorkflows(raw map[string]settingsWorkflowDef) []agentworkflow.Definition {
	if raw == nil {
		return nil
	}
	var defs []agentworkflow.Definition
	seen := map[string]bool{}
	for id, wf := range raw {
		id = strings.TrimSpace(id)
		if id == "" {
			logger.Infof("[workflows] skip entry with empty key")
			continue
		}
		if seen[id] {
			logger.Infof("[workflows] duplicate key %q, keeping first", id)
			continue
		}
		seen[id] = true

		if !wf.Trigger.valid() {
			logger.Infof("[workflows] %q: invalid trigger (both every and at_hour/at_minute set, or neither), disabling", id)
			wf.Enabled = false
		}

		agentTimeout := time.Duration(defaultAgentRunTimeoutSeconds) * time.Second
		if wf.Agent.Timeout != "" {
			if d, err := time.ParseDuration(wf.Agent.Timeout); err == nil {
				agentTimeout = d
			} else {
				logger.Infof("[workflows] %q: invalid agent timeout %q, using default", id, wf.Agent.Timeout)
			}
		}

		cronSpec := wf.Trigger.toCronSpec()
		if strings.TrimSpace(wf.Trigger.Every) != "" && cronSpec.every <= 0 {
			logger.Infof("[workflows] %q: invalid every duration %q, disabling", id, wf.Trigger.Every)
			wf.Enabled = false
		}

		spec := agentworkflow.CronSpec{
			Every:     cronSpec.every,
			Timezone:  cronSpec.timezone,
			StartHour: cronSpec.startHour,
			EndHour:   cronSpec.endHour,
			AtHour:    cronSpec.atHour,
			AtMinute:  cronSpec.atMinute,
		}

		name := wf.Agent.Name
		if name == "" {
			name = "dispatch_agent"
		}
		mode := wf.Agent.Mode
		if mode == "" {
			mode = "react"
		}

		defs = append(defs, agentworkflow.Definition{
			ID:          id,
			Name:        wf.Name,
			Description: wf.Description,
			Enabled:     wf.Enabled,
			Trigger: agentworkflow.Trigger{
				Kind: agentworkflow.TriggerCron,
				Cron: &spec,
			},
			Agent: agentworkflow.AgentSpec{
				Name:     name,
				Mode:     mode,
				MaxSteps: wf.Agent.MaxSteps,
				Timeout:  agentTimeout,
			},
			Metadata: wf.Metadata,
		})
	}
	return defs
}

func parseChatReact(agent settingsWorkflowAgent) agentworkflow.AgentSpec {
	spec := agentworkflow.AgentSpec{
		Name:     strings.TrimSpace(agent.Name),
		Mode:     strings.TrimSpace(agent.Mode),
		MaxSteps: agent.MaxSteps,
		Timeout:  time.Duration(defaultAgentRunTimeoutSeconds) * time.Second,
	}
	if spec.Name == "" {
		spec.Name = "dispatch_agent"
	}
	if spec.Mode == "" {
		spec.Mode = "react"
	}
	if spec.MaxSteps <= 0 {
		spec.MaxSteps = 200
	}
	if agent.Timeout != "" {
		if d, err := time.ParseDuration(agent.Timeout); err == nil {
			spec.Timeout = d
		} else {
			logger.Infof("[chat_react] invalid agent timeout %q, using default", agent.Timeout)
		}
	}
	return spec
}

func metadataString(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	return s
}

func metadataCSV(m map[string]any, key string, fallback []string) []string {
	s := metadataString(m, key, "")
	if s == "" {
		return fallback
	}
	var items []string
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func metadataInt(m map[string]any, key string, fallback int) int {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return fallback
	}
}

func metadataDuration(m map[string]any, key string, fallback time.Duration) time.Duration {
	s := metadataString(m, key, "")
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

type settingsWorkflowDef struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Enabled     bool                    `json:"enabled"`
	Trigger     settingsWorkflowTrigger `json:"trigger"`
	Agent       settingsWorkflowAgent   `json:"agent"`
	Metadata    map[string]any          `json:"metadata"`
}

type settingsWorkflowTrigger struct {
	Every     string `json:"every"`
	Timezone  string `json:"timezone"`
	StartHour int    `json:"start_hour"`
	EndHour   int    `json:"end_hour"`
	AtHour    *int   `json:"at_hour"`
	AtMinute  *int   `json:"at_minute"`
}

type settingsWorkflowAgent struct {
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	MaxSteps int    `json:"max_steps"`
	Timeout  string `json:"timeout"`
}

type parsedCronSpec struct {
	every     time.Duration
	timezone  string
	startHour int
	endHour   int
	atHour    *int
	atMinute  *int
}

func (t settingsWorkflowTrigger) valid() bool {
	hasEvery := strings.TrimSpace(t.Every) != ""
	hasAt := t.AtHour != nil && t.AtMinute != nil
	if hasEvery && hasAt {
		return false
	}
	return hasEvery || hasAt
}

func (t settingsWorkflowTrigger) toCronSpec() parsedCronSpec {
	spec := parsedCronSpec{
		timezone:  t.Timezone,
		startHour: t.StartHour,
		endHour:   t.EndHour,
		atHour:    t.AtHour,
		atMinute:  t.AtMinute,
	}
	if t.Every != "" {
		d, err := time.ParseDuration(t.Every)
		if err == nil {
			spec.every = d
		}
	}
	if spec.timezone == "" {
		spec.timezone = "Asia/Shanghai"
	}
	return spec
}

func (s settingsLLMModel) modelIDs() []string {
	ids := make([]string, 0, len(s.Options))
	for id := range s.Options {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (s settingsConfig) mcpEndpoints() []agentruntime.MCPEndpoint {
	var out []agentruntime.MCPEndpoint
	for _, server := range s.MCPServers {
		u := strings.TrimSpace(server.URL)
		// url_env 优先：URL 含敏感 token 时通过环境变量注入，不写进配置文件
		if server.URLEnv != "" {
			if v := strings.TrimSpace(env(server.URLEnv, "")); v != "" {
				u = v
			} else {
				logger.Infof("[mcp] %q: %s not set, skipping", server.Name, server.URLEnv)
				continue
			}
		}
		if u == "" {
			continue
		}
		auth := strings.TrimSpace(server.AuthType)

		if auth == agentruntime.MCPAuthOAuth {
			clientID := settingsAPIKey(server.ClientIDEnv)
			clientSecret := settingsAPIKey(server.ClientSecretEnv)
			refreshToken := settingsAPIKey(server.RefreshTokenEnv)
			if clientID == "" || server.TokenURL == "" {
				logger.Infof("[mcp] %q: oauth 缺少 client_id 或 token_url，skipping", server.Name)
				continue
			}
			out = append(out, agentruntime.MCPEndpoint{
				Name:         server.Name,
				URL:          u,
				Auth:         agentruntime.MCPAuthOAuth,
				TokenURL:     strings.TrimSpace(server.TokenURL),
				ClientID:     clientID,
				ClientSecret: clientSecret,
				RefreshToken: refreshToken,
				Scope:        strings.TrimSpace(server.Scope),
				Timeout:      parseMCPTimeout(server.Timeout),
			})
			continue
		}

		key := settingsAPIKey(server.APIKeyEnv)
		if server.APIKeyEnv != "" && key == "" {
			logger.Infof("[mcp] %q: %s not set, skipping", server.Name, server.APIKeyEnv)
			continue
		}
		// 留空时兼容旧逻辑：有 key 走 query，无 key 无认证
		if auth == "" {
			if key != "" {
				auth = agentruntime.MCPAuthQuery
			} else {
				auth = agentruntime.MCPAuthNone
			}
		}
		out = append(out, agentruntime.MCPEndpoint{
			Name:    server.Name,
			URL:     u,
			APIKey:  key,
			Auth:    auth,
			HeaderN: strings.TrimSpace(server.HeaderName),
			Timeout: parseMCPTimeout(server.Timeout),
		})
	}
	return out
}

func parseMCPTimeout(raw string) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

func (s settingsConfig) webFetchEnabled() bool {
	if s.Tools.WebFetch.Enabled == nil {
		return true
	}
	return *s.Tools.WebFetch.Enabled
}

func (s settingsConfig) reminderDefaultChannel() string {
	return normalizeReminderDeliveryChannel(s.Reminder.DefaultChannel, defaultReminderChannel)
}

func (s settingsConfig) storageConfig() settingsStorage {
	st := s.Storage
	st.Backend = strings.TrimSpace(st.Backend)
	if st.Redis.URLEnv == "" {
		st.Redis.URLEnv = "XIAOLI_REDIS_URL"
	}
	if st.Redis.KeyPrefix == "" {
		st.Redis.KeyPrefix = "xiaoli:cp:"
	}
	if st.Redis.MemoryTTLHours <= 0 {
		st.Redis.MemoryTTLHours = 24
	}
	if st.Local.DataDir == "" {
		st.Local.DataDir = "~/.xiaoli"
	}
	if st.Local.MemoryFile == "" {
		st.Local.MemoryFile = "Memory.md"
	}
	if st.Local.ConversationDir == "" {
		st.Local.ConversationDir = "conversations"
	}
	if st.Local.HistoryMaxMessages <= 0 {
		st.Local.HistoryMaxMessages = 40
	}
	return st
}

func (s settingsConfig) bashEnabled() bool {
	if s.Tools.Bash == nil || s.Tools.Bash.Enabled == nil {
		return false
	}
	return *s.Tools.Bash.Enabled
}

const defaultBashTimeout = 30 * time.Second
const defaultBashMaxOutputBytes = 512 * 1024

func (s settingsConfig) bashTimeout() time.Duration {
	if s.Tools.Bash == nil || s.Tools.Bash.TimeoutSeconds == nil {
		return defaultBashTimeout
	}
	return time.Duration(*s.Tools.Bash.TimeoutSeconds) * time.Second
}

func (s settingsConfig) bashMaxOutputBytes() int {
	if s.Tools.Bash == nil || s.Tools.Bash.MaxOutputKB == nil {
		return defaultBashMaxOutputBytes
	}
	return *s.Tools.Bash.MaxOutputKB * 1024
}

func (s settingsConfig) a2aEnabled() bool {
	return s.A2A.Enabled != nil && *s.A2A.Enabled
}

func (s settingsConfig) a2aPublicAgentCard() bool {
	if s.A2A.PublicAgentCard == nil {
		return true // default to true
	}
	return *s.A2A.PublicAgentCard
}

func (s settingsConfig) a2aAPIKeys() map[string]string {
	result := make(map[string]string)
	for _, key := range s.A2A.Keys {
		if key.ID == "" || key.SecretEnv == "" {
			continue
		}
		if !validKeyIDChars.MatchString(key.ID) {
			continue
		}
		secret := env(key.SecretEnv, "")
		if secret != "" {
			result[key.ID] = secret
		}
	}
	return result
}

func (s settingsConfig) a2aMaxInputChars() int {
	if s.A2A.MaxInputChars == nil || *s.A2A.MaxInputChars <= 0 {
		return 2000 // default
	}
	return *s.A2A.MaxInputChars
}

func (s settingsConfig) a2aTimeoutSeconds() int {
	if s.A2A.TimeoutSeconds == nil || *s.A2A.TimeoutSeconds <= 0 {
		return defaultA2ARequestTimeoutSeconds
	}
	return *s.A2A.TimeoutSeconds
}

func (s settingsConfig) a2aRateLimitPerKey() int {
	if s.A2A.RateLimitPerMin == nil || *s.A2A.RateLimitPerMin <= 0 {
		return 30 // default
	}
	return *s.A2A.RateLimitPerMin
}

func (s settingsConfig) a2aRateLimitGlobal() int {
	if s.A2A.RateLimitGlobal == nil || *s.A2A.RateLimitGlobal <= 0 {
		return 120 // default
	}
	return *s.A2A.RateLimitGlobal
}

func (s settingsConfig) a2aMaxConcurrentPerKey() int {
	if s.A2A.MaxConcurrent == nil || *s.A2A.MaxConcurrent <= 0 {
		return 2 // default
	}
	return *s.A2A.MaxConcurrent
}

func (s settingsConfig) a2aTaskTTLSeconds() int {
	if s.A2A.TaskTTLSeconds == nil || *s.A2A.TaskTTLSeconds <= 0 {
		return 1800 // default: 30 minutes
	}
	return *s.A2A.TaskTTLSeconds
}

func (s settingsConfig) a2aAllowedSkills() []string {
	return s.A2A.AllowedSkills
}

func (s settingsConfig) a2aTraceEnabled() bool {
	return s.A2A.Trace.Enabled != nil && *s.A2A.Trace.Enabled
}

func (s settingsConfig) a2aTraceLogInputs() bool {
	return s.A2A.Trace.LogInputs != nil && *s.A2A.Trace.LogInputs
}

func (s settingsConfig) a2aTraceLogOutputs() bool {
	if s.A2A.Trace.LogOutputs == nil {
		return false
	}
	return *s.A2A.Trace.LogOutputs
}

func (s settingsConfig) a2aTraceMaxValueLength() int {
	if s.A2A.Trace.MaxValueLength == nil || *s.A2A.Trace.MaxValueLength <= 0 {
		return 800
	}
	return *s.A2A.Trace.MaxValueLength
}

func settingsAPIKey(envName string) string {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return ""
	}
	return os.Getenv(envName)
}

func defaultAgentPromptRoots() []string {
	return []string{
		".",
		"../..",
		"/opt/xiaoli",
	}
}

func loadAgentPrompt(roots []string) string {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		agent := readPromptMarkdown(filepath.Join(root, "AGENT.md"))
		soul := readPromptMarkdown(filepath.Join(root, "SOUL.md"))
		switch {
		case agent != "" && soul != "":
			return agent + "\n\n" + soul
		case agent != "":
			return agent
		case soul != "":
			return soul
		}
	}
	return ""
}

func readPromptMarkdown(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (c Config) LarkEnabled() bool {
	return strings.TrimSpace(c.LarkAppID) != "" && strings.TrimSpace(c.LarkAppToken) != ""
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(name string, fallback float64) float64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func csv(value string) []string {
	if value == "" {
		return nil
	}
	var items []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func capDuration(d, cap time.Duration) time.Duration {
	if d > cap {
		return cap
	}
	return d
}
