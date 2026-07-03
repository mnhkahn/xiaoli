package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestLayoutUsesBottomStatusBar(t *testing.T) {
	mainW, sideW, bodyH, promptW, statusH := layoutSizes(120, 30)
	if sideW != 0 {
		t.Fatalf("sideW = %d, want 0", sideW)
	}
	if mainW != 120-boxStyle.GetHorizontalFrameSize() {
		t.Fatalf("mainW = %d, want full-width transcript", mainW)
	}
	if bodyH != 23 || promptW != 120-boxStyle.GetHorizontalFrameSize() || statusH != 2 {
		t.Fatalf("layoutSizes() = main:%d side:%d body:%d prompt:%d status:%d", mainW, sideW, bodyH, promptW, statusH)
	}
}

func TestStatusBarShowsTwoRowsWithStateAndActions(t *testing.T) {
	got := stripTestANSI(renderStatusBar(model{cwd: "/tmp/work/repo", gitStatus: "main ↑2", status: "idle"}, 80))
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("status lines = %d, want 2: %q", len(lines), got)
	}
	for _, want := range []string{"idle", "repo", "main ↑2", "⌃S sync", "⌃T tree", "⌃K diff"} {
		if !strings.Contains(got, want) {
			t.Fatalf("renderStatusBar() = %q, want %q", got, want)
		}
	}
	if strings.Contains(lines[0], "⌃S sync") || !strings.Contains(lines[1], "⌃S sync") {
		t.Fatalf("renderStatusBar() lines = %#v, want actions on second row", lines)
	}
}

func TestParseGitSyncSummary(t *testing.T) {
	got := parseGitSyncSummary("## main...origin/main [ahead 2, behind 1]\n M a.go\n?? b.go\n")
	if got != "main ↑2↓1 *2" {
		t.Fatalf("parseGitSyncSummary() = %q", got)
	}
	got = parseGitSyncSummary("## main...origin/main\n")
	if got != "main ✓" {
		t.Fatalf("parseGitSyncSummary clean = %q", got)
	}
}

func TestParseGitSyncState(t *testing.T) {
	state := parseGitSyncState("## main...origin/main [ahead 2]\n")
	if state.Branch != "main" || state.Ahead != 2 || state.Behind != 0 || !state.Actionable() {
		t.Fatalf("parseGitSyncState(ahead) = %#v", state)
	}
	state = parseGitSyncState("## main...origin/main [behind 1]\n M a.go\n")
	if state.Branch != "main" || state.Ahead != 0 || state.Behind != 1 || state.Dirty != 1 {
		t.Fatalf("parseGitSyncState(behind) = %#v", state)
	}
}

func TestGitSyncAction(t *testing.T) {
	action, args := gitSyncAction(gitSyncState{Ahead: 2})
	if action != "push" || strings.Join(args, " ") != "push" {
		t.Fatalf("gitSyncAction(ahead) = %q %#v", action, args)
	}
	action, args = gitSyncAction(gitSyncState{Behind: 1})
	if action != "pull" || strings.Join(args, " ") != "pull --ff-only" {
		t.Fatalf("gitSyncAction(behind) = %q %#v", action, args)
	}
	action, args = gitSyncAction(gitSyncState{Ahead: 1, Behind: 1})
	if action != "pull" || strings.Join(args, " ") != "pull --rebase" {
		t.Fatalf("gitSyncAction(diverged) = %q %#v", action, args)
	}
}

func TestGitSyncButtonLabel(t *testing.T) {
	m := model{
		gitStatus: "main ↑1",
		gitSync:   gitSyncState{Branch: "main", Ahead: 1, Valid: true},
	}
	if got := stripTestANSI(gitSyncButtonLabel(m)); got != "main ↑1 [push]" {
		t.Fatalf("gitSyncButtonLabel actionable = %q", got)
	}
	m.gitSyncFeedback = gitSyncFeedback{Loading: true, Action: "push", Frame: 2}
	if got := gitSyncButtonLabel(m); !strings.Contains(got, "[syncing ") {
		t.Fatalf("gitSyncButtonLabel loading = %q", got)
	}
	m.gitSyncFeedback = gitSyncFeedback{Result: "pushed"}
	if got := stripTestANSI(gitSyncButtonLabel(m)); got != "main ↑1 [pushed]" {
		t.Fatalf("gitSyncButtonLabel pushed = %q", got)
	}
}

