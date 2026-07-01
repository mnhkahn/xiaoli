package localconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentruntime "github.com/mnhkahn/xiaoli-esp32/server/internal/agent/runtime"
)

type Config struct {
	DataDir string        `json:"data_dir"`
	Models  ModelConfig   `json:"models"`
	Storage StorageConfig `json:"storage"`
	Tools   ToolConfig    `json:"tools"`
	Skills  SkillConfig   `json:"skills"`

	secrets map[string]string
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
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(cfg.DataDir, "settings.json")
	}
	data, err := os.ReadFile(expandHome(path))
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

func EnsureDefaults(path string) (Config, error) {
	cfg := DefaultConfig()
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(cfg.DataDir, "settings.json")
	}
	path = expandHome(path)
	cfg.DataDir = filepath.Dir(path)
	cfg.applyDefaults()
	if _, err := os.Stat(path); err == nil {
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
	secretPath := filepath.Join(cfg.DataDir, "secrets.json")
	if _, err := os.Stat(secretPath); os.IsNotExist(err) {
		if err := os.WriteFile(secretPath, []byte("{\n}\n"), 0600); err != nil {
			return Config{}, err
		}
	} else if err != nil {
		return Config{}, err
	}
	return Load(path)
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
	selected, ok := models[c.Models.Default]
	if !ok {
		return agentruntime.Config{}, fmt.Errorf("local config model %q is not configured", c.Models.Default)
	}
	return agentruntime.Config{
		LLMURL:                  selected.BaseURL,
		LLMAPIKey:               selected.APIKey,
		LLMModel:                c.Models.Default,
		LLMModelConfigs:         models,
		LLMPrompt:               prompt,
		LLMTimeout:              120 * time.Second,
		StorageBackend:          c.Storage.Backend,
		LocalDataDir:            c.DataDir,
		LocalMemoryFile:         c.Storage.MemoryFile,
		LocalConversationDir:    c.Storage.ConversationDir,
		LocalHistoryMaxMessages: c.Storage.HistoryMaxMessages,
		BuiltinWebFetchEnabled:  c.Tools.WebFetch,
		SkillRoots:              c.Skills.Roots,
		EnabledSkills:           c.Skills.Enabled,
		SkillExecGlobalBinDirs:  c.Skills.GlobalBinDirs,
		TaskAllowedRoots:        c.Tools.AllowedRoots,
		BashConfig: agentruntime.BashConfig{
			Enabled:        c.Tools.Bash,
			Timeout:        time.Duration(c.Tools.BashTimeoutSeconds) * time.Second,
			MaxOutputBytes: int64(c.Tools.BashMaxOutputKB) * 1024,
		},
		LogDir:   filepath.Join(c.DataDir, "logs"),
		Timezone: "Asia/Shanghai",
	}, nil
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
