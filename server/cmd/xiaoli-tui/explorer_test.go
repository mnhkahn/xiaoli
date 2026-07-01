package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestBuildTreeSortsDirectoriesBeforeFiles(t *testing.T) {
	root := buildTree([]string{
		"README.md",
		"server/cmd/main.go",
		"server/go.mod",
	})
	children := root.sortedChildren()
	if len(children) != 2 {
		t.Fatalf("children len = %d, want 2", len(children))
	}
	if children[0].name != "server" || !children[0].dir {
		t.Fatalf("first child = %#v, want server directory", children[0])
	}
	if children[1].name != "README.md" || children[1].dir {
		t.Fatalf("second child = %#v, want README.md file", children[1])
	}
}

func TestTreeExplorerRendersPreview(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := newTreeExplorer(dir, 100, 30)
	got := ex.View()
	plain := stripTestANSI(got)
	if !strings.Contains(plain, "Project Tree") || !strings.Contains(plain, "package main") {
		t.Fatalf("tree explorer view missing tree or preview:\n%s", got)
	}
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Fatalf("line %d width = %d, want <= 100: %q", i, w, line)
		}
	}
}

var ansiTestRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripTestANSI(s string) string {
	return ansiTestRe.ReplaceAllString(s, "")
}

func TestTreeExplorerCopiesRawContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := newTreeExplorer(dir, 100, 30)
	if got := ex.copyText(); got != "package main\n" {
		t.Fatalf("copyText() = %q, want raw content", got)
	}
}

func TestOpenExplorerCommand(t *testing.T) {
	dir := t.TempDir()
	m := model{cwd: dir, width: 100, height: 30}
	if got := m.openExplorerCommand("/tree"); got == nil || got.mode != explorerTree {
		t.Fatalf("openExplorerCommand(/tree) = %#v, want tree explorer", got)
	}
	if got := m.openExplorerCommand("/diff"); got == nil || got.mode != explorerDiff {
		t.Fatalf("openExplorerCommand(/diff) = %#v, want diff explorer", got)
	}
	if got := m.openExplorerCommand("/sessions"); got != nil {
		t.Fatalf("openExplorerCommand(/sessions) = %#v, want nil", got)
	}
}

func TestParseChangedFileStatusMarksStaged(t *testing.T) {
	entry, ok := parseChangedFileStatus("M  staged.go")
	if !ok || entry.Path != "staged.go" || !entry.Staged {
		t.Fatalf("parseChangedFileStatus(staged) = %#v, %v", entry, ok)
	}
	entry, ok = parseChangedFileStatus(" M unstaged.go")
	if !ok || entry.Path != "unstaged.go" || entry.Staged {
		t.Fatalf("parseChangedFileStatus(unstaged) = %#v, %v", entry, ok)
	}
	entry, ok = parseChangedFileStatus("?? new.go")
	if !ok || entry.Path != "new.go" || entry.Staged {
		t.Fatalf("parseChangedFileStatus(untracked) = %#v, %v", entry, ok)
	}
}

func TestStageToggleArgs(t *testing.T) {
	add, args := stageToggleArgs(explorerEntry{Path: "a.go", Status: " M"})
	if !add || strings.Join(args, " ") != "add -- a.go" {
		t.Fatalf("stageToggleArgs unstaged = %v %#v", add, args)
	}
	add, args = stageToggleArgs(explorerEntry{Path: "a.go", Status: "M ", Staged: true})
	if add || strings.Join(args, " ") != "restore --staged -- a.go" {
		t.Fatalf("stageToggleArgs staged = %v %#v", add, args)
	}
}