func TestGitSyncButtonLabelHighlightsChangingParts(t *testing.T) {
	m := model{
		gitStatus: "main ↑1",
		gitSync:   gitSyncState{Branch: "main", Ahead: 1, Dirty: 2, Valid: true},
	}
	got := gitSyncButtonLabel(m)
	if plain := stripTestANSI(got); plain != "main ↑1 *2 [push]" {
		t.Fatalf("plain gitSyncButtonLabel() = %q", plain)
	}
}

func TestGitSyncClickDisabledForCopyFriendlyMode(t *testing.T) {
	m := model{
		width:     120,
		height:    30,
		status:    "idle",
		gitSync:   gitSyncState{Branch: "main", Ahead: 1, Valid: true},
		gitStatus: "main ↑1",
	}
	mainW, _, bodyH, _, _ := layoutSizes(m.width, m.height)
	click := tea.MouseMsg{
		Type:   tea.MouseLeft,
		Action: tea.MouseActionPress,
		X:      mainW + boxStyle.GetHorizontalFrameSize() + 2,
		Y:      bodyH,
	}
	if m.handleGitSyncClick(click) {
		t.Fatalf("handleGitSyncClick() = true, want false")
	}
}

func TestStartGitSyncFeedbackOnlyUpdatesFooter(t *testing.T) {
	m := model{
		width:     120,
		height:    30,
		status:    "idle",
		gitSync:   gitSyncState{Branch: "main", Ahead: 1, Valid: true},
		gitStatus: "main ↑1",
	}
	if !m.startGitSyncFeedback() {
		t.Fatalf("startGitSyncFeedback() = false, want true")
	}
	if m.status != "idle" {
		t.Fatalf("status = %q, want idle", m.status)
	}
	if len(m.items) != 0 {
		t.Fatalf("items len = %d, want 0", len(m.items))
	}
	if !m.gitSyncFeedback.Loading {
		t.Fatalf("gitSyncFeedback.Loading = false, want true")
	}
}

func TestStatusBarShowsMouseFreeCopyHint(t *testing.T) {
	got := stripTestANSI(renderStatusBar(model{cwd: "/tmp/repo", gitStatus: "main ✓", copyMode: true, status: "copy mode"}, 80))
	if !strings.Contains(got, "copy mode") || !strings.Contains(got, "esc back") {
		t.Fatalf("renderStatusBar(copy mode) = %q, want copy mode hints", got)
	}
}

func TestCtrlKOpensDiffExplorer(t *testing.T) {
	input := textinput.New()
	m := model{input: input, cwd: t.TempDir(), width: 100, height: 30}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("Ctrl-K returned command, want nil")
	}
	if got.explorer == nil || got.explorer.mode != explorerDiff {
		t.Fatalf("Ctrl-K explorer = %#v, want diff explorer", got.explorer)
	}
	if got.input.Value() != "" {
		t.Fatalf("Ctrl-K input = %q, want empty", got.input.Value())
	}
}

func TestCtrlTOpensTreeExplorer(t *testing.T) {
	input := textinput.New()
	m := model{input: input, cwd: t.TempDir(), width: 100, height: 30}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("Ctrl-T returned command, want nil")
	}
	if got.explorer == nil || got.explorer.mode != explorerTree {
		t.Fatalf("Ctrl-T explorer = %#v, want tree explorer", got.explorer)
	}
}

func TestCtrlOTogglesCopyMode(t *testing.T) {
	input := textinput.New()
	m := model{input: input, mouseEnabled: false, status: "idle"}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("Ctrl-O returned command, want nil when mouse is already terminal-owned")
	}
	if !got.copyMode || got.mouseEnabled || got.status != "copy mode" {
		t.Fatalf("copy enter = copyMode:%v mouse:%v status:%q", got.copyMode, got.mouseEnabled, got.status)
	}
	next, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = next.(model)
	if cmd != nil {
		t.Fatalf("Esc returned command, want nil when leaving copy mode")
	}
	if got.copyMode || got.mouseEnabled || got.status != "idle" {
		t.Fatalf("copy exit = copyMode:%v mouse:%v status:%q", got.copyMode, got.mouseEnabled, got.status)
	}
}

