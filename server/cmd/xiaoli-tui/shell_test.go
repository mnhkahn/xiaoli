package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellModeDetection(t *testing.T) {
	if !isShellInput("!git status") {
		t.Fatalf("isShellInput(!git status) = false, want true")
	}
	if isShellInput(" !git status") {
		t.Fatalf("isShellInput(leading space) = true, want false")
	}
	if isShellInput("/diff") {
		t.Fatalf("isShellInput(/diff) = true, want false")
	}
}

func TestShellCompletionsIncludeGitSubcommands(t *testing.T) {
	items := shellCompletions("!git s", t.TempDir(), []string{"git", "go"})
	var names []string
	for _, item := range items {
		names = append(names, item.Name)
	}
	got := strings.Join(names, ",")
	if !strings.Contains(got, "git status") || !strings.Contains(got, "git show") {
		t.Fatalf("shellCompletions(!git s) = %q, want git status and git show", got)
	}
}

func TestShellCompletionsIncludePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "server"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	items := shellCompletions("!cat se", dir, nil)
	var names []string
	for _, item := range items {
		names = append(names, item.Name)
	}
	got := strings.Join(names, ",")
	if !strings.Contains(got, "cat server/") || !strings.Contains(got, "cat settings.json") {
		t.Fatalf("shellCompletions(!cat se) = %q, want matching paths", got)
	}
}

func TestApplyShellCompletionReplacesCurrentToken(t *testing.T) {
	got := applyShellCompletion("!cat se", "cat server/")
	if got != "!cat server/" {
		t.Fatalf("applyShellCompletion() = %q", got)
	}
}

func TestShellCommandResultTranscript(t *testing.T) {
	msg := shellDoneMsg{
		command: "printf hi",
		output:  "hi",
	}
	item := msg.transcriptItem()
	if item.role != "shell" || !strings.Contains(item.text, "$ printf hi") || !strings.Contains(item.text, "hi") {
		t.Fatalf("transcriptItem() = %#v", item)
	}
}
