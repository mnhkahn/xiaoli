package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/schema"
	"github.com/redis/go-redis/v9"

	agentsession "xiaoli/server/internal/agent/session"
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

func TestPromptProfileDefaultMaxStepsMatchesAgentDefault(t *testing.T) {
	if got := promptProfileMaxSteps(0); got != defaultAgentMaxIterations {
		t.Fatalf("promptProfileMaxSteps(0) = %d, want %d", got, defaultAgentMaxIterations)
	}
	if got := promptProfileMaxSteps(3); got != 3 {
		t.Fatalf("promptProfileMaxSteps(3) = %d, want explicit value", got)
	}
}

func TestA2APromptProfileHistoryLoadsAndSavesIsolatedSession(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	mem := &Memory{client: client, prefix: "test:", ttl: time.Hour}
	mgr := agentsession.NewManager(client, "test:")
	agent := &Agent{
		memory:     mem,
		sessionMgr: mgr,
		cfg:        Config{LLMModel: "test-model"},
	}

	sessionKey := "a2a:partner:ctx-1"
	sessionID, _, err := mgr.GetOrCreate(ctx, "a2a_profile", sessionKey, "test-model")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if err := mem.Save(ctx, sessionID, []*schema.Message{
		schema.UserMessage(`{"date":"2026-06-26"}`),
		{Role: schema.Assistant, Content: "上一句鼓励"},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	msgs, loadedSession := agent.buildPromptProfileMessages(ctx, "提示", "新的输入", "a2a", sessionKey)
	if loadedSession != sessionID {
		t.Fatalf("loadedSession = %q, want %q", loadedSession, sessionID)
	}
	if len(msgs) != 4 {
		t.Fatalf("len(msgs) = %d, want system + 2 history + user", len(msgs))
	}
	if msgs[1].Content != `{"date":"2026-06-26"}` || msgs[2].Content != "上一句鼓励" || msgs[3].Content != "新的输入" {
		t.Fatalf("msgs = %#v, want history before current user input", msgs)
	}

	agent.savePromptProfileHistory(ctx, loadedSession, msgs, "新的回复")
	saved := mem.Load(ctx, sessionID)
	if len(saved) != 4 {
		t.Fatalf("len(saved) = %d, want previous 2 + current user/assistant without system; saved=%#v", len(saved), saved)
	}
	if saved[len(saved)-2].Content != "新的输入" || saved[len(saved)-1].Content != "新的回复" {
		t.Fatalf("saved tail = %#v, want current user and assistant", saved[len(saved)-2:])
	}

	info, err := mgr.Get(ctx, sessionID)
	if err != nil {
		t.Fatalf("Get(session) error = %v", err)
	}
	if info.ChannelName != "a2a_profile" || info.ChannelUser != sessionKey || info.Count != len(saved) {
		t.Fatalf("session info = %#v, want isolated a2a profile session with count %d", info, len(saved))
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