func TestDoubleTabOpensWorkspacePicker(t *testing.T) {
	input := textinput.New()
	m := model{input: input, cwd: t.TempDir(), width: 100, height: 30, workspaceStatePath: filepath.Join(t.TempDir(), "workspaces.json")}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("first Tab returned command, want nil")
	}
	if got.workspacePicker != nil {
		t.Fatalf("first Tab opened workspace picker")
	}
	next, cmd = got.Update(tea.KeyMsg{Type: tea.KeyTab})
	got = next.(model)
	if cmd != nil {
		t.Fatalf("second Tab returned command, want nil")
	}
	if got.workspacePicker == nil {
		t.Fatalf("second Tab did not open workspace picker")
	}
}

func TestWorkspaceStoreUpsertsRecentProjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	first := workspaceItem{CWD: "/tmp/one", SessionID: "s1", Title: "one", LastOpened: time.Now().Add(-time.Hour)}
	second := workspaceItem{CWD: "/tmp/two", SessionID: "s2", Title: "two", LastOpened: time.Now()}
	if err := upsertWorkspace(path, first); err != nil {
		t.Fatalf("upsert first error = %v", err)
	}
	if err := upsertWorkspace(path, second); err != nil {
		t.Fatalf("upsert second error = %v", err)
	}
	items := loadWorkspaces(path)
	if len(items) != 2 {
		t.Fatalf("len(loadWorkspaces) = %d, want 2", len(items))
	}
	if items[0].CWD != "/tmp/two" || items[1].CWD != "/tmp/one" {
		t.Fatalf("workspace order = %#v, want newest first", items)
	}
	first.SessionID = "s3"
	first.LastOpened = time.Now().Add(time.Hour)
	if err := upsertWorkspace(path, first); err != nil {
		t.Fatalf("upsert existing error = %v", err)
	}
	items = loadWorkspaces(path)
	if len(items) != 2 || items[0].CWD != "/tmp/one" || items[0].SessionID != "s3" {
		t.Fatalf("updated workspaces = %#v", items)
	}
}

func TestSwitchWorkspaceRestoresSession(t *testing.T) {
	dir := t.TempDir()
	m := model{cwd: t.TempDir(), input: textinput.New(), workspaceStatePath: filepath.Join(t.TempDir(), "workspaces.json")}
	item := workspaceItem{CWD: dir, SessionID: "session-1", Title: "Project"}
	m.switchWorkspace(item)
	if m.cwd != dir || m.sessionID != "session-1" {
		t.Fatalf("switchWorkspace cwd/session = %q/%q", m.cwd, m.sessionID)
	}
	if len(m.items) == 0 || !strings.Contains(m.items[len(m.items)-1].text, "Switched to") {
		t.Fatalf("items = %#v, want switch notice", m.items)
	}
}

func TestEscClearsInputWithoutQuitting(t *testing.T) {
	input := textinput.New()
	input.SetValue("/commit ")
	m := model{input: input}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("Esc returned command, want nil")
	}
	if got.quitting {
		t.Fatalf("Esc set quitting=true, want false")
	}
	if got.input.Value() != "" {
		t.Fatalf("Esc input = %q, want empty", got.input.Value())
	}
}

func TestEscCancelsBusyWithoutQuitting(t *testing.T) {
	input := textinput.New()
	canceled := false
	m := model{input: input, busy: true, status: "running", activeCancel: func() { canceled = true }}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("Esc returned command, want nil")
	}
	if !canceled {
		t.Fatalf("Esc did not call active cancel")
	}
	if got.quitting || got.busy || got.status != "idle" {
		t.Fatalf("Esc model = quitting:%v busy:%v status:%q", got.quitting, got.busy, got.status)
	}
}

func TestInputHistoryNavigation(t *testing.T) {
	input := textinput.New()
	input.SetValue("draft")
	m := model{input: input}
	m.recordInputHistory("first")
	m.recordInputHistory("second")

	if !m.navigateInputHistory(-1) || m.input.Value() != "second" {
		t.Fatalf("first up input = %q", m.input.Value())
	}
	if !m.navigateInputHistory(-1) || m.input.Value() != "first" {
		t.Fatalf("second up input = %q", m.input.Value())
	}
	if m.navigateInputHistory(-1) {
		t.Fatalf("third up should not navigate")
	}
	if !m.navigateInputHistory(1) || m.input.Value() != "second" {
		t.Fatalf("first down input = %q", m.input.Value())
	}
	if !m.navigateInputHistory(1) || m.input.Value() != "draft" {
		t.Fatalf("second down input = %q", m.input.Value())
	}
}

