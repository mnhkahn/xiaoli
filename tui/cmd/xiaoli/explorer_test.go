package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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
	if !strings.Contains(plain, "1 │ package main") {
		t.Fatalf("tree explorer preview missing line number:\n%s", plain)
	}
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Fatalf("line %d width = %d, want <= 100: %q", i, w, line)
		}
	}
}

func TestExplorerFitsTerminalHeightAndKeepsPaneHeaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := newTreeExplorer(dir, 100, 10)
	if view := ex.View(); lipgloss.Height(view) != 9 {
		t.Fatalf("tree explorer height = %d, want 9:\n%s", lipgloss.Height(view), stripTestANSI(view))
	}

	diff := &tuiExplorer{
		mode:        explorerDiff,
		width:       100,
		height:      10,
		leftWidth:   explorerLeftWidth(100),
		entries:     []explorerEntry{{Path: "controllers/homework_controller.go", Status: " M"}},
		previewPath: "controllers/homework_controller.go",
		preview:     strings.Repeat("diff line\n", 40),
	}
	plain := stripTestANSI(diff.View())
	if view := diff.View(); lipgloss.Height(view) != 9 {
		t.Fatalf("diff explorer height = %d, want 9:\n%s", lipgloss.Height(view), stripTestANSI(view))
	}
	if !strings.Contains(plain, "Changed Files") || !strings.Contains(plain, "Diff · controllers/homework_controller.go") {
		t.Fatalf("diff explorer omitted persistent headers:\n%s", plain)
	}
}

func TestExplorerNeverExceedsShortTerminalHeight(t *testing.T) {
	ex := &tuiExplorer{
		mode:      explorerDiff,
		width:     100,
		height:    7,
		leftWidth: explorerLeftWidth(100),
		entries:   []explorerEntry{{Path: "main.go", Status: " M"}},
		preview:   strings.Repeat("diff line\n", 20),
	}

	view := ex.View()
	if got, want := lipgloss.Height(view), 6; got != want {
		t.Fatalf("short explorer height = %d, want %d:\n%s", got, want, stripTestANSI(view))
	}
	plain := stripTestANSI(view)
	if !strings.Contains(plain, "Changed Files") {
		t.Fatalf("short explorer omitted left header:\n%s", plain)
	}
}

func TestDiffExplorerLeavesTerminalLastColumnUnused(t *testing.T) {
	ex := &tuiExplorer{
		mode:        explorerDiff,
		width:       138,
		height:      48,
		leftWidth:   explorerLeftWidth(137),
		entries:     []explorerEntry{{Path: "tui/cmd/xiaoli/explorer.go", Status: " M"}},
		previewPath: "tui/cmd/xiaoli/explorer.go",
		preview:     strings.Repeat("diff line\n", 60),
	}

	for _, line := range strings.Split(ex.View(), "\n") {
		if got, want := lipgloss.Width(line), 137; got > want {
			t.Fatalf("diff line width = %d, want <= %d: %q", got, want, stripTestANSI(line))
		}
	}
	if got, want := lipgloss.Height(ex.View()), 47; got != want {
		t.Fatalf("diff view height = %d, want %d to preserve Otty safety row", got, want)
	}
}

func TestDiffExplorerKeepsHeadersAcrossFileSelection(t *testing.T) {
	ex := &tuiExplorer{
		mode:      explorerDiff,
		width:     138,
		height:    48,
		leftWidth: explorerLeftWidth(137),
		entries: []explorerEntry{
			{Path: "tui/cmd/xiaoli/explorer.go", Status: " M"},
			{Path: "tui/cmd/xiaoli/explorer_test.go", Status: " M"},
			{Path: "tui/cmd/xiaoli/main.go", Status: " M"},
			{Path: "tui/cmd/xiaoli/main_test.go", Status: " M"},
		},
		preview: strings.Repeat("diff line\n", 80),
	}

	for i := range ex.entries {
		ex.selected = i
		ex.previewPath = ex.entries[i].Path
		ex.ensureSelectionVisible()
		view := ex.View()
		plain := stripTestANSI(view)
		if got, want := lipgloss.Height(view), 47; got > want {
			t.Fatalf("selection %d view height = %d, want <= %d", i, got, want)
		}
		if !strings.Contains(plain, "Xiaoli /diff") || !strings.Contains(plain, "Changed Files") {
			t.Fatalf("selection %d dropped fixed headers:\n%s", i, plain)
		}
	}
}

func TestLimitExplorerViewHeightDropsOnlyBottomLines(t *testing.T) {
	got := limitExplorerViewHeight("header\nbody\nhelp\nfooter", 2)
	if want := "header\nbody"; got != want {
		t.Fatalf("limitExplorerViewHeight() = %q, want %q", got, want)
	}
}

