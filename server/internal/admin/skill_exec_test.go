package admin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
)

func TestSkillBuildContentLoadsInstructionsWithoutCommand(t *testing.T) {
	skill := testExecSkill(t, "cyeam-cli", "Use cyeam.\n")
	build := newSkillContentBuilder(skillExecConfig{
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	})

	got, err := build(context.Background(), skill, `{"skill":"cyeam-cli"}`)
	if err != nil {
		t.Fatalf("BuildContent() error = %v", err)
	}
	if !strings.Contains(got, "此 Skill 的目录：") || !strings.Contains(got, skill.BaseDirectory) || !strings.Contains(got, "Use cyeam.") {
		t.Fatalf("BuildContent() = %q, want default skill content with base directory and instructions", got)
	}
}

func TestBuildSkillToolDescriptionExplainsSkillCommandExecution(t *testing.T) {
	desc := buildSkillToolDescription(context.Background(), []einoskill.FrontMatter{{
		Name:        "cyeam-cli",
		Description: "Use cyeam CLI.",
	}})

	for _, want := range []string{"cyeam-cli", "argv", "skill"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("buildSkillToolDescription() missing %q in %q", want, desc)
		}
	}
	if !strings.Contains(desc, "不可直接作为工具名调用") {
		t.Fatalf("buildSkillToolDescription() should warn against calling skill names directly: %q", desc)
	}
}

func TestSkillBuildContentExecutesArgvFromGlobalBin(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "cyeam"), "#!/bin/sh\nprintf 'argv:%s\\n' \"$*\"\n")
	skill := testExecSkill(t, "cyeam-cli", "Use cyeam.\n")
	build := newSkillContentBuilder(skillExecConfig{
		GlobalBinDirs:  []string{binDir},
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	})

	got, err := build(context.Background(), skill, `{"skill":"cyeam-cli","argv":["cyeam","tv","today","--json"]}`)
	if err != nil {
		t.Fatalf("BuildContent() error = %v", err)
	}
	if !strings.Contains(got, "Skill command completed") || !strings.Contains(got, "argv:tv today --json") {
		t.Fatalf("BuildContent() = %q, want command output", got)
	}
}

func TestSkillBuildContentAllowsLiteralArgvShellCharacters(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "cyeam"), "#!/bin/sh\nprintf 'argv:%s\\n' \"$*\"\n")
	skill := testExecSkill(t, "cyeam-cli", "Use cyeam.\n")
	build := newSkillContentBuilder(skillExecConfig{
		GlobalBinDirs:  []string{binDir},
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	})

	got, err := build(context.Background(), skill, `{"skill":"cyeam-cli","argv":["cyeam","ask","https://example.test/live?from=1&to=$HOME;literal"]}`)
	if err != nil {
		t.Fatalf("BuildContent() error = %v", err)
	}
	if !strings.Contains(got, "argv:ask https://example.test/live?from=1&to=$HOME;literal") {
		t.Fatalf("BuildContent() = %q, want literal argv characters passed without shell parsing", got)
	}
}

func TestSkillBuildContentExecutesSkillLocalBinary(t *testing.T) {
	skill := testExecSkill(t, "local-cli", "Use local tool.\n")
	if err := os.MkdirAll(filepath.Join(skill.BaseDirectory, "bin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	writeExecutable(t, filepath.Join(skill.BaseDirectory, "bin", "local-tool"), "#!/bin/sh\nprintf 'local:%s\\n' \"$*\"\n")
	build := newSkillContentBuilder(skillExecConfig{
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	})

	got, err := build(context.Background(), skill, `{"skill":"local-cli","cmd":"local-tool run --flag value"}`)
	if err != nil {
		t.Fatalf("BuildContent() error = %v", err)
	}
	if !strings.Contains(got, "local:run --flag value") {
		t.Fatalf("BuildContent() = %q, want local command output", got)
	}
}

func TestSkillBuildContentRejectsShellOperators(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "cyeam"), "#!/bin/sh\nprintf 'bad\\n'\n")
	skill := testExecSkill(t, "cyeam-cli", "Use cyeam.\n")
	build := newSkillContentBuilder(skillExecConfig{
		GlobalBinDirs:  []string{binDir},
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	})

	_, err := build(context.Background(), skill, `{"skill":"cyeam-cli","cmd":"cyeam tv today --json && echo bad"}`)
	if err == nil {
		t.Fatal("BuildContent() error = nil, want shell operator rejection")
	}
}

func TestSkillBuildContentRejectsGlobalBinaryOutsideAllowedDirs(t *testing.T) {
	skill := testExecSkill(t, "unsafe-cli", "Use unsafe tool.\n")
	build := newSkillContentBuilder(skillExecConfig{
		GlobalBinDirs:  []string{t.TempDir()},
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	})

	_, err := build(context.Background(), skill, `{"skill":"unsafe-cli","argv":["/bin/echo","hello"]}`)
	if err == nil {
		t.Fatal("BuildContent() error = nil, want outside path rejection")
	}
}

func testExecSkill(t *testing.T, name, content string) einoskill.Skill {
	t.Helper()
	dir := t.TempDir()
	return einoskill.Skill{
		FrontMatter:   einoskill.FrontMatter{Name: name, Description: "test skill"},
		Content:       content,
		BaseDirectory: dir,
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}