func TestRenderScrollGutterAddsThumbForOverflow(t *testing.T) {
	lines := []string{"a", "b", "c"}
	got := renderScrollGutter(lines, 12, 2, 3, 0)
	if !strings.Contains(got, "█") {
		t.Fatalf("renderScrollGutter() = %q, want visible thumb", got)
	}
}

func TestRenderTranscriptNeverExceedsWidth(t *testing.T) {
	items := []transcriptItem{{
		role: "assistant",
		text: strings.Repeat("https://example.com/very-long-undivided-token-", 20),
	}, {
		role: "assistant",
		text: "下面按 **2026-06-30 今天 cyeam news 聚合内容** 做总结和趋势解读。\n\n" + strings.Repeat("Claude正式发布与AI新闻长段落", 20),
	}}
	width := 48
	got := renderTranscript(items, width, 12, 0)
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, w, width, line)
		}
	}
}

func TestRenderTranscriptContentNeverExceedsWidth(t *testing.T) {
	items := []transcriptItem{{
		role: "assistant",
		text: strings.Repeat("https://example.com/very-long-undivided-token-", 20),
	}, {
		role: "user",
		text: "明天北京什么天气？",
	}}
	width := 64
	got := renderTranscriptContent(items, width)
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, w, width, line)
		}
	}
}

func TestFitDisplayClampsAndPads(t *testing.T) {
	if got := fitDisplay("abc", 5); lipgloss.Width(got) != 5 {
		t.Fatalf("fitDisplay short width = %d, want 5 (%q)", lipgloss.Width(got), got)
	}
	got := fitDisplay(strings.Repeat("x", 100), 20)
	if lipgloss.Width(got) > 20 {
		t.Fatalf("fitDisplay long width = %d, want <= 20", lipgloss.Width(got))
	}
}

func TestTruncateDisplayFitsWidth(t *testing.T) {
	got := truncateDisplay("abcdefghijklmnopqrstuvwxyz", 10)
	if lipgloss.Width(got) > 10 {
		t.Fatalf("truncateDisplay width = %d, want <= 10 (%q)", lipgloss.Width(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncateDisplay() = %q, want ellipsis", got)
	}
}

func TestTruncateDisplayTinyWidth(t *testing.T) {
	for width := 1; width <= 3; width++ {
		got := truncateDisplay("abcdef", width)
		if lipgloss.Width(got) > width {
			t.Fatalf("truncateDisplay tiny width = %d, want <= %d (%q)", lipgloss.Width(got), width, got)
		}
	}
}

func TestLatestAssistantText(t *testing.T) {
	items := []transcriptItem{
		{role: "assistant", text: "first"},
		{role: "user", text: "copy?"},
		{role: "event", text: "run completed"},
		{role: "assistant", text: "second"},
		{role: "system", text: "ignored"},
	}
	if got := latestAssistantText(items); got != "second" {
		t.Fatalf("latestAssistantText() = %q, want second", got)
	}
}

func TestPendingOptionByInput(t *testing.T) {
	m := model{pendingOptions: []string{"允许", "拒绝"}}
	if got, ok := m.pendingOptionByInput("1"); !ok || got != "允许" {
		t.Fatalf("pendingOptionByInput(1) = %q, %v; want 允许, true", got, ok)
	}
	if got, ok := m.pendingOptionByInput("拒绝"); !ok || got != "拒绝" {
		t.Fatalf("pendingOptionByInput(拒绝) = %q, %v; want 拒绝, true", got, ok)
	}
	if _, ok := m.pendingOptionByInput("3"); ok {
		t.Fatalf("pendingOptionByInput(3) ok = true, want false")
	}
}

func TestAppendLocalSuggestionsIncludesCommit(t *testing.T) {
	items := appendLocalSuggestions("/co", nil)
	for _, item := range items {
		if item.Name == "commit" {
			return
		}
	}
	t.Fatalf("appendLocalSuggestions(/co) = %#v, want commit", items)
}

func TestAppendLocalSuggestionsIncludesCDAndVersion(t *testing.T) {
	items := appendLocalSuggestions("/v", nil)
	if !hasSuggestion(items, "version") {
		t.Fatalf("appendLocalSuggestions(/v) = %#v, want version", items)
	}
	items = appendLocalSuggestions("/c", nil)
	if !hasSuggestion(items, "cd") || !hasSuggestion(items, "commit") {
		t.Fatalf("appendLocalSuggestions(/c) = %#v, want cd and commit", items)
	}
}

func TestHandleLocalCDChangesCWD(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	m := model{cwd: root, gitStatus: "old", input: textinput.New()}
	if !m.handleLocalCommand("/cd child") {
		t.Fatalf("handleLocalCommand(/cd child) = false, want true")
	}
	if m.cwd != child {
		t.Fatalf("cwd = %q, want %q", m.cwd, child)
	}
	if len(m.items) == 0 || !strings.Contains(m.items[len(m.items)-1].text, child) {
		t.Fatalf("items = %#v, want cd confirmation", m.items)
	}
}

func TestHandleLocalCDRejectsFiles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := model{cwd: root, input: textinput.New()}
	if !m.handleLocalCommand("/cd file.txt") {
		t.Fatalf("handleLocalCommand(/cd file.txt) = false, want true")
	}
	if m.cwd != root {
		t.Fatalf("cwd = %q, want unchanged %q", m.cwd, root)
	}
	if len(m.items) == 0 || m.items[len(m.items)-1].role != "error" {
		t.Fatalf("items = %#v, want error item", m.items)
	}
}

func TestVersionInfoUsesBuildVersion(t *testing.T) {
	old := version
	version = "v1.2.3"
	defer func() { version = old }()
	got := versionInfo()
	if !strings.Contains(got, "v1.2.3") {
		t.Fatalf("versionInfo() = %q, want injected version", got)
	}
}

func TestWelcomeBannerIncludesVersionCommandsAndFullWidthLogo(t *testing.T) {
	old := version
	version = "v1.2.3"
	defer func() { version = old }()
	got := stripTestANSI(renderWelcomeBanner(model{cwd: "/tmp/repo", gitStatus: "main ✓", sessionID: "abc"}, 100))
	for _, want := range []string{"Xiaoli TUI", "v1.2.3", "Getting started", "/cd <path>", "Ctrl+S", "git sync"} {
		if !strings.Contains(got, want) {
			t.Fatalf("renderWelcomeBanner() = %q, want %q", got, want)
		}
	}
	for _, notWant := range []string{"model   ", "cwd     ", "session ", "What's new", "bottom status bar", ".com"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("renderWelcomeBanner() = %q, should not contain label %q", got, notWant)
		}
	}
	if !strings.Contains(got, "│") {
		t.Fatalf("renderWelcomeBanner() = %q, want compact split layout", got)
	}
}

