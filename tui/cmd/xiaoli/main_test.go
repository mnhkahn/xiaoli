package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mnhkahn/xiaoli/internal/agent/localapp"
	"github.com/mnhkahn/xiaoli/internal/agent/localconfig"
	agentevent "github.com/mnhkahn/xiaoli/internal/event"
	"github.com/muesli/termenv"
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

func TestStatusBarShowsSelectionMouseHint(t *testing.T) {
	got := stripTestANSI(renderStatusBar(model{cwd: "/tmp/repo", gitStatus: "main ✓", status: "idle"}, 80))
	if !strings.Contains(got, "drag select copies") || !strings.Contains(got, "Esc clear") {
		t.Fatalf("renderStatusBar(idle) = %q, want selection mouse hint", got)
	}
	got = stripTestANSI(renderStatusBar(model{cwd: "/tmp/repo", gitStatus: "main ✓", selection: transcriptSelection{active: true}, status: "selecting"}, 80))
	if !strings.Contains(got, "selecting") || !strings.Contains(got, "Esc clear") {
		t.Fatalf("renderStatusBar(selecting) = %q, want selection hints", got)
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

func TestPastedLongTextCollapsesIntoAttachment(t *testing.T) {
	input := textinput.New()
	m := model{input: input, cwd: t.TempDir(), width: 100, height: 30}
	pasted := strings.Repeat("这是一段很长的复制文本\n", 4)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted), Paste: true})
	got := next.(model)

	if got.input.Value() != "[Paste #1 · 4 lines]" {
		t.Fatalf("input = %q, want collapsed paste ref", got.input.Value())
	}
	if len(got.pastedContents) != 1 || got.pastedContents[1].Content != pasted {
		t.Fatalf("pastedContents = %#v, want original paste stored", got.pastedContents)
	}
	if expanded := got.expandInputAttachments(got.input.Value()); expanded != pasted {
		t.Fatalf("expanded = %q, want original paste", expanded)
	}
}

func TestPastedShortTextStaysInline(t *testing.T) {
	input := textinput.New()
	m := model{input: input, cwd: t.TempDir(), width: 100, height: 30}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("短文本"), Paste: true})
	got := next.(model)

	if got.input.Value() != "短文本" {
		t.Fatalf("input = %q, want short paste inline", got.input.Value())
	}
	if len(got.pastedContents) != 0 {
		t.Fatalf("pastedContents = %#v, want empty", got.pastedContents)
	}
}

func TestImageAttachmentUsesCompactPlaceholder(t *testing.T) {
	input := textinput.New()
	m := model{input: input, cwd: t.TempDir(), width: 100, height: 30}
	imagePath := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	m.addImageAttachment(imagePath, "image/png")

	if m.input.Value() != "[图#1]" {
		t.Fatalf("input = %q, want compact image ref", m.input.Value())
	}
	if len(m.pastedContents) != 1 || m.pastedContents[1].Path != imagePath {
		t.Fatalf("pastedContents = %#v, want image path stored", m.pastedContents)
	}
	expanded := m.expandInputAttachments(m.input.Value())
	if !strings.Contains(expanded, imagePath) || !strings.Contains(expanded, "图片 #1") {
		t.Fatalf("expanded = %q, want image path detail", expanded)
	}
}

func TestCtrlVPastesClipboardImageAsCompactPlaceholder(t *testing.T) {
	old := readClipboardImageFunc
	defer func() { readClipboardImageFunc = old }()
	imagePath := filepath.Join(t.TempDir(), "clip.png")
	readClipboardImageFunc = func(context.Context, string) (string, string, error) {
		return imagePath, "image/png", nil
	}
	input := textinput.New()
	m := model{input: input, cwd: t.TempDir(), width: 100, height: 30}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := next.(model)
	if cmd == nil {
		t.Fatal("Ctrl+V command is nil, want clipboard image read")
	}
	msg := cmd()
	next, _ = got.Update(msg)
	got = next.(model)

	if got.input.Value() != "[图#1]" {
		t.Fatalf("input = %q, want compact image ref", got.input.Value())
	}
	if len(got.pastedContents) != 1 || got.pastedContents[1].Path != imagePath {
		t.Fatalf("pastedContents = %#v, want clipboard image path stored", got.pastedContents)
	}
}

