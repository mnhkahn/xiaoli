package localconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingUsesLocalSafeDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Storage.Backend != "local" {
		t.Fatalf("backend = %q, want local", cfg.Storage.Backend)
	}
	if cfg.Tools.Bash {
		t.Fatal("bash default enabled, want disabled")
	}
	if cfg.Storage.MemoryFile != "Memory.md" {
		t.Fatalf("memory file = %q, want Memory.md", cfg.Storage.MemoryFile)
	}
	roots := strings.Join(cfg.Skills.Roots, "\n")
	if !strings.Contains(roots, filepath.Join(DefaultAgentsDir(), "skills")) {
		t.Fatalf("default skill roots = %#v, want shared agents skills dir", cfg.Skills.Roots)
	}
}

func TestRuntimeConfigResolvesModelAndSecrets(t *testing.T) {
	t.Setenv("LOCAL_TEST_API_KEY", "secret-env")
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	body := `{
		"data_dir": "` + dir + `",
		"models": {
			"default": "test",
			"options": {
				"test": {
					"name": "Test",
					"base_url": "https://example.test/v1",
					"model": "test-model",
					"api_key_env": "LOCAL_TEST_API_KEY",
					"max_tokens": 123,
					"context_length": 456
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	rt, err := cfg.RuntimeConfig("prompt")
	if err != nil {
		t.Fatalf("RuntimeConfig() error = %v", err)
	}
	if rt.StorageBackend != "local" || rt.LLMAPIKey != "secret-env" || rt.LLMModel != "test" {
		t.Fatalf("runtime config = %#v", rt)
	}
	if got := cfg.RunLogDir(); got != filepath.Join(dir, "runs") {
		t.Fatalf("RunLogDir() = %q", got)
	}
}

func TestRuntimeConfigUsesExtendedLLMTimeout(t *testing.T) {
	t.Setenv("LOCAL_TEST_API_KEY", "secret-env")
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	body := `{
		"data_dir": "` + dir + `",
		"models": {
			"default": "test",
			"options": {
				"test": {
					"base_url": "https://example.test/v1",
					"model": "test-model",
					"api_key_env": "LOCAL_TEST_API_KEY"
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	rt, err := cfg.RuntimeConfig("prompt")
	if err != nil {
		t.Fatalf("RuntimeConfig() error = %v", err)
	}
	if rt.LLMTimeout != 240*time.Second {
		t.Fatalf("LLMTimeout = %s, want 4m0s", rt.LLMTimeout)
	}
}

func TestRuntimeConfigIncludesWorkspaceSkillRoot(t *testing.T) {
	t.Setenv("LOCAL_TEST_API_KEY", "secret-env")
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	body := `{
		"data_dir": "` + dir + `",
		"models": {
			"default": "test",
			"options": {
				"test": {
					"base_url": "https://example.test/v1",
					"model": "test-model",
					"api_key_env": "LOCAL_TEST_API_KEY"
				}
			}
		},
		"skills": {
			"roots": ["` + filepath.ToSlash(filepath.Join(dir, "global-skills")) + `"]
		}
	}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	rt, err := cfg.RuntimeConfig("prompt")
	if err != nil {
		t.Fatalf("RuntimeConfig() error = %v", err)
	}
	if !containsPath(rt.SkillRoots, filepath.Join(".agents", "skills")) {
		t.Fatalf("SkillRoots = %#v, want workspace .agents/skills root", rt.SkillRoots)
	}
}

func TestRuntimeConfigResolvesSecretsFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	body := `{
		"data_dir": "` + dir + `",
		"models": {
			"default": "test",
			"options": {
				"test": {
					"base_url": "https://example.test/v1",
					"model": "test-model",
					"api_key_env": "LOCAL_TEST_API_KEY"
				}
			}
		}
	}`
	if err := os.WriteFile(settingsPath, []byte(body), 0600); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.json"), []byte(`{"LOCAL_TEST_API_KEY":"secret-file"}`), 0600); err != nil {
		t.Fatalf("WriteFile(secrets) error = %v", err)
	}
	cfg, err := Load(settingsPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	rt, err := cfg.RuntimeConfig("prompt")
	if err != nil {
		t.Fatalf("RuntimeConfig() error = %v", err)
	}
	if rt.LLMAPIKey != "secret-file" {
		t.Fatalf("LLMAPIKey = %q, want secret-file", rt.LLMAPIKey)
	}
}

func TestRuntimeConfigLoadsMCPServers(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	body := `{
		"data_dir": "` + dir + `",
		"models": {
			"default": "test",
			"options": {
				"test": {"base_url": "https://example.test/v1", "model": "test-model"}
			}
		},
		"mcp_servers": [
			{"name": "xiaohongshu", "url": "http://localhost:18060/mcp"},
			{"name": "protected", "url": "https://mcp.example.test", "auth_type": "bearer", "api_key_env": "MCP_TEST_TOKEN"},
			{"name": "missing-token", "url": "https://skip.example.test", "api_key_env": "MISSING_TOKEN"}
		]
	}`
	if err := os.WriteFile(settingsPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.json"), []byte(`{"MCP_TEST_TOKEN":"secret-token"}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := cfg.RuntimeConfig("prompt")
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.ExternalMCPEndpoints) != 2 {
		t.Fatalf("ExternalMCPEndpoints = %#v, want two valid endpoints", rt.ExternalMCPEndpoints)
	}
	if got := rt.ExternalMCPEndpoints[0]; got.Name != "xiaohongshu" || got.URL != "http://localhost:18060/mcp" || got.Auth != "none" {
		t.Fatalf("xiaohongshu endpoint = %#v", got)
	}
	if got := rt.ExternalMCPEndpoints[1]; got.Name != "protected" || got.Auth != "bearer" || got.APIKey != "secret-token" {
		t.Fatalf("protected endpoint = %#v", got)
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if filepath.Clean(path) == filepath.Clean(want) {
			return true
		}
	}
	return false
}

func TestEnsureDefaultsWritesSettingsAndSecrets(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".xiaoli", "settings.json")
	cfg, err := EnsureDefaults(settingsPath)
	if err != nil {
		t.Fatalf("EnsureDefaults() error = %v", err)
	}
	if cfg.Storage.Backend != "local" {
		t.Fatalf("backend = %q, want local", cfg.Storage.Backend)
	}
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(settingsPath), "secrets.json")); err != nil {
		t.Fatalf("secrets not written next to settings: %v", err)
	}
}

