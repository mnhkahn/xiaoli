package admin

import (
	"os"
	"path/filepath"
	"testing"

	agentskill "xiaoli/server/internal/agent/tool/skill"
)

func TestLoadConfigSetsDefaultSkillConfig(t *testing.T) {
	t.Setenv("XIAOLI_SKILL_ROOTS", "")
	t.Setenv("XIAOLI_ENABLED_SKILLS", "")
	t.Setenv("XIAOLI_SKILL_MAX_BYTES", "")
	t.Setenv("XIAOLI_SKILL_EXEC_TIMEOUT_SECONDS", "")
	t.Setenv("XIAOLI_SKILL_EXEC_MAX_OUTPUT_BYTES", "")
	t.Setenv("XIAOLI_SKILL_EXEC_GLOBAL_BIN_DIRS", "")

	cfg := LoadConfig()
	if len(cfg.SkillRoots) != 1 || cfg.SkillRoots[0] != "/opt/xiaoli/skills" {
		t.Fatalf("SkillRoots = %#v, want /opt/xiaoli/skills", cfg.SkillRoots)
	}
	if len(cfg.EnabledSkills) != 1 || cfg.EnabledSkills[0] != "*" {
		t.Fatalf("EnabledSkills = %#v, want *", cfg.EnabledSkills)
	}
	if cfg.SkillMaxBytes != agentskill.DefaultMaxBytes {
		t.Fatalf("SkillMaxBytes = %d, want %d", cfg.SkillMaxBytes, agentskill.DefaultMaxBytes)
	}
	if cfg.SkillExecTimeout != agentskill.DefaultExecTimeout {
		t.Fatalf("SkillExecTimeout = %s, want %s", cfg.SkillExecTimeout, agentskill.DefaultExecTimeout)
	}
	if cfg.SkillExecMaxOutputBytes != agentskill.DefaultExecMaxOutputBytes {
		t.Fatalf("SkillExecMaxOutputBytes = %d, want %d", cfg.SkillExecMaxOutputBytes, agentskill.DefaultExecMaxOutputBytes)
	}
	if len(cfg.SkillExecGlobalBinDirs) != 1 || cfg.SkillExecGlobalBinDirs[0] != "/usr/local/bin" {
		t.Fatalf("SkillExecGlobalBinDirs = %#v, want /usr/local/bin", cfg.SkillExecGlobalBinDirs)
	}
}