func TestClearInputDraftClearsPastedAttachments(t *testing.T) {
	input := textinput.New()
	m := model{input: input, pastedContents: map[int]pastedContent{1: {ID: 1, Type: pastedContentText, Content: "hello"}}}
	m.input.SetValue("[Paste #1 · 1 line]")

	m.clearInputDraft()

	if m.input.Value() != "" {
		t.Fatalf("input = %q, want empty", m.input.Value())
	}
	if len(m.pastedContents) != 0 {
		t.Fatalf("pastedContents = %#v, want cleared", m.pastedContents)
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

func TestCtrlONoLongerTogglesMouseMode(t *testing.T) {
	input := textinput.New()
	m := model{input: input, mouseEnabled: false, status: "idle"}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("Ctrl-O returned command, want nil")
	}
	if got.status == "mouse mode" {
		t.Fatalf("Ctrl-O entered mouse mode: status:%q", got.status)
	}
}

func TestTranscriptSelectionExtractsVisibleText(t *testing.T) {
	lines := []string{"alpha beta", "gamma delta"}
	sel := transcriptSelection{active: true, anchor: selectionPoint{x: 6, y: 0}, focus: selectionPoint{x: 4, y: 1}}
	got := selectedTranscriptText(lines, sel)
	if got != "beta\ngamma" {
		t.Fatalf("selectedTranscriptText() = %q, want beta/gamma", got)
	}
}

func TestTranscriptSelectionOverlayMarksSelectedCells(t *testing.T) {
	lines := []string{"alpha beta", "gamma delta"}
	sel := transcriptSelection{active: true, anchor: selectionPoint{x: 6, y: 0}, focus: selectionPoint{x: 4, y: 1}}
	got := renderTranscriptSelectionOverlay(lines, sel, 20)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("selection overlay missing ANSI highlight: %q", got)
	}
	plain := stripTestANSI(got)
	if !strings.Contains(plain, "alpha beta") || !strings.Contains(plain, "gamma delta") {
		t.Fatalf("selection overlay plain = %q, want original text", plain)
	}
}

func TestTranscriptSelectionOverlayKeepsHeightAndOriginalANSI(t *testing.T) {
	lines := []string{
		"\x1b[38;5;114massistant text\x1b[0m",
		"",
		"plain text",
	}
	sel := transcriptSelection{active: true, anchor: selectionPoint{x: 0, y: 0}, focus: selectionPoint{x: 5, y: 0}}
	got := renderTranscriptSelectionOverlay(lines, sel, 40)
	if len(strings.Split(got, "\n")) != len(lines) {
		t.Fatalf("overlay changed height: got %d lines, want %d: %q", len(strings.Split(got, "\n")), len(lines), got)
	}
	if !strings.Contains(got, "\x1b[38;5;114m") {
		t.Fatalf("overlay dropped original ANSI style: %q", got)
	}
	if strings.Contains(got, "48;5;238") {
		t.Fatalf("overlay used gray background instead of selection inverse: %q", got)
	}
}

func TestTranscriptSelectionOverlayAvoidsRightEdgeReset(t *testing.T) {
	lines := []string{"0123456789"}
	sel := transcriptSelection{active: true, anchor: selectionPoint{x: 0, y: 0}, focus: selectionPoint{x: 9, y: 0}}
	got := renderTranscriptSelectionOverlay(lines, sel, 10)
	if strings.HasSuffix(got, selectionEndSeq) {
		t.Fatalf("overlay ended with selection reset after the right edge: %q", got)
	}
	if stripTestANSI(got) != lines[0] {
		t.Fatalf("overlay plain text = %q, want %q", stripTestANSI(got), lines[0])
	}
}

func TestTranscriptSelectionOverlayDoesNotSelectTrailingPadding(t *testing.T) {
	lines := []string{"abc   "}
	sel := transcriptSelection{active: true, anchor: selectionPoint{x: 0, y: 0}, focus: selectionPoint{x: 5, y: 0}}
	got := renderTranscriptSelectionOverlay(lines, sel, 20)
	want := selectionStartSeq + "abc" + selectionEndSeq + "   "
	if got != want {
		t.Fatalf("overlay = %q, want selection only on content %q", got, want)
	}
}

