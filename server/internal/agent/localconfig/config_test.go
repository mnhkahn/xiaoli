package localconfig

import (
	"os"
	"path/filepath"
	"testing"
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
