package localconfig

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mnhkahn/xiaoli/internal/agent/modelcatalog"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
)

const defaultLLMTimeout = 240 * time.Second

type Config struct {
	DataDir      string             `json:"data_dir"`
	Models       ModelConfig        `json:"models"`
	ModelCatalog ModelCatalogConfig `json:"model_catalog"`
	MCPServers   []MCPServerConfig  `json:"mcp_servers"`
	Storage      StorageConfig      `json:"storage"`
	Tools        ToolConfig         `json:"tools"`
	Skills       SkillConfig        `json:"skills"`

	secrets map[string]string
}

// MCPServerConfig defines a remote MCP endpoint available to the local TUI.
// Secret values are read from the environment or secrets.json via their *_env fields.
type MCPServerConfig struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	URLEnv     string `json:"url_env"`
	APIKeyEnv  string `json:"api_key_env"`
	AuthType   string `json:"auth_type"`
	HeaderName string `json:"header_name"`

	TokenURL        string `json:"token_url"`
	ClientIDEnv     string `json:"client_id_env"`
	ClientSecretEnv string `json:"client_secret_env"`
	RefreshTokenEnv string `json:"refresh_token_env"`
	Scope           string `json:"scope"`
	Timeout         string `json:"timeout"`
}

type ModelConfig struct {
	Default string                   `json:"default"`
	Options map[string]ModelEndpoint `json:"options"`
}

type ModelEndpoint struct {
	Name          string `json:"name"`
	BaseURL       string `json:"base_url"`
	Model         string `json:"model"`
	APIKeyEnv     string `json:"api_key_env"`
	APIKey        string `json:"api_key"`
	MaxTokens     int    `json:"max_tokens"`
	ContextLength int    `json:"context_length"`
}

type ModelCatalogConfig struct {
	Enabled         bool                             `json:"enabled"`
	URL             string                           `json:"url"`
	RefreshInterval string                           `json:"refresh_interval"`
	Timeout         string                           `json:"timeout"`
	Providers       map[string]modelcatalog.Provider `json:"providers"`
}

func (c ModelCatalogConfig) catalogConfig() modelcatalog.Config {
	refresh, _ := time.ParseDuration(c.RefreshInterval)
	timeout, _ := time.ParseDuration(c.Timeout)
	return modelcatalog.Config{Enabled: c.Enabled, URL: c.URL, RefreshInterval: refresh, Timeout: timeout, Providers: c.Providers}
}

type StorageConfig struct {
	Backend            string `json:"backend"`
	MemoryFile         string `json:"memory_file"`
	ConversationDir    string `json:"conversation_dir"`
	HistoryMaxMessages int    `json:"history_max_messages"`
	RunDir             string `json:"run_dir"`
}

type ToolConfig struct {
	WebFetch           bool     `json:"webfetch"`
	Bash               bool     `json:"bash"`
	BashTimeoutSeconds int      `json:"bash_timeout_seconds"`
	BashMaxOutputKB    int      `json:"bash_max_output_kb"`
	AllowedRoots       []string `json:"allowed_roots"`
}

type SkillConfig struct {
	Roots         []string `json:"roots"`
	Enabled       []string `json:"enabled"`
	GlobalBinDirs []string `json:"global_bin_dirs"`
}

const workspaceSkillRoot = ".agents/skills"

func DefaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".xiaoli")
	}
	return ".xiaoli"
}

func DefaultAgentsDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".agents")
	}
	return ".agents"
}

func DefaultConfig() Config {
	dataDir := DefaultDataDir()
	agentsDir := DefaultAgentsDir()
	return Config{
		DataDir: dataDir,
		Storage: StorageConfig{
			Backend:            "local",
			MemoryFile:         "Memory.md",
			ConversationDir:    "conversations",
			HistoryMaxMessages: 40,
			RunDir:             "runs",
		},
		Tools: ToolConfig{
			WebFetch:           true,
			Bash:               false,
			BashTimeoutSeconds: 30,
			BashMaxOutputKB:    512,
			AllowedRoots:       []string{dataDir},
		},
		Skills: SkillConfig{
			Roots:         []string{filepath.Join(agentsDir, "skills"), filepath.Join(dataDir, "skills")},
			Enabled:       []string{"*"},
			GlobalBinDirs: []string{filepath.Join(agentsDir, "bin"), filepath.Join(dataDir, "bin"), "/usr/local/bin"},
		},
	}
}

