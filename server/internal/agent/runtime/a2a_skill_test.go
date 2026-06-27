package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
)

func TestNewAgentInitializesA2ASkillsIndependentlyFromDefaultSkills(t *testing.T) {
	root := t.TempDir()
	writeRuntimeTestSkill(t, root, "news", "公开新闻查询", "Use cyeam news search.\n")

	agent := NewAgent(Config{
		LLMURL:                  "https://example.test",
		LLMAPIKey:               "test-key",
		LLMModel:                "test-model",
		SkillRoots:              []string{root},
		EnabledSkills:           []string{"missing"},
		A2AAllowedSkills:        []string{"news"},
		SkillExecTimeout:        5 * time.Second,
		SkillExecMaxOutputBytes: 1024,
	}, nil)
	if agent == nil {
		t.Fatal("NewAgent() returned nil")
	}
	if agent.skillMW != nil {
		t.Fatal("ordinary skill middleware should not initialize for missing EnabledSkills")
	}
	if agent.a2aSkillMW == nil {
		t.Fatal("A2A skill middleware should initialize from A2AAllowedSkills independently")
	}
}

func TestA2ASkillContentBuilderAllowsAllowedSkillCommands(t *testing.T) {
	binDir := t.TempDir()
	writeRuntimeTestExecutable(t, filepath.Join(binDir, "cyeam"), "#!/bin/sh\nprintf 'argv:%s\\n' \"$*\"\n")
	skill := einoskill.Skill{
		FrontMatter:   einoskill.FrontMatter{Name: "news", Description: "公开新闻查询"},
		Content:       "Use cyeam news search.\n",
		BaseDirectory: t.TempDir(),
	}
	build := newA2ASkillContentBuilder(Config{
		SkillExecTimeout:        5 * time.Second,
		SkillExecMaxOutputBytes: 1024,
		SkillExecGlobalBinDirs:  []string{binDir},
	}, nil)

	got, err := build(context.Background(), skill, `{"skill":"news","argv":["cyeam","news","search","OpenAI"]}`)
	if err != nil {
		t.Fatalf("BuildContent(argv) error = %v", err)
	}
	if !strings.Contains(got, "completed") || !strings.Contains(got, "argv:news search OpenAI") {
		t.Fatalf("BuildContent(argv) = %q, want executed cyeam command with query argument", got)
	}

	got, err = build(context.Background(), skill, `{"skill":"news","cmd":"cyeam news search OpenAI"}`)
	if err != nil {
		t.Fatalf("BuildContent(cmd) error = %v", err)
	}
	if !strings.Contains(got, "completed") || !strings.Contains(got, "argv:news search OpenAI") {
		t.Fatalf("BuildContent(cmd) = %q, want executed cyeam command with query argument", got)
	}
}

func writeRuntimeTestExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func writeRuntimeTestSkill(t *testing.T, root, name, desc, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
}