func TestMouseDragSelectsTranscriptAndReleaseCopies(t *testing.T) {
	input := textinput.New()
	m := model{
		input:        input,
		mouseEnabled: true,
		width:        80,
		height:       24,
		viewport:     viewport.New(80, 5),
		items:        []transcriptItem{{role: "assistant", text: "hello world"}},
	}
	m.syncViewport(true)

	next, cmd := m.Update(tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 2, Y: 1})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("mouse press returned command, want nil")
	}
	if !got.selection.dragging {
		t.Fatalf("mouse press did not start dragging: %#v", got.selection)
	}

	next, cmd = got.Update(tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion, X: 7, Y: 1})
	got = next.(model)
	if cmd != nil {
		t.Fatalf("mouse motion returned command, want nil")
	}
	if !got.selection.active {
		t.Fatalf("mouse motion did not activate selection: %#v", got.selection)
	}

	next, cmd = got.Update(tea.MouseMsg{Type: tea.MouseRelease, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease, X: 7, Y: 1})
	got = next.(model)
	if cmd == nil {
		t.Fatalf("mouse release returned nil command, want copy command")
	}
	if got.selection.dragging {
		t.Fatalf("mouse release left dragging active: %#v", got.selection)
	}
	if strings.TrimSpace(got.selection.text) == "" {
		t.Fatalf("mouse release did not capture selected text: %#v", got.selection)
	}
}

func TestStripMouseSGRFragmentsFromInput(t *testing.T) {
	got := stripMouseSGRFragments("hello [<65;96;30M world \x1b[<64;10;5M!")
	if got != "hello  world !" {
		t.Fatalf("stripMouseSGRFragments() = %q, want mouse SGR fragments removed", got)
	}
}

func TestPendingOptionsUseUpDownBeforeViewportScroll(t *testing.T) {
	input := textinput.New()
	m := model{
		input:          input,
		pendingOptions: []string{"允许一次", "本会话允许", "拒绝"},
		pendingChoice:  0,
		width:          80,
		height:         24,
		viewport:       viewport.New(80, 5),
	}
	m.viewport.SetContent(strings.Repeat("line\n", 40))
	m.viewport.GotoBottom()
	before := m.viewport.YOffset

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("Down returned command, want nil")
	}
	if got.pendingChoice != 1 {
		t.Fatalf("pendingChoice after Down = %d, want 1", got.pendingChoice)
	}
	if got.viewport.YOffset != before {
		t.Fatalf("viewport offset changed from %d to %d", before, got.viewport.YOffset)
	}

	next, cmd = got.Update(tea.KeyMsg{Type: tea.KeyUp})
	got = next.(model)
	if cmd != nil {
		t.Fatalf("Up returned command, want nil")
	}
	if got.pendingChoice != 0 {
		t.Fatalf("pendingChoice after Up = %d, want 0", got.pendingChoice)
	}
	if got.viewport.YOffset != before {
		t.Fatalf("viewport offset changed after Up from %d to %d", before, got.viewport.YOffset)
	}
}

func TestRunEventsRenderGradientStatusRows(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	start := eventTranscriptItem(agentevent.Event{Type: agentevent.TypeAgentRunStarted})
	done := eventTranscriptItem(agentevent.Event{Type: agentevent.TypeAgentRunCompleted})
	if start.role != "run-active" || done.role != "run-done" {
		t.Fatalf("event roles = %q/%q, want run-active/run-done", start.role, done.role)
	}
	got := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{start, done}, 80, 2))
	if !strings.Contains(got, "Aligning") || !strings.Contains(got, "Delivered") {
		t.Fatalf("render run events = %q, want run labels", got)
	}
	if !strings.Contains(got, "(^_^)") || !strings.Contains(got, "(ok)") {
		t.Fatalf("render run events = %q, want kaomoji status glyphs", got)
	}
	shimmer := renderTranscriptContentWithFrame([]transcriptItem{start}, 80, 4)
	if !strings.Contains(shimmer, "\x1b[") {
		t.Fatalf("render run event missing ANSI shimmer: %q", shimmer)
	}
	first := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{start}, 80, 0))
	second := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{start}, 80, 1))
	if first != second {
		t.Fatalf("loading frame changed too quickly: %q / %q", first, second)
	}
	laterGlyph := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{start}, 80, 4))
	if !strings.Contains(first, "(^_^)") || !strings.Contains(laterGlyph, "(^_^)") {
		t.Fatalf("loading frames = %q / %q, want stable kaomoji glyph", first, laterGlyph)
	}
	later := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{start}, 80, 32))
	if !strings.Contains(later, "Syncing context") {
		t.Fatalf("status phrase did not rotate: %q", later)
	}
}

