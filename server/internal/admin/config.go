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
	agentskill "xiaoli/server/internal/agent/tool/skill"
)

const fallbackLLMPrompt = "你是一个叫小李的中文语音助手。回答要简短、自然、适合通过扬声器播放。"

type Config struct {
	Host                     string
	Port                     int
	PublicBaseURL            string
	SessionSecret            string
	SessionMaxAge            time.Duration
	LogtoEndpoint            string
	LogtoAppID               string
	LogtoAppSecret           string
	AllowedUsers             []string
	DirectDeviceServer       bool
	DeviceAuthEnabled        bool
	DeviceAuthKey            string
	AllowedDeviceIDs         []string
	BridgeBaseURL            string
	VisionProxyBaseURL       string
	InternalStreamToken      string
	MCPReadyWait             time.Duration
	GoASRURL                 string
	GoASRAPIKey              string
	GoASRModel               string
	GoASRTimeout             time.Duration
	GoLLMURL                 string
	GoLLMAPIKey              string
	GoLLMModel               string
	GoLLMModels              []string
	GoLLMModelConfigs        map[string]LLMModelConfig
	GoLLMPrompt              string
	GoLLMTimeout             time.Duration
	GoVLLMURL                string
	GoVLLMAPIKey             string
	GoVLLMModel              string
	GoVLLMTimeout            time.Duration
	GoTTSURL                 string
	GoTTSAPIKey              string
	GoTTSModel               string
	GoTTSVoice               string
	GoTTSResponseFormat      string
	GoTTSTimeout             time.Duration
	ExternalMCPURLs          []string
	MCPConfigPath            string
	BuiltinWebFetchEnabled   bool
	SkillRoots               []string
	EnabledSkills            []string
	SkillMaxBytes            int64
	SkillExecTimeout         time.Duration
	SkillExecMaxOutputBytes  int64
	SkillExecGlobalBinDirs   []string
	StudyMonitorEnabled      bool
	StudyMonitorTimezone     string
	StudyMonitorStartHour    int
	StudyMonitorEndHour      int
	StudyMonitorInterval     time.Duration
	StudyMonitorCameraTool   string
	StudyMonitorReminder     string
	StudyMonitorToolTimeout  time.Duration
	StudyMonitorDeviceIDs    []string
	MorningGreetingEnabled   bool
	MorningGreetingTimezone  string
	MorningGreetingHour      int
	MorningGreetingMinute    int
	MorningGreetingText      string
	MorningGreetingDeviceIDs []string
	LarkWebhookURL           string
	LarkAppID                string
	LarkAppToken             string
	WeChatEnabled            bool
	WeChatBotToken           string
	WeChatBaseURL            string
	RedisURL                 string
	RedisKeyPrefix           string
	MemoryTTL                time.Duration
	Now                      func() time.Time
}

