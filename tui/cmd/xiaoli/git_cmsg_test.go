package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareGitCmsgDiffKeepsExistingStagedSet(t *testing.T) {
	if _, err := runGitCombined(t.TempDir(), "version"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	mustRunGit(t, dir, "init")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unstaged.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, dir, "add", ".")
	mustRunGit(t, dir, "commit", "-m", "chore: init")

	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unstaged.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, dir, "add", "staged.txt")

	_, files, _, err := prepareGitCmsgDiff(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(files) != "staged.txt" {
		t.Fatalf("files = %q, want only staged.txt", files)
	}
	after, err := runGitCombined(dir, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(after) != "staged.txt" {
		t.Fatalf("cached files after prepare = %q, want only staged.txt", after)
	}
}

func TestRunGitCombinedDisplaysChinesePaths(t *testing.T) {
	if _, err := runGitCombined(t.TempDir(), "version"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	mustRunGit(t, dir, "init")
	mustRunGit(t, dir, "config", "core.quotepath", "true")

	path := filepath.Join(dir, "docs", "古诗文.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# 古诗文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, dir, "add", ".")

	stat, err := runGitCombined(dir, "diff", "--cached", "--stat")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stat, "古诗文.md") {
		t.Fatalf("git stat = %q, want Chinese path", stat)
	}
	if strings.Contains(stat, `\345`) || strings.Contains(stat, `\346`) || strings.Contains(stat, `\347`) {
		t.Fatalf("git stat = %q, should not contain octal-escaped Chinese path", stat)
	}
}

func TestSanitizeCommitMessagePreservesBody(t *testing.T) {
	input := "```text\nfeat(agent): 放宽模型调用超时\n\n- 将 LLM HTTP 超时调整为 240 秒\n- 将 A2A 外层超时调整为 600 秒\n```\n"

	got := sanitizeCommitMessage(input)

	want := "feat(agent): 放宽模型调用超时\n\n- 将 LLM HTTP 超时调整为 240 秒\n- 将 A2A 外层超时调整为 600 秒"
	if got != want {
		t.Fatalf("sanitizeCommitMessage() = %q, want %q", got, want)
	}
}

func TestGitCommitMessagePromptRequiresStructuredBody(t *testing.T) {
	for _, want := range []string{"第一行是 type(scope): 简短中文描述", "随后空一行", "`- ` 开头的列表", "至少给出一条列表项"} {
		if !strings.Contains(gitCommitMessageSystemPrompt, want) {
			t.Fatalf("gitCommitMessageSystemPrompt = %q, want %q", gitCommitMessageSystemPrompt, want)
		}
	}
}

func TestSplitCommitMessageArgsSplitsSubjectAndBodyParagraphs(t *testing.T) {
	message := "feat(agent): 放宽模型调用超时\n\n- 将 LLM HTTP 超时调整为 240 秒\n- 将 A2A 外层超时调整为 600 秒\n\nCo-authored-by: ark-code-latest (ByteDance ARK) <noreply@volcesengine.com>"

	got := splitCommitMessageArgs(message)

	want := []string{
		"feat(agent): 放宽模型调用超时",
		"- 将 LLM HTTP 超时调整为 240 秒\n- 将 A2A 外层超时调整为 600 秒",
		"Co-authored-by: ark-code-latest (ByteDance ARK) <noreply@volcesengine.com>",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("splitCommitMessageArgs() = %#v, want %#v", got, want)
	}
}

func TestGitCommitMessageArgsAddsMForEachParagraph(t *testing.T) {
	message := "feat(tui): 优化复制体验\n\n- 调整状态栏\n- 新增项目切换\n\nCo-authored-by: ark-code-latest (ByteDance ARK) <noreply@volcesengine.com>"

	got := gitCommitMessageArgs(message)

	want := []string{
		"commit",
		"-m", "feat(tui): 优化复制体验",
		"-m", "- 调整状态栏\n- 新增项目切换",
		"-m", "Co-authored-by: ark-code-latest (ByteDance ARK) <noreply@volcesengine.com>",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("gitCommitMessageArgs() = %#v, want %#v", got, want)
	}
}

func TestFormatGitCmsgCommitErrorIncludesGitOutput(t *testing.T) {
	got := formatGitCmsgCommitError(context.Canceled, "error: pathspec 'body' did not match any file(s) known to git\n")

	if !strings.Contains(got, "context canceled") || !strings.Contains(got, "pathspec") {
		t.Fatalf("formatGitCmsgCommitError() = %q, want err and git output", got)
	}
}

func TestFormatGitCmsgQuestionShowsNumstatOnce(t *testing.T) {
	got := formatGitCmsgQuestion(gitCmsgPrepareMsg{
		message: "feat(tui): 优化提交统计",
		files:   "a.go\nb.go\n",
		stat:    "10\t0\ta.go\n2\t3\tb.go\n",
	}, 80)
	plain := ansiEscapeRE.ReplaceAllString(got, "")
	if strings.Contains(plain, "文件：") || strings.Contains(plain, "|") {
		t.Fatalf("formatGitCmsgQuestion() = %q, want no duplicate file section or pipe", plain)
	}
	for _, want := range []string{"a.go", "+10", "b.go", "+2", "-3", "2 个文件", "+12"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("formatGitCmsgQuestion() = %q, want %q", plain, want)
		}
	}
	if strings.Contains(plain, "-0") || strings.Contains(plain, "+0") {
		t.Fatalf("formatGitCmsgQuestion() = %q, want zero deltas hidden", plain)
	}
}

func TestFormatGitCommitPreviewUsesRequestedLayout(t *testing.T) {
	got := formatGitCommitPreview("feat(kiosk): 修复授权\n\n- ignored", "120\t16\tapp/main.go\n2\t0\tapp/test.go")
	for _, want := range []string{
		"feat(kiosk): 修复授权",
		"2 files changed, 122 insertions(+), 16 deletions(-)",
		" app/main.go | 136 ",
		" app/test.go | 2 ++",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatGitCommitPreview() = %q, want %q", got, want)
		}
	}
}

func TestPendingQuestionDisplayPreservesCommitStatLayout(t *testing.T) {
	question := formatGitCmsgQuestion(gitCmsgPrepareMsg{
		message: "feat(tui): 优化提交统计",
		stat:    "10\t0\tinternal/agent/localconfig/config.go\n2\t3\ttui/cmd/xiaoli/main.go\n",
	}, 80)
	got := pendingQuestionDisplay(question)
	if got != question {
		t.Fatalf("pendingQuestionDisplay() changed commit layout:\n%s", got)
	}
	if !strings.Contains(got, "internal/agent/localconfig/config.go") || !strings.Contains(got, "+10") {
		t.Fatalf("pendingQuestionDisplay() = %q, want file and delta on preserved rows", got)
	}
}

func mustRunGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	if out, err := runGitCombined(cwd, args...); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