func Load(path string) (Config, error) {
	cfg := DefaultConfig()
	path = SettingsPath(path)
	cfg.DataDir = filepath.Dir(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	if secrets, err := LoadSecrets(filepath.Join(cfg.DataDir, "secrets.json")); err == nil {
		cfg.secrets = secrets
	} else if !os.IsNotExist(err) {
		return Config{}, err
	}
	return cfg, nil
}

func LoadSecrets(path string) (map[string]string, error) {
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, err
	}
	var secrets map[string]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, err
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	return secrets, nil
}

func SettingsPath(path string) string {
	cfg := DefaultConfig()
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(cfg.DataDir, "settings.json")
	}
	return expandHome(path)
}

func EnsureDefaults(path string) (Config, error) {
	cfg := DefaultConfig()
	path = SettingsPath(path)
	cfg.DataDir = filepath.Dir(path)
	cfg.applyDefaults()
	if _, err := os.Stat(path); err == nil {
		if err := ensureSecretsFile(cfg.DataDir); err != nil {
			return Config{}, err
		}
		return Load(path)
	} else if !os.IsNotExist(err) {
		return Config{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return Config{}, err
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Config{}, err
	}
	if err := os.WriteFile(path, append(body, '\n'), 0600); err != nil {
		return Config{}, err
	}
	if err := ensureSecretsFile(cfg.DataDir); err != nil {
		return Config{}, err
	}
	return Load(path)
}

func ensureSecretsFile(dataDir string) error {
	secretPath := filepath.Join(dataDir, "secrets.json")
	if _, err := os.Stat(secretPath); os.IsNotExist(err) {
		return os.WriteFile(secretPath, []byte("{\n}\n"), 0600)
	} else if err != nil {
		return err
	}
	return nil
}

func Save(path string, cfg Config) error {
	path = SettingsPath(path)
	cfg.DataDir = filepath.Dir(path)
	cfg.applyDefaults()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0600)
}

func SaveSecrets(dataDir string, secrets map[string]string) error {
	if secrets == nil {
		secrets = map[string]string{}
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, "secrets.json"), append(body, '\n'), 0600)
}

func NeedsModelWizard(cfg Config) bool {
	if strings.TrimSpace(cfg.Models.Default) == "" {
		return true
	}
	if cfg.Models.Options == nil {
		return true
	}
	option, ok := cfg.Models.Options[cfg.Models.Default]
	return !ok || strings.TrimSpace(option.BaseURL) == "" || strings.TrimSpace(option.Model) == ""
}

type ModelProviderPreset struct {
	ID            string
	Label         string
	BaseURL       string
	Model         string
	APIKeyEnv     string
	MaxTokens     int
	ContextLength int
	CustomBaseURL bool
	CustomModel   bool
	CustomKeyEnv  bool
}