func TestWelcomeCommandsCanRenderInMultipleColumns(t *testing.T) {
	got := stripTestANSI(renderWelcomeCommands(70))
	lines := strings.Split(got, "\n")
	if len(lines) < 4 {
		t.Fatalf("renderWelcomeCommands() lines = %#v, want multiple command rows", lines)
	}
	if !strings.Contains(got, "/cd <path>") || !strings.Contains(got, "Ctrl+S") {
		t.Fatalf("renderWelcomeCommands() = %q, want commands and shortcuts", got)
	}
	if !strings.Contains(lines[0], "/tree") {
		t.Fatalf("renderWelcomeCommands() first row = %q, want multiple columns", lines[0])
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("v1.2.3", "v1.2.4") >= 0 {
		t.Fatalf("compareVersions(v1.2.3, v1.2.4) >= 0")
	}
	if compareVersions("v1.10.0", "v1.2.9") <= 0 {
		t.Fatalf("compareVersions(v1.10.0, v1.2.9) <= 0")
	}
	if compareVersions("1.2.3", "v1.2.3") != 0 {
		t.Fatalf("compareVersions(1.2.3, v1.2.3) != 0")
	}
}

func TestLatestReleaseFromJSONExtractsNotes(t *testing.T) {
	got, err := latestReleaseFromJSON([]byte(`{
		"tag_name":"v0.10.0",
		"html_url":"https://github.com/mnhkahn/xiaoli/releases/tag/v0.10.0",
		"body":"## What's Changed\n- Add dynamic welcome notes\n- Improve TUI layout\n\nFull changelog: https://example.test"
	}`))
	if err != nil {
		t.Fatalf("latestReleaseFromJSON error = %v", err)
	}
	if got.Tag != "v0.10.0" || got.URL == "" {
		t.Fatalf("latestReleaseFromJSON() = %#v, want tag and URL", got)
	}
	if len(got.Notes) != 2 || got.Notes[0] != "Add dynamic welcome notes" || got.Notes[1] != "Improve TUI layout" {
		t.Fatalf("Notes = %#v, want extracted bullets", got.Notes)
	}
}

func TestUpdateInfoAvailableAndCommand(t *testing.T) {
	info := newUpdateInfo("v0.1.0", "v0.2.0", time.Now())
	if !info.Available() {
		t.Fatalf("Available() = false, want true")
	}
	if !strings.Contains(info.Command, "go install github.com/mnhkahn/xiaoli/tui/cmd/xiaoli@v0.2.0") {
		t.Fatalf("Command = %q, want go install latest tag", info.Command)
	}
}

func TestWelcomeBannerShowsUpdateHint(t *testing.T) {
	old := version
	version = "v0.1.0"
	defer func() { version = old }()
	m := model{
		cwd:        "/tmp/repo",
		updateInfo: newUpdateInfo("v0.1.0", "v0.2.0", time.Now()),
	}
	got := stripTestANSI(renderWelcomeBanner(m, 100))
	if !strings.Contains(got, "update  v0.1.0 -> v0.2.0") || !strings.Contains(got, "/upgrade") {
		t.Fatalf("renderWelcomeBanner(update) = %q, want update hint", got)
	}
}

func TestWelcomeBannerShowsReleaseNotesWhenDynamic(t *testing.T) {
	old := version
	version = "v0.1.0"
	defer func() { version = old }()
	m := model{
		cwd: "/tmp/repo",
		updateInfo: updateInfo{
			Current:   "v0.1.0",
			Latest:    "v0.2.0",
			Command:   upgradeCommand("v0.2.0"),
			Notes:     []string{"Add dynamic welcome notes", "Improve TUI layout"},
			CheckedAt: time.Now(),
		},
	}
	got := stripTestANSI(renderWelcomeBanner(m, 100))
	if !strings.Contains(got, "What's new") || !strings.Contains(got, "Add dynamic welcome notes") {
		t.Fatalf("renderWelcomeBanner(notes) = %q, want dynamic release notes", got)
	}
}

func TestHandleLocalUpgradeShowsCommand(t *testing.T) {
	m := model{
		input: textinput.New(),
		updateInfo: updateInfo{
			Current:    "v0.1.0",
			Latest:     "v0.2.0",
			Command:    upgradeCommand("v0.2.0"),
			ReleaseURL: "https://github.com/mnhkahn/xiaoli/releases/tag/v0.2.0",
			CheckedAt:  time.Now(),
		},
	}
	if !m.handleLocalCommand("/upgrade") {
		t.Fatalf("handleLocalCommand(/upgrade) = false, want true")
	}
	if len(m.items) == 0 || !strings.Contains(m.items[len(m.items)-1].text, "go install") || !strings.Contains(m.items[len(m.items)-1].text, "releases/tag/v0.2.0") {
		t.Fatalf("items = %#v, want upgrade command and release URL", m.items)
	}
}

func TestRenderPendingOptionsMarksSelected(t *testing.T) {
	got := renderPendingAskPanel("是否允许执行命令？", []string{"允许::执行该命令", "拒绝::不执行"}, 1, 80)
	if !strings.Contains(got, "› 2. 拒绝") {
		t.Fatalf("renderPendingAskPanel() = %q, want selected marker", got)
	}
	if !strings.Contains(got, "不执行") {
		t.Fatalf("renderPendingAskPanel() = %q, want description", got)
	}
}

func TestTranscriptPlainText(t *testing.T) {
	items := []transcriptItem{
		{role: "user", text: "你好"},
		{role: "assistant", text: "世界"},
	}
	got := transcriptPlainText(items)
	if !strings.Contains(got, "user: 你好") || !strings.Contains(got, "assistant: 世界") {
		t.Fatalf("transcriptPlainText() = %q", got)
	}
}

func TestExitLogoUsesFigletStyle(t *testing.T) {
	logo := exitLogo()
	plain := stripTestANSI(logo)
	if !strings.Contains(plain, "___") || !strings.Contains(plain, "/ __") {
		t.Fatalf("exitLogo() = %q, want FIGlet-like strokes", logo)
	}
	if len(strings.Split(strings.TrimSpace(plain), "\n")) < 8 {
		t.Fatalf("exitLogo() lines too few: %q", logo)
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

func hasSuggestion(items []slashSuggestion, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}