func TestDiffExplorerScrollsOnlyFileRows(t *testing.T) {
	entries := make([]explorerEntry, 40)
	for i := range entries {
		entries[i] = explorerEntry{Path: "internal/very-long-package-name/file-" + strings.Repeat("x", 32) + ".go", Status: " M"}
	}
	ex := &tuiExplorer{
		mode:      explorerDiff,
		width:     80,
		height:    12,
		leftWidth: explorerLeftWidth(80),
		entries:   entries,
	}

	for range entries {
		ex.moveSelection(1)
	}
	view := ex.renderLeft(32, ex.listHeight()+1)
	plain := stripTestANSI(view)
	lines := strings.Split(plain, "\n")
	if !strings.Contains(lines[0], "Changed Files") {
		t.Fatalf("header scrolled out of pane:\n%s", plain)
	}
	if !strings.Contains(plain, "█") || !strings.Contains(plain, "│") {
		t.Fatalf("long file list omitted its internal scrollbar:\n%s", plain)
	}
	if got, want := lipgloss.Height(view), ex.listHeight()+1; got != want {
		t.Fatalf("left pane height = %d, want %d:\n%s", got, want, plain)
	}
}

func TestTreeExplorerHighlightsSelectedRow(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := newTreeExplorer(dir, 100, 30)
	got := ex.renderLeft(40, 8)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("renderLeft() = %q, want ANSI highlight", got)
	}
	if plain := stripTestANSI(got); !strings.Contains(plain, "›") || !strings.Contains(plain, "main.go") {
		t.Fatalf("plain renderLeft() = %q, want selected marker and file", plain)
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

func TestTreeExplorerOpensEditorAndSavesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := newTreeExplorer(dir, 100, 30)
	if ex.editor == nil {
		t.Fatalf("editor is nil, want selected file loaded")
	}
	ex.focusRight = true
	ex.editor.handleKey(keyRunes("i"))
	ex.editor.handleKey(keyRunes("// "))
	ex.handleEditorCommand("w")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "// package main") {
		t.Fatalf("saved file = %q", string(data))
	}
}

func TestTreeExplorerFocusesEditorWithL(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := newTreeExplorer(dir, 100, 30)
	if ex.focusRight {
		t.Fatalf("focusRight = true, want tree focus")
	}
	ex.handleKey(keyRunes("l"))
	if !ex.focusRight {
		t.Fatalf("focusRight = false, want editor focus")
	}
	ex.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if ex.focusRight {
		t.Fatalf("focusRight = true after tab, want tree focus")
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

func TestDiffExplorerHandlesSpaceForStageToggle(t *testing.T) {
	ex := &tuiExplorer{mode: explorerDiff, entries: []explorerEntry{{Path: "a.go", Status: " M", IsDir: true}}, selected: 0}
	_, _, handled := ex.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	if !handled {
		t.Fatalf("diff explorer space handled = false, want true")
	}
	tree := &tuiExplorer{mode: explorerTree, entries: []explorerEntry{{Path: "a.go", Label: "a.go"}}, selected: 0}
	_, _, handled = tree.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	if handled {
		t.Fatalf("tree explorer space handled = true, want false")
	}
}

func TestDiffExplorerKeyboardSwitchesToPreviewAndScrolls(t *testing.T) {
	ex := &tuiExplorer{
		mode:       explorerDiff,
		width:      100,
		height:     16,
		entries:    []explorerEntry{{Path: "a.go", Status: " M"}},
		selected:   0,
		preview:    strings.Repeat("diff line\n", 40),
		previewRaw: strings.Repeat("diff line\n", 40),
	}

	_, _, handled := ex.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	if !handled || !ex.focusRight {
		t.Fatalf("right = handled:%v focusRight:%v, want preview focus", handled, ex.focusRight)
	}
	_, _, handled = ex.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if !handled || ex.rightScroll != 1 || ex.selected != 0 {
		t.Fatalf("down = handled:%v scroll:%d selected:%d, want scroll only", handled, ex.rightScroll, ex.selected)
	}
	_, _, handled = ex.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if !handled || ex.focusRight {
		t.Fatalf("left = handled:%v focusRight:%v, want file list focus", handled, ex.focusRight)
	}
}

func TestDiffExplorerPreviewDragSelectsRightPane(t *testing.T) {
	ex := &tuiExplorer{
		mode:       explorerDiff,
		width:      100,
		height:     30,
		leftWidth:  explorerLeftWidth(100),
		preview:    "diff --git a/a.go b/a.go\n+added line\n-removed line",
		previewRaw: "diff --git a/a.go b/a.go\n+added line\n-removed line",
	}
	press := tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: ex.leftWidth + 2, Y: explorerPreviewMouseTop + 1}
	motion := tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion, X: ex.leftWidth + 8, Y: explorerPreviewMouseTop + 1}

	if !ex.handleMouse(press) || !ex.selection.dragging {
		t.Fatalf("press did not start right pane selection: %#v", ex.selection)
	}
	if !ex.handleMouse(motion) || !ex.selection.active {
		t.Fatalf("motion did not activate right pane selection: %#v", ex.selection)
	}
	if ex.selection.anchor.y != 1 || ex.selection.focus.y != 1 {
		t.Fatalf("selection y = %d/%d, want preview line 1", ex.selection.anchor.y, ex.selection.focus.y)
	}
}