func TestEnsureDefaultsCreatesMissingSecretsWhenSettingsExists(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".xiaoli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureDefaults(settingsPath); err != nil {
		t.Fatalf("EnsureDefaults() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(settingsPath), "secrets.json")); err != nil {
		t.Fatalf("secrets not written next to existing settings: %v", err)
	}
}

func TestRunModelWizardOpenRouter(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".xiaoli", "settings.json")
	if _, err := EnsureDefaults(settingsPath); err != nil {
		t.Fatalf("EnsureDefaults() error = %v", err)
	}
	var out bytes.Buffer
	cfg, err := RunModelWizard(settingsPath, strings.NewReader("\ntest-key\n"), &out)
	if err != nil {
		t.Fatalf("RunModelWizard() error = %v\n%s", err, out.String())
	}
	if cfg.Models.Default != "openrouter" {
		t.Fatalf("default model = %q, want openrouter", cfg.Models.Default)
	}
	option := cfg.Models.Options["openrouter"]
	if option.BaseURL != "https://openrouter.ai/api/v1" || option.Model != "openrouter/free" || option.APIKeyEnv != "OPENROUTER_API_KEY" {
		t.Fatalf("openrouter option = %#v", option)
	}
	secrets, err := LoadSecrets(filepath.Join(filepath.Dir(settingsPath), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if secrets["OPENROUTER_API_KEY"] != "test-key" {
		t.Fatalf("secret = %q, want test-key", secrets["OPENROUTER_API_KEY"])
	}
}

func TestRunModelWizardCustom(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".xiaoli", "settings.json")
	if _, err := EnsureDefaults(settingsPath); err != nil {
		t.Fatalf("EnsureDefaults() error = %v", err)
	}
	input := "5\nhttps://llm.example/v1\ncustom-model\nCUSTOM_API_KEY\nsecret-value\n"
	var out bytes.Buffer
	cfg, err := RunModelWizard(settingsPath, strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("RunModelWizard() error = %v\n%s", err, out.String())
	}
	option := cfg.Models.Options["custom"]
	if cfg.Models.Default != "custom" || option.BaseURL != "https://llm.example/v1" || option.Model != "custom-model" || option.APIKeyEnv != "CUSTOM_API_KEY" {
		t.Fatalf("custom model config = default %q option %#v", cfg.Models.Default, option)
	}
	secrets, err := LoadSecrets(filepath.Join(filepath.Dir(settingsPath), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if secrets["CUSTOM_API_KEY"] != "secret-value" {
		t.Fatalf("secret = %q, want secret-value", secrets["CUSTOM_API_KEY"])
	}
}

func TestLoadPromptUsesSharedThenXiaoliFilesThenExtra(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".agents")
	xiaoliDir := filepath.Join(dir, ".xiaoli")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("MkdirAll agents error = %v", err)
	}
	if err := os.MkdirAll(xiaoliDir, 0755); err != nil {
		t.Fatalf("MkdirAll xiaoli error = %v", err)
	}
	files := map[string]string{
		filepath.Join(agentsDir, "AGENT.md"): "shared",
		filepath.Join(xiaoliDir, "AGENT.md"): "xiaoli",
		filepath.Join(xiaoliDir, "SOUL.md"):  "soul",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	cfg := DefaultConfig()
	cfg.DataDir = xiaoliDir
	got, err := cfg.loadPrompt("extra", agentsDir)
	if err != nil {
		t.Fatalf("loadPrompt() error = %v", err)
	}
	want := "shared\n\nxiaoli\n\nsoul\n\nextra"
	if got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
}