func TestOnlyLatestRunEventAnimates(t *testing.T) {
	first := transcriptItem{role: "run-active", frame: 0}
	second := transcriptItem{role: "run-active"}
	got := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{first, second}, 80, 32))
	if strings.Count(got, "Aligning") != 1 || strings.Count(got, "Syncing context") != 1 {
		t.Fatalf("rendered active events = %q, want frozen first and animated latest", got)
	}
}

func TestCommitSlashUsesRunEventStart(t *testing.T) {
	m := model{runPulseFrame: 7}
	m.appendRunActiveEvent("Preparing commit")
	if len(m.items) == 0 {
		t.Fatalf("/commit added no transcript item")
	}
	last := m.items[len(m.items)-1]
	if last.role != "run-active" || last.text != "Preparing commit" || !m.runPulseActive || m.runPulseFrame != 0 {
		t.Fatalf("/commit event = %#v, want run-active Preparing commit", last)
	}
}

func TestToolEventsRenderWorkplaceStatusRows(t *testing.T) {
	start := eventTranscriptItem(agentevent.Event{
		Type: agentevent.TypeAgentToolStarted,
		Data: map[string]any{"name": "bash"},
	})
	done := eventTranscriptItem(agentevent.Event{
		Type: agentevent.TypeAgentToolFinished,
		Data: map[string]any{"name": "bash"},
	})
	failed := eventTranscriptItem(agentevent.Event{
		Type: agentevent.TypeAgentToolFinished,
		Data: map[string]any{"name": "bash", "error": "exit status 1"},
	})
	if start.role != "tool-active" || done.role != "tool-done" || failed.role != "tool-failed" {
		t.Fatalf("tool event roles = %q/%q/%q", start.role, done.role, failed.role)
	}
	got := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{start, done, failed}, 100, 3))
	for _, want := range []string{"Tracing bash", "Validated bash", "Blocked bash"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool render = %q, missing %q", got, want)
		}
	}
	for _, want := range []string{"(._.)", "(ok)", "(>_<)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool render = %q, missing glyph %q", got, want)
		}
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

func TestWorkspacePickerTabSwitchesToNextWorkspace(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "workspaces.json")
	current := t.TempDir()
	nextDir := t.TempDir()
	if err := recordWorkspaceSession(statePath, workspaceItem{CWD: nextDir, SessionID: "next-session", Title: "Next", LastOpened: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatalf("upsert next workspace error = %v", err)
	}
	m := model{
		input:              textinput.New(),
		cwd:                current,
		sessionID:          "current-session",
		width:              100,
		height:             30,
		workspaceStatePath: statePath,
	}
	m.items = []transcriptItem{{role: "user", text: "old workspace transcript"}}
	m.syncViewport(true)
	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := nextModel.(model)
	nextModel, _ = got.Update(tea.KeyMsg{Type: tea.KeyTab})
	got = nextModel.(model)
	if got.workspacePicker == nil {
		t.Fatalf("second Tab did not open workspace picker")
	}

	nextModel, cmd := got.Update(tea.KeyMsg{Type: tea.KeyTab})
	got = nextModel.(model)
	if cmd != nil {
		t.Fatalf("picker Tab returned command, want nil")
	}
	if got.workspacePicker != nil {
		t.Fatalf("picker Tab left workspace picker open")
	}
	if !samePath(got.cwd, nextDir) || got.sessionID != "next-session" {
		t.Fatalf("picker Tab cwd/session = %q/%q, want %q/next-session", got.cwd, got.sessionID, nextDir)
	}
	if len(got.items) == 0 || !strings.Contains(got.items[len(got.items)-1].text, "Switched to") {
		t.Fatalf("items = %#v, want switch notice", got.items)
	}
	if view := got.viewport.View(); strings.Contains(view, "old workspace transcript") {
		t.Fatalf("viewport still shows old workspace transcript: %q", view)
	}
}

func TestNewModelDoesNotResumeWorkspaceSessionWithoutFlag(t *testing.T) {
	dataDir := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd)
	statePath := workspaceStatePath(dataDir)
	if err := recordWorkspaceSession(statePath, workspaceItem{CWD: cwd, SessionID: "recorded-session", Title: "Recorded", LastOpened: time.Now()}); err != nil {
		t.Fatalf("upsert workspace error = %v", err)
	}
	app := &localapp.App{
		Config: localconfig.Config{DataDir: dataDir},
		Bus:    agentevent.NewBus(),
	}

	m := newModel(app, "", "")

	if m.sessionID != "" {
		t.Fatalf("newModel sessionID = %q, want empty without -s", m.sessionID)
	}
	if len(m.items) != 1 || m.items[0].role != "banner" {
		t.Fatalf("items = %#v, want welcome banner", m.items)
	}
	item, ok := findWorkspace(statePath, cwd)
	if !ok {
		t.Fatalf("workspace %q not recorded", cwd)
	}
	if item.SessionID != "recorded-session" {
		t.Fatalf("recorded session = %q, want existing recorded-session preserved", item.SessionID)
	}
}

