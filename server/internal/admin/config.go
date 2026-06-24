package admin

import (
	"encoding/json"
	"github.com/mnhkahn/gogogo/logger"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	agentruntime "xiaoli/server/internal/agent/runtime"
	agentbuiltin "xiaoli/server/internal/agent/tool/builtin"
	agentskill "xiaoli/server/internal/agent/tool/skill"
	agentworkflow "xiaoli/server/internal/agent/workflow"
)

const fallbackLLMPrompt = "你是一个叫小李的中文语音助手。回答要简短、自然、适合通过扬声器播放。"

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
	Workflows               []agentworkflow.Definition
	LarkWebhookURL          string
	LarkAppID               string
	LarkAppToken            string
	WeChatEnabled           bool
	WeChatBotToken          string
	WeChatBaseURL           string
	RedisURL                string
	RedisKeyPrefix          string
	MemoryTTL               time.Duration
	DataDir                 string
	Timezone                string
	Now                     func() time.Time
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
		GoLLMTimeout:            time.Duration(envInt("XIAOLI_GO_LLM_TIMEOUT_SECONDS", 120)) * time.Second,
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
		Workflows:               parseWorkflows(settings.Workflows),
		LarkWebhookURL:          env("LARK_BOT_WEBHOOK_URL", ""),
		LarkAppID:               env("LARK_APP_ID", ""),
		LarkAppToken:            env("LARK_APP_TOKEN", ""),
		WeChatEnabled:           envBool("WECHAT_ENABLED", false),
		WeChatBotToken:          env("WECHAT_BOT_TOKEN", ""),
		WeChatBaseURL:           env("WECHAT_BASE_URL", wechatDefaultBaseURL),
		RedisURL:                env("XIAOLI_REDIS_URL", ""),
		RedisKeyPrefix:          env("XIAOLI_REDIS_KEY_PREFIX", "xiaoli:cp:"),
		MemoryTTL:               time.Duration(envInt("XIAOLI_MEMORY_TTL_HOURS", 24)) * time.Hour,
		DataDir:                 env("XIAOLI_DATA_DIR", "/data"),
		Timezone:                env("TZ", "Asia/Shanghai"),
	}
	return cfg
}

func defaultSettingsPaths() []string {
	return []string{
		"settings.json",
		"/opt/xiaoli/settings.json",
	}
}

type settingsConfig struct {
	Models     settingsModels                 `json:"models"`
	MCPServers []settingsMCPServer            `json:"mcp_servers"`
	Tools      settingsTools                  `json:"tools"`
	Workflows  map[string]settingsWorkflowDef `json:"cron"`
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

		agentTimeout := 120 * time.Second
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
				URL:          u,
				Auth:         agentruntime.MCPAuthOAuth,
				TokenURL:     strings.TrimSpace(server.TokenURL),
				ClientID:     clientID,
				ClientSecret: clientSecret,
				RefreshToken: refreshToken,
				Scope:        strings.TrimSpace(server.Scope),
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
			URL:     u,
			APIKey:  key,
			Auth:    auth,
			HeaderN: strings.TrimSpace(server.HeaderName),
		})
	}
	return out
}

func (s settingsConfig) webFetchEnabled() bool {
	if s.Tools.WebFetch.Enabled == nil {
		return true
	}
	return *s.Tools.WebFetch.Enabled
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