func TestLoadConfigReadsLLMModelOptions(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{
		"models": {
			"llm": {
				"default": "model-a",
				"options": {
					"model-b": {"base_url": "https://example.test", "model": "real-b"},
					"model-a": {"base_url": "https://example.test", "model": "real-a"}
				}
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadConfig()

	if cfg.GoLLMModel != "model-a" {
		t.Fatalf("GoLLMModel = %q, want model-a", cfg.GoLLMModel)
	}
	if len(cfg.GoLLMModels) != 2 || cfg.GoLLMModels[0] != "model-a" || cfg.GoLLMModels[1] != "model-b" {
		t.Fatalf("GoLLMModels = %#v, want model-a/model-b", cfg.GoLLMModels)
	}
}

func TestLoadConfigDefaultsLLMModelOptionsToCurrentModel(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{
		"models": {
			"llm": {
				"options": {
					"model-a": {"base_url": "https://example.test", "model": "real-a"}
				}
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadConfig()

	if cfg.GoLLMModel != "model-a" {
		t.Fatalf("GoLLMModel = %q, want first configured model", cfg.GoLLMModel)
	}
	if len(cfg.GoLLMModels) != 1 || cfg.GoLLMModels[0] != "model-a" {
		t.Fatalf("GoLLMModels = %#v, want configured model", cfg.GoLLMModels)
	}
}

func TestLoadConfigReadsModelAndMCPSettingsFromJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("SILICONFLOW_API_KEY", "secret-from-env")
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{
		"models": {
			"llm": {
				"default": "siliconflow:qwen3-8b",
				"options": {
					"siliconflow:qwen3-8b": {
						"name": "Qwen3 8B",
						"base_url": "https://settings.example/v1/chat/completions",
						"model": "Qwen/Qwen3-8B",
						"api_key_env": "SILICONFLOW_API_KEY"
					},
					"siliconflow:deepseek-v3": {
						"name": "DeepSeek V3",
						"base_url": "https://settings.example/v1/chat/completions",
						"model": "deepseek-ai/DeepSeek-V3",
						"api_key_env": "SILICONFLOW_API_KEY"
					}
				}
			},
			"vision": {
				"base_url": "https://settings.example/v1/chat/completions",
				"model": "Qwen/Qwen3-VL-8B-Instruct",
				"api_key_env": "SILICONFLOW_API_KEY"
			},
			"asr": {
				"base_url": "https://settings.example/v1/audio/transcriptions",
				"model": "FunAudioLLM/SenseVoiceSmall",
				"api_key_env": "SILICONFLOW_API_KEY"
			},
			"tts": {
				"base_url": "https://settings.example/v1/audio/speech",
				"model": "FunAudioLLM/CosyVoice2-0.5B",
				"voice": "FunAudioLLM/CosyVoice2-0.5B:anna",
				"response_format": "opus",
				"api_key_env": "SILICONFLOW_API_KEY"
			}
		},
		"mcp_servers": [
			{
				"name": "CYEAM",
				"url": "https://cyeam-wiki-mcp-production.up.railway.app/mcp"
			}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadConfig()

	if cfg.GoLLMModel != "siliconflow:qwen3-8b" {
		t.Fatalf("GoLLMModel = %q, want settings default id", cfg.GoLLMModel)
	}
	if cfg.GoLLMURL != "https://settings.example/v1/chat/completions" {
		t.Fatalf("GoLLMURL = %q, want settings URL", cfg.GoLLMURL)
	}
	if cfg.GoLLMAPIKey != "secret-from-env" {
		t.Fatalf("GoLLMAPIKey = %q, want env secret", cfg.GoLLMAPIKey)
	}
	if len(cfg.GoLLMModels) != 2 || cfg.GoLLMModels[0] != "siliconflow:deepseek-v3" || cfg.GoLLMModels[1] != "siliconflow:qwen3-8b" {
		t.Fatalf("GoLLMModels = %#v, want sorted settings ids", cfg.GoLLMModels)
	}
	if got := cfg.GoLLMModelConfigs["siliconflow:qwen3-8b"]; got.Model != "Qwen/Qwen3-8B" || got.DisplayName != "Qwen3 8B" {
		t.Fatalf("GoLLMModelConfigs[qwen] = %#v, want model config", got)
	}
	if cfg.GoVLLMURL != "https://settings.example/v1/chat/completions" || cfg.GoVLLMModel != "Qwen/Qwen3-VL-8B-Instruct" || cfg.GoVLLMAPIKey != "secret-from-env" {
		t.Fatalf("vision config = url %q model %q key %q, want settings", cfg.GoVLLMURL, cfg.GoVLLMModel, cfg.GoVLLMAPIKey)
	}
	if cfg.GoASRURL != "https://settings.example/v1/audio/transcriptions" || cfg.GoASRModel != "FunAudioLLM/SenseVoiceSmall" || cfg.GoASRAPIKey != "secret-from-env" {
		t.Fatalf("asr config = url %q model %q key %q, want settings", cfg.GoASRURL, cfg.GoASRModel, cfg.GoASRAPIKey)
	}
	if cfg.GoTTSURL != "https://settings.example/v1/audio/speech" || cfg.GoTTSModel != "FunAudioLLM/CosyVoice2-0.5B" || cfg.GoTTSVoice != "FunAudioLLM/CosyVoice2-0.5B:anna" || cfg.GoTTSResponseFormat != "opus" || cfg.GoTTSAPIKey != "secret-from-env" {
		t.Fatalf("tts config = %#v/%q/%q/%q/%q, want settings", cfg.GoTTSURL, cfg.GoTTSModel, cfg.GoTTSVoice, cfg.GoTTSResponseFormat, cfg.GoTTSAPIKey)
	}
	if len(cfg.ExternalMCPEndpoints) != 1 || cfg.ExternalMCPEndpoints[0].URL != "https://cyeam-wiki-mcp-production.up.railway.app/mcp" {
		t.Fatalf("ExternalMCPEndpoints = %#v, want settings MCP URL", cfg.ExternalMCPEndpoints)
	}
}

func TestLoadConfigReadsBuiltinWebFetchSettings(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig()
	if !cfg.BuiltinWebFetchEnabled {
		t.Fatal("BuiltinWebFetchEnabled = false, want default true")
	}

	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{
		"tools": {
			"webfetch": {"enabled": false}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = LoadConfig()
	if cfg.BuiltinWebFetchEnabled {
		t.Fatal("BuiltinWebFetchEnabled = true, want configured false")
	}
}

func TestLoadConfigReadsLLMPromptFromAgentMarkdown(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("你是仓库里的小李。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XIAOLI_GO_LLM_PROMPT", "env prompt should not be used")

	cfg := LoadConfig()

	if cfg.GoLLMPrompt != "你是仓库里的小李。" {
		t.Fatalf("GoLLMPrompt = %q, want AGENT.md content", cfg.GoLLMPrompt)
	}
}

func TestLoadAgentPromptCombinesAgentAndSoulMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("agent rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("soul voice\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt := loadAgentPrompt([]string{dir})

	if prompt != "agent rules\n\nsoul voice" {
		t.Fatalf("prompt = %q, want AGENT and SOUL joined", prompt)
	}
}