func TestSwitchWorkspaceWithoutRecordedSessionClearsActiveSession(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "workspaces.json")
	current := t.TempDir()
	nextDir := t.TempDir()
	m := model{
		input:              textinput.New(),
		cwd:                current,
		sessionID:          "current-session",
		width:              100,
		height:             30,
		workspaceStatePath: statePath,
		items:              []transcriptItem{{role: "user", text: "old workspace transcript"}},
	}
	m.syncViewport(true)

	m.switchWorkspace(workspaceItem{CWD: nextDir, Title: "Next"})

	if !samePath(m.cwd, nextDir) || m.sessionID != "" {
		t.Fatalf("switchWorkspace cwd/session = %q/%q, want %q/empty", m.cwd, m.sessionID, nextDir)
	}
	if view := m.viewport.View(); strings.Contains(view, "old workspace transcript") {
		t.Fatalf("viewport still shows old workspace transcript: %q", view)
	}
}

func TestWorkspaceStoreRecordsRecentSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	project := t.TempDir()
	base := time.Now()
	for i, sid := range []string{"s1", "s2", "s3", "s4"} {
		if err := recordWorkspaceSession(path, workspaceItem{CWD: project, SessionID: sid, Title: sid, LastOpened: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("record %s error = %v", sid, err)
		}
	}
	if err := recordWorkspaceSession(path, workspaceItem{CWD: project, LastOpened: base.Add(time.Hour)}); err != nil {
		t.Fatalf("record empty session error = %v", err)
	}
	items := loadWorkspaceSessions(path)
	if len(items) != 3 {
		t.Fatalf("len(loadWorkspaceSessions) = %d, want 3", len(items))
	}
	for i, want := range []string{"s4", "s3", "s2"} {
		if items[i].SessionID != want {
			t.Fatalf("items[%d].SessionID = %q, want %q; items=%#v", i, items[i].SessionID, want, items)
		}
	}
}

func TestWorkspaceStoreKeepsSessionInOnlyNewestProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	projectA := t.TempDir()
	projectB := t.TempDir()
	base := time.Now()
	if err := recordWorkspaceSession(path, workspaceItem{CWD: projectA, SessionID: "shared-session", Title: "A", LastOpened: base}); err != nil {
		t.Fatalf("record project A error = %v", err)
	}
	if err := recordWorkspaceSession(path, workspaceItem{CWD: projectB, SessionID: "shared-session", Title: "B", LastOpened: base.Add(time.Minute)}); err != nil {
		t.Fatalf("record project B error = %v", err)
	}

	items := loadWorkspaceSessions(path)
	if len(items) != 1 {
		t.Fatalf("len(loadWorkspaceSessions) = %d, want 1: %#v", len(items), items)
	}
	if !samePath(items[0].CWD, projectB) || items[0].SessionID != "shared-session" {
		t.Fatalf("session owner = %q/%q, want projectB/shared-session", items[0].CWD, items[0].SessionID)
	}
}

func TestWorkspacePickerSwitchDoesNotPersistEmptySession(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "workspaces.json")
	projectA := t.TempDir()
	projectB := t.TempDir()
	t.Chdir(projectA)
	if err := recordWorkspaceSession(statePath, workspaceItem{CWD: projectA, SessionID: "session-a", Title: "A", LastOpened: time.Now()}); err != nil {
		t.Fatalf("record project A error = %v", err)
	}
	m := model{
		input:              textinput.New(),
		cwd:                projectA,
		sessionID:          "session-a",
		width:              100,
		height:             30,
		workspaceStatePath: statePath,
	}

	m.switchWorkspace(workspaceItem{CWD: projectB, Title: "B"})
	m.switchWorkspace(workspaceItem{CWD: projectA, SessionID: "session-a", Title: "A"})
	m.switchWorkspace(workspaceItem{CWD: projectB, Title: "B"})

	if !samePath(m.cwd, projectB) || m.sessionID != "" {
		t.Fatalf("switch back to empty project cwd/session = %q/%q, want %q/empty", m.cwd, m.sessionID, projectB)
	}
	if got, _ := os.Getwd(); !samePath(got, projectB) {
		t.Fatalf("process cwd = %q, want %q", got, projectB)
	}
	for _, item := range loadWorkspaceSessions(statePath) {
		if samePath(item.CWD, projectB) {
			t.Fatalf("empty project was persisted in session list: %#v", item)
		}
	}
}