func ModelProviderPresets() []ModelProviderPreset {
	return []ModelProviderPreset{
		{ID: "openrouter", Label: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", Model: "minimax/minimax-m3:free", APIKeyEnv: "OPENROUTER_API_KEY", MaxTokens: 4096, ContextLength: 1048576},
		{ID: "siliconflow", Label: "SiliconFlow", BaseURL: "https://api.siliconflow.cn/v1", Model: "Qwen/Qwen3-8B", APIKeyEnv: "SILICONFLOW_API_KEY", MaxTokens: 4096, ContextLength: 32768},
		{ID: "ark", Label: "Ark / 火山方舟", BaseURL: "https://ark.cn-beijing.volces.com/api/v3", APIKeyEnv: "ARK_API_KEY", MaxTokens: 4096, ContextLength: 32768, CustomModel: true},
		{ID: "openai", Label: "OpenAI Compatible", BaseURL: "https://api.openai.com/v1", Model: "gpt-4.1-mini", APIKeyEnv: "OPENAI_API_KEY", MaxTokens: 4096, ContextLength: 128000, CustomBaseURL: true, CustomModel: true},
		{ID: "custom", Label: "Custom", APIKeyEnv: "XIAOLI_API_KEY", MaxTokens: 4096, ContextLength: 32768, CustomBaseURL: true, CustomModel: true, CustomKeyEnv: true},
	}
}

func RunModelWizard(path string, in io.Reader, out io.Writer) (Config, error) {
	path = SettingsPath(path)
	cfg, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	scanner := bufio.NewScanner(in)
	presets := ModelProviderPresets()
	fmt.Fprintln(out, "Choose model provider:")
	for i, preset := range presets {
		fmt.Fprintf(out, "%d. %s\n", i+1, preset.Label)
	}
	choice, err := promptRequired(scanner, out, "Provider", "1")
	if err != nil {
		return Config{}, err
	}
	preset, err := selectProviderPreset(presets, choice)
	if err != nil {
		return Config{}, err
	}
	baseURL := preset.BaseURL
	if preset.CustomBaseURL || strings.TrimSpace(baseURL) == "" {
		baseURL, err = promptRequired(scanner, out, "Base URL", baseURL)
		if err != nil {
			return Config{}, err
		}
	}
	model := preset.Model
	if preset.CustomModel || strings.TrimSpace(model) == "" {
		model, err = promptRequired(scanner, out, "Model", model)
		if err != nil {
			return Config{}, err
		}
	}
	keyEnv := preset.APIKeyEnv
	if preset.CustomKeyEnv || strings.TrimSpace(keyEnv) == "" {
		keyEnv, err = promptRequired(scanner, out, "API key name", keyEnv)
		if err != nil {
			return Config{}, err
		}
	}
	apiKey, err := promptRequired(scanner, out, "API Key", "")
	if err != nil {
		return Config{}, err
	}
	if cfg.Models.Options == nil {
		cfg.Models.Options = map[string]ModelEndpoint{}
	}
	cfg.Models.Default = preset.ID
	cfg.Models.Options[preset.ID] = ModelEndpoint{
		Name:          preset.Label,
		BaseURL:       baseURL,
		Model:         model,
		APIKeyEnv:     keyEnv,
		MaxTokens:     preset.MaxTokens,
		ContextLength: preset.ContextLength,
	}
	if err := Save(path, cfg); err != nil {
		return Config{}, err
	}
	secrets, err := LoadSecrets(filepath.Join(cfg.DataDir, "secrets.json"))
	if os.IsNotExist(err) {
		secrets = map[string]string{}
	} else if err != nil {
		return Config{}, err
	}
	secrets[keyEnv] = apiKey
	if err := SaveSecrets(cfg.DataDir, secrets); err != nil {
		return Config{}, err
	}
	fmt.Fprintf(out, "Saved model %q to %s\n", preset.ID, path)
	return Load(path)
}

func selectProviderPreset(presets []ModelProviderPreset, choice string) (ModelProviderPreset, error) {
	choice = strings.TrimSpace(strings.ToLower(choice))
	if choice == "" {
		choice = "1"
	}
	for i, preset := range presets {
		if choice == fmt.Sprintf("%d", i+1) || choice == strings.ToLower(preset.ID) || choice == strings.ToLower(preset.Label) {
			return preset, nil
		}
	}
	return ModelProviderPreset{}, fmt.Errorf("unknown model provider %q", choice)
}

func promptRequired(scanner *bufio.Scanner, out io.Writer, label, def string) (string, error) {
	for {
		if strings.TrimSpace(def) != "" {
			fmt.Fprintf(out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(out, "%s: ", label)
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", err
			}
			return "", fmt.Errorf("%s is required", label)
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			text = strings.TrimSpace(def)
		}
		if text != "" {
			return text, nil
		}
		fmt.Fprintf(out, "%s is required.\n", label)
	}
}

func (c *Config) applyDefaults() {
	defaults := DefaultConfig()
	if c.DataDir == "" {
		c.DataDir = defaults.DataDir
	}
	c.DataDir = expandHome(c.DataDir)
	if c.Storage.Backend == "" {
		c.Storage.Backend = defaults.Storage.Backend
	}
	if c.Storage.MemoryFile == "" {
		c.Storage.MemoryFile = defaults.Storage.MemoryFile
	}
	if c.Storage.ConversationDir == "" {
		c.Storage.ConversationDir = defaults.Storage.ConversationDir
	}
	if c.Storage.HistoryMaxMessages <= 0 {
		c.Storage.HistoryMaxMessages = defaults.Storage.HistoryMaxMessages
	}
	if c.Storage.RunDir == "" {
		c.Storage.RunDir = defaults.Storage.RunDir
	}
	if c.Tools.BashTimeoutSeconds <= 0 {
		c.Tools.BashTimeoutSeconds = defaults.Tools.BashTimeoutSeconds
	}
	if c.Tools.BashMaxOutputKB <= 0 {
		c.Tools.BashMaxOutputKB = defaults.Tools.BashMaxOutputKB
	}
	if len(c.Tools.AllowedRoots) == 0 {
		c.Tools.AllowedRoots = defaults.Tools.AllowedRoots
	}
	if len(c.Skills.Roots) == 0 {
		c.Skills.Roots = defaults.Skills.Roots
	}
	if len(c.Skills.Enabled) == 0 {
		c.Skills.Enabled = defaults.Skills.Enabled
	}
	if len(c.Skills.GlobalBinDirs) == 0 {
		c.Skills.GlobalBinDirs = defaults.Skills.GlobalBinDirs
	}
	c.Tools.AllowedRoots = expandPaths(c.Tools.AllowedRoots)
	c.Skills.Roots = expandPaths(c.Skills.Roots)
	c.Skills.GlobalBinDirs = expandPaths(c.Skills.GlobalBinDirs)
}

func (c Config) RuntimeConfig(prompt string) (agentruntime.Config, error) {
	if c.Models.Default == "" {
		return agentruntime.Config{}, fmt.Errorf("local config models.default is required")
	}
	models := make(map[string]agentruntime.LLMModelConfig, len(c.Models.Options))
	for id, option := range c.Models.Options {
		apiKey := option.APIKey
		if apiKey == "" && option.APIKeyEnv != "" {
			apiKey = os.Getenv(option.APIKeyEnv)
		}
		if apiKey == "" && option.APIKeyEnv != "" && c.secrets != nil {
			apiKey = c.secrets[option.APIKeyEnv]
		}
		models[id] = agentruntime.LLMModelConfig{
			ID:            id,
			DisplayName:   option.Name,
			BaseURL:       option.BaseURL,
			Model:         option.Model,
			APIKey:        apiKey,
			MaxTokens:     option.MaxTokens,
			ContextLength: option.ContextLength,
		}
	}
	if catalog, err := modelcatalog.Load(context.Background(), c.ModelCatalog.catalogConfig(), filepath.Join(c.DataDir, "model_catalog.json")); err == nil {
		for _, entry := range modelcatalog.Entries(catalog, c.ModelCatalog.catalogConfig().Providers) {
			if _, exists := models[entry.ID]; exists {
				continue
			}
			apiKey := os.Getenv(entry.APIKeyEnv)
			if apiKey == "" && c.secrets != nil {
				apiKey = c.secrets[entry.APIKeyEnv]
			}
			models[entry.ID] = agentruntime.LLMModelConfig{ID: entry.ID, DisplayName: entry.DisplayName, BaseURL: entry.BaseURL, Model: entry.Model, APIKey: apiKey, MaxTokens: entry.MaxTokens, ContextLength: entry.ContextLength}
		}
	}
	selected, ok := models[c.Models.Default]
	if !ok {
		return agentruntime.Config{}, fmt.Errorf("local config model %q is not configured", c.Models.Default)
	}
	mcpEndpoints := c.mcpEndpoints()
	return agentruntime.Config{
		LLMURL:                  selected.BaseURL,
		LLMAPIKey:               selected.APIKey,
		LLMModel:                c.Models.Default,
		LLMModelConfigs:         models,
		LLMPrompt:               prompt,
		LLMTimeout:              defaultLLMTimeout,
		StorageBackend:          c.Storage.Backend,
		LocalDataDir:            c.DataDir,
		LocalMemoryFile:         c.Storage.MemoryFile,
		LocalConversationDir:    c.Storage.ConversationDir,
		LocalHistoryMaxMessages: c.Storage.HistoryMaxMessages,
		ExternalMCPEndpoints:    mcpEndpoints,
		BuiltinWebFetchEnabled:  c.Tools.WebFetch,
		SkillRoots:              withWorkspaceSkillRoot(c.Skills.Roots),
		EnabledSkills:           c.Skills.Enabled,
		SkillExecGlobalBinDirs:  c.Skills.GlobalBinDirs,
		TaskAllowedRoots:        c.Tools.AllowedRoots,
		BashConfig: agentruntime.BashConfig{
			Enabled:        c.Tools.Bash,
			Timeout:        time.Duration(c.Tools.BashTimeoutSeconds) * time.Second,
			MaxOutputBytes: int64(c.Tools.BashMaxOutputKB) * 1024,
			PolicyPath:     filepath.Join(c.DataDir, "state", "bash_policy.json"),
		},
		LogDir:   filepath.Join(c.DataDir, "logs"),
		Timezone: "Asia/Shanghai",
	}, nil
}

func (c Config) mcpEndpoints() []agentruntime.MCPEndpoint {
	endpoints := make([]agentruntime.MCPEndpoint, 0, len(c.MCPServers))
	for _, server := range c.MCPServers {
		url := strings.TrimSpace(server.URL)
		if server.URLEnv != "" {
			url = c.secretValue(server.URLEnv)
		}
		if url == "" {
			continue
		}

		auth := strings.TrimSpace(server.AuthType)
		if auth == agentruntime.MCPAuthOAuth {
			clientID := c.secretValue(server.ClientIDEnv)
			if clientID == "" || strings.TrimSpace(server.TokenURL) == "" {
				continue
			}
			endpoints = append(endpoints, agentruntime.MCPEndpoint{
				Name:         strings.TrimSpace(server.Name),
				URL:          url,
				Auth:         agentruntime.MCPAuthOAuth,
				TokenURL:     strings.TrimSpace(server.TokenURL),
				ClientID:     clientID,
				ClientSecret: c.secretValue(server.ClientSecretEnv),
				RefreshToken: c.secretValue(server.RefreshTokenEnv),
				Scope:        strings.TrimSpace(server.Scope),
				Timeout:      parseMCPTimeout(server.Timeout),
			})
			continue
		}

		key := c.secretValue(server.APIKeyEnv)
		if server.APIKeyEnv != "" && key == "" {
			continue
		}
		if auth == "" {
			if key == "" {
				auth = agentruntime.MCPAuthNone
			} else {
				auth = agentruntime.MCPAuthQuery
			}
		}
		endpoints = append(endpoints, agentruntime.MCPEndpoint{
			Name:    strings.TrimSpace(server.Name),
			URL:     url,
			APIKey:  key,
			Auth:    auth,
			HeaderN: strings.TrimSpace(server.HeaderName),
			Timeout: parseMCPTimeout(server.Timeout),
		})
	}
	return endpoints
}

func parseMCPTimeout(raw string) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

func (c Config) secretValue(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return strings.TrimSpace(c.secrets[name])
}

func (c Config) LoadPrompt(extra string) (string, error) {
	return c.loadPrompt(extra, DefaultAgentsDir())
}

func (c Config) loadPrompt(extra string, agentsDir string) (string, error) {
	paths := []string{
		filepath.Join(agentsDir, "AGENT.md"),
		filepath.Join(c.DataDir, "AGENT.md"),
		filepath.Join(c.DataDir, "SOUL.md"),
	}
	parts := make([]string, 0, len(paths)+1)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		text := strings.TrimSpace(string(data))
		if text != "" {
			parts = append(parts, text)
		}
	}
	if strings.TrimSpace(extra) != "" {
		parts = append(parts, strings.TrimSpace(extra))
	}
	if len(parts) == 0 {
		parts = append(parts, "你是小李，一个本地运行的中文 Agent。回答要清楚、直接、适合终端阅读。")
	}
	return strings.Join(parts, "\n\n"), nil
}

func (c Config) RunLogDir() string {
	dir := c.Storage.RunDir
	if dir == "" {
		dir = "runs"
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(c.DataDir, dir)
}

func expandPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, expandHome(p))
	}
	return out
}

func withWorkspaceSkillRoot(paths []string) []string {
	out := make([]string, 0, len(paths)+1)
	seen := map[string]bool{}
	for _, path := range paths {
		key := filepath.Clean(strings.TrimSpace(path))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, path)
	}
	key := filepath.Clean(workspaceSkillRoot)
	if !seen[key] {
		out = append(out, workspaceSkillRoot)
	}
	return out
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
