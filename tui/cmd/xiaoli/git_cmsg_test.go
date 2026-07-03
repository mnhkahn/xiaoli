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

func TestSanitizeCommitMessagePreservesBody(t *testing.T) {
	input := "```text\nfeat(agent): 放宽模型调用超时\n\n1. 将 LLM HTTP 超时调整为 240 秒\n2. 将 A2A 外层超时调整为 600 秒\n```\n"

	got := sanitizeCommitMessage(input)

	want := "feat(agent): 放宽模型调用超时\n\n1. 将 LLM HTTP 超时调整为 240 秒\n2. 将 A2A 外层超时调整为 600 秒"
	if got != want {
		t.Fatalf("sanitizeCommitMessage() = %q, want %q", got, want)
	}
}

func TestSplitCommitMessageArgsSplitsSubjectAndBodyParagraphs(t *testing.T) {
	message := "feat(agent): 放宽模型调用超时\n\n1. 将 LLM HTTP 超时调整为 240 秒\n2. 将 A2A 外层超时调整为 600 秒\n\nCo-authored-by: ark-code-latest (ByteDance ARK) <noreply@volcesengine.com>"

	got := splitCommitMessageArgs(message)

	want := []string{
		"feat(agent): 放宽模型调用超时",
		"1. 将 LLM HTTP 超时调整为 240 秒\n2. 将 A2A 外层超时调整为 600 秒",
		"Co-authored-by: ark-code-latest (ByteDance ARK) <noreply@volcesengine.com>",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("splitCommitMessageArgs() = %#v, want %#v", got, want)
	}
}

func TestGitCommitMessageArgsAddsMForEachParagraph(t *testing.T) {
	message := "feat(tui): 优化复制体验\n\n1. 调整状态栏\n2. 新增项目切换\n\nCo-authored-by: ark-code-latest (ByteDance ARK) <noreply@volcesengine.com>"

	got := gitCommitMessageArgs(message)

	want := []string{
		"commit",
		"-m", "feat(tui): 优化复制体验",
		"-m", "1. 调整状态栏\n2. 新增项目切换",
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

func mustRunGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	if out, err := runGitCombined(cwd, args...); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