func TestSwitchWorkspaceRestoresSession(t *testing.T) {
	dir := t.TempDir()
	start := t.TempDir()
	t.Chdir(start)
	m := model{cwd: t.TempDir(), input: textinput.New(), workspaceStatePath: filepath.Join(t.TempDir(), "workspaces.json")}
	item := workspaceItem{CWD: dir, SessionID: "session-1", Title: "Project"}
	m.switchWorkspace(item)
	if !samePath(m.cwd, dir) || m.sessionID != "session-1" {
		t.Fatalf("switchWorkspace cwd/session = %q/%q", m.cwd, m.sessionID)
	}
	if got, _ := os.Getwd(); !samePath(got, dir) {
		t.Fatalf("process cwd = %q, want %q", got, dir)
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
	t.Chdir(root)
	m := model{cwd: root, gitStatus: "old", input: textinput.New()}
	if !m.handleLocalCommand("/cd child") {
		t.Fatalf("handleLocalCommand(/cd child) = false, want true")
	}
	if !samePath(m.cwd, child) {
		t.Fatalf("cwd = %q, want %q", m.cwd, child)
	}
	if got, _ := os.Getwd(); !samePath(got, child) {
		t.Fatalf("process cwd = %q, want %q", got, child)
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

func TestRenderPendingAskPanelSummarizesPythonCommands(t *testing.T) {
	command := "python3 - <<'PY'\n" +
		"import os\n" +
		"print('secret implementation detail that should not fill the approval panel')\n" +
		"for item in range(100):\n" +
		"    print(item)\n" +
		"PY"
	got := renderPendingAskPanel("是否允许执行命令："+command, []string{
		"允许一次::" + command,
		"拒绝::不执行",
	}, 0, 80)
	if !strings.Contains(got, "python 输出结果") {
		t.Fatalf("renderPendingAskPanel() = %q, want python action summary", got)
	}
	if strings.Contains(got, "secret implementation detail") || strings.Contains(got, "range(100)") {
		t.Fatalf("renderPendingAskPanel() leaked full script: %q", got)
	}
}

func TestSummarizeCommandForDisplayShowsPythonScriptPath(t *testing.T) {
	got := summarizeCommandForDisplay("python3 scripts/deploy.py --env prod --verbose")
	if got != "python 执行脚本 scripts/deploy.py" {
		t.Fatalf("summarizeCommandForDisplay() = %q", got)
	}
}

func TestSummarizeCommandForDisplayDescribesPythonInlineActions(t *testing.T) {
	command := "python3 - <<'PY'\n" +
		"import json\n" +
		"from pathlib import Path\n" +
		"data = json.loads(Path('input.json').read_text())\n" +
		"Path('out.json').write_text(json.dumps(data))\n" +
		"PY"
	got := summarizeCommandForDisplay(command)
	for _, want := range []string{"python", "写入文件", "读取文件", "处理 JSON", "input.json", "out.json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summarizeCommandForDisplay() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "5 行") || strings.Contains(got, "内联脚本") {
		t.Fatalf("summarizeCommandForDisplay() = %q, want action summary instead of line-count summary", got)
	}
}

func TestRenderPendingAskPanelKeepsCommandSummarySingleLine(t *testing.T) {
	command := "python3 - <<'PY'\n" +
		"import json\n" +
		"from pathlib import Path\n" +
		"data = json.loads(Path('input.json').read_text())\n" +
		"Path('out.json').write_text(json.dumps(data))\n" +
		"PY"
	got := stripTestANSI(renderPendingAskPanel("是否允许执行命令："+command, []string{
		"允许一次::" + command,
		"拒绝::不执行",
	}, 0, 96))
	if !strings.Contains(got, "文件 input.json、out.json") {
		t.Fatalf("renderPendingAskPanel() = %q, want file detail on summary line", got)
	}
	if strings.Count(got, "允许一次") != 1 {
		t.Fatalf("renderPendingAskPanel() = %q, want selected option rendered once", got)
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