type LLMModelConfig struct {
	ID          string
	DisplayName string
	BaseURL     string
	Model       string
	APIKey      string
	MaxTokens   int
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
			ID:          id,
			DisplayName: strings.TrimSpace(option.Name),
			BaseURL:     strings.TrimSpace(option.BaseURL),
			Model:       strings.TrimSpace(option.Model),
			APIKey:      settingsAPIKey(option.APIKeyEnv),
			MaxTokens:   option.MaxTokens,
		}
	}
	if goLLMModel == "" && len(goLLMModels) > 0 {
		goLLMModel = goLLMModels[0]
	}
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
		Host:                     env("XIAOLI_ADMIN_HOST", "0.0.0.0"),
		Port:                     envInt("XIAOLI_ADMIN_PORT", 8004),
		PublicBaseURL:            strings.TrimRight(env("ADMIN_PUBLIC_BASE_URL", env("PUBLIC_BASE_URL", "https://xiaoli-server.fly.dev")), "/"),
		SessionSecret:            sessionSecret,
		SessionMaxAge:            time.Duration(envInt("ADMIN_SESSION_MAX_AGE_SECONDS", 604800)) * time.Second,
		LogtoEndpoint:            strings.TrimRight(env("LOGTO_ENDPOINT", ""), "/") + "/",
		LogtoAppID:               env("LOGTO_APP_ID", ""),
		LogtoAppSecret:           env("LOGTO_APP_SECRET", ""),
		AllowedUsers:             csv(env("ADMIN_ALLOWED_USERS", "")),
		DirectDeviceServer:       envBool("XIAOLI_DIRECT_DEVICE_SERVER", false),
		DeviceAuthEnabled:        envBool("ENABLE_SERVER_AUTH", false),
		DeviceAuthKey:            env("SERVER_AUTH_KEY", ""),
		AllowedDeviceIDs:         csv(firstNonEmptyEnv("ALLOWED_DEVICE_IDS", "ALLOWED_DEVICE_ID", "SERVER_AUTH_ALLOWED_DEVICE_IDS")),
		BridgeBaseURL:            strings.TrimRight(env("XIAOLI_BRIDGE_BASE_URL", "http://127.0.0.1:8005"), "/"),
		VisionProxyBaseURL:       strings.TrimRight(env("XIAOLI_VISION_PROXY_BASE_URL", "http://127.0.0.1:8003"), "/"),
		InternalStreamToken:      env("XIAOLI_ADMIN_INTERNAL_TOKEN", sessionSecret),
		MCPReadyWait:             time.Duration(envFloat("ADMIN_MCP_READY_WAIT_SECONDS", 5)) * time.Second,
		GoASRURL:                 strings.TrimSpace(asr.BaseURL),
		GoASRAPIKey:              settingsAPIKey(asr.APIKeyEnv),
		GoASRModel:               strings.TrimSpace(asr.Model),
		GoASRTimeout:             time.Duration(envInt("XIAOLI_GO_ASR_TIMEOUT_SECONDS", 45)) * time.Second,
		GoLLMURL:                 selectedLLM.BaseURL,
		GoLLMAPIKey:              selectedLLM.APIKey,
		GoLLMModel:               goLLMModel,
		GoLLMModels:              goLLMModels,
		GoLLMModelConfigs:        goLLMModelConfigs,
		GoLLMPrompt:              goLLMPrompt,
		GoLLMTimeout:             time.Duration(envInt("XIAOLI_GO_LLM_TIMEOUT_SECONDS", 120)) * time.Second,
		GoVLLMURL:                strings.TrimSpace(vision.BaseURL),
		GoVLLMAPIKey:             settingsAPIKey(vision.APIKeyEnv),
		GoVLLMModel:              strings.TrimSpace(vision.Model),
		GoVLLMTimeout:            time.Duration(envInt("XIAOLI_GO_VLLM_TIMEOUT_SECONDS", 60)) * time.Second,
		GoTTSURL:                 strings.TrimSpace(tts.BaseURL),
		GoTTSAPIKey:              settingsAPIKey(tts.APIKeyEnv),
		GoTTSModel:               strings.TrimSpace(tts.Model),
		GoTTSVoice:               strings.TrimSpace(tts.Voice),
		GoTTSResponseFormat:      strings.TrimSpace(tts.ResponseFormat),
		GoTTSTimeout:             time.Duration(envInt("XIAOLI_GO_TTS_TIMEOUT_SECONDS", 30)) * time.Second,
		MCPConfigPath:            settingsPath,
		ExternalMCPURLs:          settings.mcpURLs(),
		BuiltinWebFetchEnabled:   settings.webFetchEnabled(),
		SkillRoots:               csv(env("XIAOLI_SKILL_ROOTS", "/opt/xiaoli/skills")),
		EnabledSkills:            csv(env("XIAOLI_ENABLED_SKILLS", "*")),
		SkillMaxBytes:            int64(envInt("XIAOLI_SKILL_MAX_BYTES", int(agentskill.DefaultMaxBytes))),
		SkillExecTimeout:         time.Duration(envInt("XIAOLI_SKILL_EXEC_TIMEOUT_SECONDS", int(agentskill.DefaultExecTimeout/time.Second))) * time.Second,
		SkillExecMaxOutputBytes:  int64(envInt("XIAOLI_SKILL_EXEC_MAX_OUTPUT_BYTES", agentskill.DefaultExecMaxOutputBytes)),
		SkillExecGlobalBinDirs:   csv(env("XIAOLI_SKILL_EXEC_GLOBAL_BIN_DIRS", "/usr/local/bin")),
		StudyMonitorEnabled:      envBool("STUDY_MONITOR_ENABLED", false),
		StudyMonitorTimezone:     env("STUDY_MONITOR_TIMEZONE", "Asia/Shanghai"),
		StudyMonitorStartHour:    envInt("STUDY_MONITOR_START_HOUR", 17),
		StudyMonitorEndHour:      envInt("STUDY_MONITOR_END_HOUR", 21),
		StudyMonitorInterval:     time.Duration(envInt("STUDY_MONITOR_INTERVAL_SECONDS", 300)) * time.Second,
		StudyMonitorCameraTool:   env("STUDY_MONITOR_CAMERA_TOOL", "self.camera.take_photo"),
		StudyMonitorReminder:     env("STUDY_MONITOR_REMINDER_TEXT", "请坐直，认真学习。"),
		StudyMonitorToolTimeout:  time.Duration(envInt("STUDY_MONITOR_TOOL_TIMEOUT_SECONDS", 120)) * time.Second,
		StudyMonitorDeviceIDs:    csv(env("STUDY_MONITOR_DEVICE_IDS", "")),
		MorningGreetingEnabled:   envBool("MORNING_GREETING_ENABLED", true),
		MorningGreetingTimezone:  env("MORNING_GREETING_TIMEZONE", "Asia/Shanghai"),
		MorningGreetingHour:      envInt("MORNING_GREETING_HOUR", 8),
		MorningGreetingMinute:    envInt("MORNING_GREETING_MINUTE", 0),
		MorningGreetingText:      env("MORNING_GREETING_TEXT", "早上好。"),
		MorningGreetingDeviceIDs: csv(env("MORNING_GREETING_DEVICE_IDS", "")),
		LarkWebhookURL:           env("LARK_BOT_WEBHOOK_URL", ""),
		LarkAppID:                env("LARK_APP_ID", ""),
		LarkAppToken:             env("LARK_APP_TOKEN", ""),
		WeChatEnabled:            envBool("WECHAT_ENABLED", false),
		WeChatBotToken:           env("WECHAT_BOT_TOKEN", ""),
		WeChatBaseURL:            env("WECHAT_BASE_URL", wechatDefaultBaseURL),
		RedisURL:                 env("XIAOLI_REDIS_URL", ""),
		RedisKeyPrefix:           env("XIAOLI_REDIS_KEY_PREFIX", "xiaoli:cp:"),
		MemoryTTL:                time.Duration(envInt("XIAOLI_MEMORY_TTL_HOURS", 24)) * time.Hour,
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
	Models     settingsModels      `json:"models"`
	MCPServers []settingsMCPServer `json:"mcp_servers"`
	Tools      settingsTools       `json:"tools"`
}

type settingsTools struct {
	WebFetch settingsToolSwitch `json:"webfetch"`
}

type settingsToolSwitch struct {
	Enabled *bool `json:"enabled"`
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
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format"`
}

type settingsMCPServer struct {
	Name string `json:"name"`
	URL  string `json:"url"`
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

func (s settingsConfig) mcpURLs() []string {
	urls := make([]string, 0, len(s.MCPServers))
	for _, server := range s.MCPServers {
		url := strings.TrimSpace(server.URL)
		if url != "" {
			urls = append(urls, url)
		}
	}
	return urls
}

func (s settingsConfig) webFetchEnabled() bool {
	if s.Tools.WebFetch.Enabled == nil {
		return true
	}
	return *s.Tools.WebFetch.Enabled
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
