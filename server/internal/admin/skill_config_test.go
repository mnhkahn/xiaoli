package admin

import (
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
	t.Setenv("XIAOLI_GO_LLM_MODEL", "model-a")
	t.Setenv("XIAOLI_GO_LLM_MODELS", "model-a,model-b")

	cfg := LoadConfig()

	if cfg.GoLLMModel != "model-a" {
		t.Fatalf("GoLLMModel = %q, want model-a", cfg.GoLLMModel)
	}
	if len(cfg.GoLLMModels) != 2 || cfg.GoLLMModels[0] != "model-a" || cfg.GoLLMModels[1] != "model-b" {
		t.Fatalf("GoLLMModels = %#v, want model-a/model-b", cfg.GoLLMModels)
	}
}

func TestLoadConfigDefaultsLLMModelOptionsToCurrentModel(t *testing.T) {
	t.Setenv("XIAOLI_GO_LLM_MODEL", "model-a")
	t.Setenv("XIAOLI_GO_LLM_MODELS", "")

	cfg := LoadConfig()

	if len(cfg.GoLLMModels) != 1 || cfg.GoLLMModels[0] != "model-a" {
		t.Fatalf("GoLLMModels = %#v, want current model", cfg.GoLLMModels)
	}
}