func TestDiffExplorerRenderRightShowsSelectionOverlay(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	ex := &tuiExplorer{
		mode:       explorerDiff,
		width:      100,
		height:     30,
		leftWidth:  explorerLeftWidth(100),
		preview:    "diff --git a/a.go b/a.go\n+added line\n-removed line",
		previewRaw: "diff --git a/a.go b/a.go\n+added line\n-removed line",
		selection: transcriptSelection{
			active: true,
			anchor: selectionPoint{
				x: 0,
				y: 1,
			},
			focus: selectionPoint{
				x: 5,
				y: 1,
			},
		},
	}

	got := ex.renderRight(60, 10)

	if !strings.Contains(got, selectionStartSeq) || !strings.Contains(got, selectionEndSeq) {
		t.Fatalf("renderRight() = %q, want selection overlay", got)
	}
	if plain := stripTestANSI(got); !strings.Contains(plain, "+added line") {
		t.Fatalf("plain renderRight() = %q, want diff content unchanged", plain)
	}
}

func TestDiffExplorerShowsStagedOnlyDiff(t *testing.T) {
	dir := t.TempDir()
	if _, err := runGit(dir, "init"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte("FROM debian\nRUN apt-get install -y git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "add", "Dockerfile"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("FROM debian\nRUN apt-get install -y git poppler-utils antiword\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "add", "Dockerfile"); err != nil {
		t.Fatal(err)
	}

	entry := explorerEntry{Path: "Dockerfile", Status: "M", Staged: true}
	diff, err := fileDiff(dir, entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "poppler-utils antiword") {
		t.Fatalf("fileDiff(staged-only) = %q, want cached diff", diff)
	}

	ex := &tuiExplorer{
		mode:        explorerDiff,
		cwd:         dir,
		width:       120,
		height:      30,
		leftWidth:   explorerLeftWidth(120),
		entries:     []explorerEntry{entry},
		selected:    0,
		preview:     diff,
		previewRaw:  diff,
		previewPath: entry.Path,
	}
	if plain := stripTestANSI(ex.renderRight(70, 20)); !strings.Contains(plain, "Dockerfile") || !strings.Contains(plain, "poppler-utils antiword") {
		t.Fatalf("renderRight(staged-only) = %q, want cached diff", plain)
	}
}

func TestDiffExplorerUsesRepositoryRootFromSubdirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server", "internal", "admin", "a2a_adapter.go")
	tuiPath := filepath.Join(dir, "tui", "cmd", "xiaoli", "explorer.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tuiPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package admin\n\nconst adapter = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tuiPath, []byte("package main\n\nconst version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "add", "server/internal/admin/a2a_adapter.go", "tui/cmd/xiaoli/explorer.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "config", "status.relativePaths", "false"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package admin\n\nconst adapter = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tuiPath, []byte("package main\n\nconst version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ex := newDiffExplorer(filepath.Join(dir, "server"), 100, 30)
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ex.cwd != root {
		t.Fatalf("explorer cwd = %q, want repository root %q", ex.cwd, root)
	}
	if len(ex.entries) != 2 || ex.entries[0].Path != "server/internal/admin/a2a_adapter.go" || ex.entries[1].Path != "tui/cmd/xiaoli/explorer.go" {
		t.Fatalf("diff entries = %#v, want all repository changes", ex.entries)
	}
	if !strings.Contains(ex.preview, "const adapter = 2") {
		t.Fatalf("diff preview = %q, want modified file diff", ex.preview)
	}
}

func TestDiffExplorerMouseClickSelectsWithoutTogglingStage(t *testing.T) {
	ex := &tuiExplorer{
		mode:      explorerDiff,
		width:     100,
		height:    30,
		leftWidth: explorerLeftWidth(100),
		entries: []explorerEntry{
			{Path: "a.go", Status: " M"},
			{Path: "b.go", Status: "M", Staged: true},
		},
		selected: 0,
		preview:  "old preview",
	}

	if !ex.handleMouse(tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 2, Y: 4}) {
		t.Fatalf("mouse click was not handled")
	}
	if ex.selected != 1 {
		t.Fatalf("selected = %d, want 1", ex.selected)
	}
	if !ex.entries[1].Staged {
		t.Fatalf("click toggled staged state")
	}
	if !strings.Contains(ex.preview, "No diff for b.go") {
		t.Fatalf("preview = %q, want selected file preview", ex.preview)
	}
}

func TestExplorerRefreshPreviewClearsSelection(t *testing.T) {
	ex := &tuiExplorer{
		mode:      explorerDiff,
		cwd:       t.TempDir(),
		entries:   []explorerEntry{{Path: "missing.go", Status: " M"}},
		selected:  0,
		selection: transcriptSelection{active: true, anchor: selectionPoint{x: 0, y: 0}, focus: selectionPoint{x: 3, y: 0}},
	}

	ex.refreshPreview()

	if ex.selection.active || ex.selection.dragging {
		t.Fatalf("selection = %#v, want cleared", ex.selection)
	}
}

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
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
