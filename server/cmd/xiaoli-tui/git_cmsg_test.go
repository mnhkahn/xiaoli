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

func mustRunGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	if out, err := runGitCombined(cwd, args...); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
