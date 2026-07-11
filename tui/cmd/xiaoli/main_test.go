package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/xiaoli/internal/agent/localapp"
	"github.com/mnhkahn/xiaoli/internal/agent/localconfig"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
	agentevent "github.com/mnhkahn/xiaoli/internal/event"
	"github.com/muesli/termenv"
)

func newTestLocalApp(t *testing.T, dataDir string) *localapp.App {
	t.Helper()
	agent := agentruntime.NewAgent(agentruntime.Config{
		LLMURL:         "https://example.invalid/v1",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		StorageBackend: "local",
		LocalDataDir:   dataDir,
	}, agentevent.NewBus())
	if agent == nil {
		t.Fatal("NewAgent() returned nil")
	}
	return &localapp.App{
		Config: localconfig.Config{DataDir: dataDir},
		Bus:    agentevent.NewBus(),
		Agent:  agent,
	}
}

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

func TestChatDeltaFlushesViewportOnThrottleTick(t *testing.T) {
	m := model{
		width:          100,
		height:         30,
		viewport:       viewport.New(0, 0),
		chatRunID:      7,
		chatMsgs:       make(chan tea.Msg),
		streamingIndex: -1,
	}
	m.viewport.SetContent("before")

	next, cmd := m.Update(chatDeltaMsg{delta: "slow stream"})
	got := next.(model)
	if cmd == nil {
		t.Fatalf("chat delta returned nil command, want wait + throttled flush")
	}
	if !got.streamFlushPending {
		t.Fatalf("streamFlushPending = false, want true")
	}
	if view := got.View(); strings.Contains(stripTestANSI(view), "slow stream") {
		t.Fatalf("View synced transcript before flush: %q", stripTestANSI(view))
	}

	next, _ = got.Update(streamFlushMsg{runID: 7})
	got = next.(model)
	if got.streamFlushPending {
		t.Fatalf("streamFlushPending = true after flush, want false")
	}
	if view := stripTestANSI(got.viewport.View()); !strings.Contains(view, "slow stream") {
		t.Fatalf("viewport after flush = %q, want streamed text", view)
	}
}

func TestStreamDeltaBatcherCombinesDeltasBeforeSending(t *testing.T) {
	ctx := context.Background()
	out := make(chan tea.Msg, 3)
	batcher := newStreamDeltaBatcher(ctx, out)

	if !batcher.Append("a") || !batcher.Append("b") || !batcher.Append("c") {
		t.Fatalf("Append returned false, want true")
	}
	if len(out) != 0 {
		t.Fatalf("out len = %d, want no per-token messages before flush", len(out))
	}

	if !batcher.Flush() {
		t.Fatalf("Flush returned false, want true")
	}
	msg, ok := (<-out).(chatDeltaMsg)
	if !ok {
		t.Fatalf("flushed msg type = %T, want chatDeltaMsg", msg)
	}
	if msg.delta != "abc" {
		t.Fatalf("flushed delta = %q, want %q", msg.delta, "abc")
	}
	if !msg.flushNow {
		t.Fatalf("flushed msg flushNow = false, want true")
	}
	if len(out) != 0 {
		t.Fatalf("out len = %d after flush, want 0", len(out))
	}
}

func TestBatchedChatDeltaFlushesViewportImmediately(t *testing.T) {
	m := model{
		width:          100,
		height:         30,
		viewport:       viewport.New(0, 0),
		chatRunID:      7,
		chatMsgs:       make(chan tea.Msg),
		streamingIndex: -1,
	}
	m.viewport.SetContent("before")

	next, cmd := m.Update(chatDeltaMsg{delta: "batched stream", flushNow: true})
	got := next.(model)
	if cmd == nil {
		t.Fatalf("batched chat delta returned nil command, want waitForChat")
	}
	if got.streamFlushPending {
		t.Fatalf("streamFlushPending = true, want false for batched flush")
	}
	if view := stripTestANSI(got.viewport.View()); !strings.Contains(view, "batched stream") {
		t.Fatalf("viewport after batched delta = %q, want streamed text", view)
	}
}

func TestWaitForClosedChannelsStopsWaiting(t *testing.T) {
	chatCh := make(chan tea.Msg)
	close(chatCh)
	if _, ok := waitForChat(chatCh)().(chatStreamClosedMsg); !ok {
		t.Fatalf("waitForChat(closed) did not return chatStreamClosedMsg")
	}

	eventCh := make(chan agentevent.Event)
	close(eventCh)
	if _, ok := waitForEvent(eventCh)().(eventStreamClosedMsg); !ok {
		t.Fatalf("waitForEvent(closed) did not return eventStreamClosedMsg")
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

func TestNewModelDefaultsToMouseViewportScrolling(t *testing.T) {
	m := newModel(newTestLocalApp(t, t.TempDir()), "", "")
	if !m.mouseEnabled {
		t.Fatalf("mouseEnabled = false, want true so wheel scrolls transcript viewport")
	}
}

func TestTerminalTitleStates(t *testing.T) {
	tests := []struct {
		name  string
		model model
		want  string
	}{
		{name: "idle", model: model{cwd: "/Users/test/code/alpha"}, want: "alpha"},
		{name: "running", model: model{cwd: "/Users/test/code/alpha", busy: true, status: "running", runPulseFrame: 1}, want: "alpha"},
		{name: "approval", model: model{cwd: "/Users/test/code/alpha", status: "waiting approval"}, want: "alpha"},
		{name: "input", model: model{cwd: "/Users/test/code/alpha", status: "waiting input"}, want: "alpha"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terminalTitle(tt.model); got != tt.want {
				t.Fatalf("terminalTitle() = %q, want %q", got, tt.want)
			}
			if strings.Contains(terminalTitle(tt.model), "\a") {
				t.Fatalf("terminalTitle() contains BEL: %q", terminalTitle(tt.model))
			}
		})
	}
}

func TestTerminalProjectName(t *testing.T) {
	tests := []struct {
		cwd  string
		want string
	}{
		{cwd: "/Users/test/code/alpha", want: "alpha"},
		{cwd: "/Users/test/code/alpha/", want: "alpha"},
		{cwd: "", want: "-"},
	}
	for _, tt := range tests {
		if got := terminalProjectName(tt.cwd); got != tt.want {
			t.Fatalf("terminalProjectName(%q) = %q, want %q", tt.cwd, got, tt.want)
		}
	}
}

func TestTerminalTitleTransientStatesDoNotRingBell(t *testing.T) {
	for _, title := range []string{terminalDoneTitle, terminalFailedTitle} {
		if strings.Contains(title, "\a") {
			t.Fatalf("transient title contains BEL: %q", title)
		}
	}
}

func TestTerminalTabTitleSequenceUsesOSC1(t *testing.T) {
	const title = "[⠋ RUNNING] Xiaoli"
	got := terminalTabTitleSequence(title)
	want := "\x1b]1;" + title + "\x07"
	if got != want {
		t.Fatalf("terminalTabTitleSequence() = %q, want %q", got, want)
	}
	if strings.Contains(got, "\x1b]2;") {
		t.Fatalf("terminal tab title sequence uses OSC 2: %q", got)
	}
}

func TestTerminalProgressSequences(t *testing.T) {
	tests := []struct {
		name  string
		state terminalProgressState
		want  string
	}{
		{name: "running", state: terminalProgressRunning, want: "\x1b]9;4;3\x07"},
		{name: "done", state: terminalProgressDone, want: "\x1b]9;4;5;0\x07"},
		{name: "failed", state: terminalProgressFailed, want: "\x1b]9;4;5;1\x07"},
		{name: "clear", state: terminalProgressClear, want: "\x1b]9;4;0\x07"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terminalProgressSequence(tt.state); got != tt.want {
				t.Fatalf("terminalProgressSequence(%v) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestTerminalProgressStates(t *testing.T) {
	tests := []struct {
		name  string
		model model
		want  terminalProgressState
	}{
		{name: "idle clears progress", model: model{}, want: terminalProgressClear},
		{name: "busy is indeterminate", model: model{busy: true}, want: terminalProgressRunning},
		{name: "run pulse is indeterminate", model: model{runPulseActive: true}, want: terminalProgressRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terminalProgress(tt.model); got != tt.want {
				t.Fatalf("terminalProgress() = %v, want %v", got, tt.want)
			}
		})
	}
	if got := terminalProgressForTitle(terminalDoneTitle); got != terminalProgressDone {
		t.Fatalf("done title progress = %v, want %v", got, terminalProgressDone)
	}
	if got := terminalProgressForTitle(terminalFailedTitle); got != terminalProgressFailed {
		t.Fatalf("failed title progress = %v, want %v", got, terminalProgressFailed)
	}
	if got := terminalProgressForTitle(terminalIdleTitle); got != terminalProgressClear {
		t.Fatalf("idle title progress = %v, want %v", got, terminalProgressClear)
	}
}

func TestViewConstrainsTranscriptWhenNoPendingPanel(t *testing.T) {
	input := textinput.New()
	items := make([]transcriptItem, 0, 40)
	for i := 0; i < 40; i++ {
		items = append(items, transcriptItem{role: "assistant", text: fmt.Sprintf("history line %02d", i)})
	}
	m := model{
		input:    input,
		width:    100,
		height:   24,
		viewport: viewport.New(100, 5),
		items:    items,
		status:   "idle",
	}
	m.syncViewport(true)

	got := stripTestANSI(m.View())
	for _, want := range []string{"history line 39", ">"} {
		if !strings.Contains(got, want) {
			t.Fatalf("View() missing %q in constrained transcript:\n%s", want, got)
		}
	}
	if strings.Contains(got, "history line 00") {
		t.Fatalf("View() rendered old transcript head without a pending panel:\n%s", got)
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

func TestApprovedBashFollowupCarriesToolUseID(t *testing.T) {
	got := formatApprovedBashFollowup("toolu_bash_test_123", "echo ok", "ok\n", nil)
	for _, want := range []string{
		"[TOOL_RESULT]",
		"tool=bash",
		"tool_use_id=toolu_bash_test_123",
		"status=success",
		"[/TOOL_RESULT]",
		"直接调用下一个工具",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatApprovedBashFollowup() = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "用户已批准并执行") {
		t.Fatalf("formatApprovedBashFollowup() = %q, should not use plain approval wrapper", got)
	}
}

func TestApprovedBashFollowupTreatsShellErrorOutputAsError(t *testing.T) {
	got := formatApprovedBashFollowup(
		"toolu_bash_nomatch",
		`grep -rn foo routers/*.go 2>/dev/null | head -20`,
		"zsh:1: no matches found: routers/*.go\n",
		nil,
	)

	for _, want := range []string{
		"status=error",
		"执行错误：shell reported an error in output",
		"zsh:1: no matches found: routers/*.go",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatApprovedBashFollowup() = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "status=success") {
		t.Fatalf("formatApprovedBashFollowup() = %q, should not mark shell error output as success", got)
	}
}

func TestExpiredBashFollowupCarriesToolUseID(t *testing.T) {
	got := formatExpiredBashFollowup("toolu_bash_test_expired", "echo stale")
	for _, want := range []string{
		"[TOOL_RESULT]",
		"tool=bash",
		"tool_use_id=toolu_bash_test_expired",
		"status=expired",
		"echo stale",
		"重新发起新的 bash 工具调用",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatExpiredBashFollowup() = %q, want %q", got, want)
		}
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
	if !strings.Contains(got, "wheel scroll") || !strings.Contains(got, "drag copy") || !strings.Contains(got, "Esc clear") {
		t.Fatalf("renderStatusBar(idle) = %q, want mouse viewport hints", got)
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

func TestCtrlKInDiffClosesExplorerAndStartsCommit(t *testing.T) {
	input := textinput.New()
	m := model{
		app:      newTestLocalApp(t, t.TempDir()),
		input:    input,
		cwd:      t.TempDir(),
		width:    100,
		height:   30,
		viewport: viewport.New(100, 10),
		explorer: &tuiExplorer{mode: explorerDiff},
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	got := next.(model)

	if cmd == nil {
		t.Fatal("Ctrl-K in diff returned nil command, want commit preparation")
	}
	if got.explorer != nil {
		t.Fatalf("Ctrl-K in diff explorer = %#v, want explorer closed", got.explorer)
	}
	if !got.autoCommitGitCmsg || !got.busy || got.status != "commit" {
		t.Fatalf("auto/busy/status = %v/%v/%q, want true/true/commit", got.autoCommitGitCmsg, got.busy, got.status)
	}
}

func TestGitCmsgPrepareAutoCommitsFromDiffShortcut(t *testing.T) {
	input := textinput.New()
	m := model{
		input:             input,
		cwd:               t.TempDir(),
		width:             100,
		height:            30,
		viewport:          viewport.New(100, 10),
		busy:              true,
		autoCommitGitCmsg: true,
		explorer:          &tuiExplorer{mode: explorerDiff},
	}

	next, cmd := m.Update(gitCmsgPrepareMsg{args: "", message: "fix(tui): 快捷提交"})
	got := next.(model)

	if cmd == nil {
		t.Fatal("auto commit preparation returned nil command, want git commit")
	}
	if got.autoCommitGitCmsg || got.pendingGitCmsg.Active || got.pendingQuestion != "" || got.pendingOptions != nil {
		t.Fatalf("commit state = auto:%v pending:%#v question:%q options:%#v, want cleared", got.autoCommitGitCmsg, got.pendingGitCmsg, got.pendingQuestion, got.pendingOptions)
	}
	if !got.busy || got.status != "git commit" {
		t.Fatalf("busy/status = %v/%q, want true/git commit", got.busy, got.status)
	}
	if got.explorer == nil || got.explorer.mode != explorerDiff {
		t.Fatalf("explorer = %#v, want diff explorer unchanged", got.explorer)
	}
}

func TestCtrlKOpensAndClosesDiffWhileAgentIsBusy(t *testing.T) {
	input := textinput.New()
	m := model{input: input, cwd: t.TempDir(), width: 100, height: 30, busy: true, status: "working"}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	got := next.(model)
	if cmd != nil {
		t.Fatal("Ctrl-K while busy returned command, want view-only")
	}
	if got.explorer == nil || got.explorer.mode != explorerDiff || !got.busy || got.status != "working" {
		t.Fatalf("open diff while busy = explorer:%#v busy:%v status:%q", got.explorer, got.busy, got.status)
	}

	next, cmd = got.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	got = next.(model)
	if cmd != nil {
		t.Fatal("second Ctrl-K while busy returned command, want view-only")
	}
	if got.explorer != nil || !got.busy || got.status != "working" {
		t.Fatalf("close diff while busy = explorer:%#v busy:%v status:%q", got.explorer, got.busy, got.status)
	}
}

func TestCtrlKCommitsWhenCommitPlanIsPending(t *testing.T) {
	input := textinput.New()
	m := model{
		input:           input,
		cwd:             t.TempDir(),
		width:           100,
		height:          30,
		viewport:        viewport.New(100, 10),
		pendingGitCmsg:  gitCmsgPending{Active: true, Message: "fix(tui): polish commit flow"},
		pendingQuestion: "确认提交？",
		pendingOptions:  []string{"提交并推送", "确认提交", "重新生成", "取消操作"},
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	got := next.(model)

	if cmd == nil {
		t.Fatal("Ctrl-K returned nil command, want commit command")
	}
	if got.explorer != nil {
		t.Fatalf("Ctrl-K explorer = %#v, want no diff explorer", got.explorer)
	}
	if got.pendingGitCmsg.Active || got.pendingQuestion != "" || got.pendingOptions != nil {
		t.Fatalf("pending commit state = %#v question=%q options=%#v, want cleared", got.pendingGitCmsg, got.pendingQuestion, got.pendingOptions)
	}
	if !got.busy || got.status != "git commit && push" {
		t.Fatalf("busy/status = %v/%q, want git commit && push", got.busy, got.status)
	}
	if len(got.items) == 0 || got.items[len(got.items)-1].role != "run-active" || got.items[len(got.items)-1].text != "Commit and push" {
		t.Fatalf("last item = %#v, want Commit and push run-active", got.items)
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

func TestTranscriptMousePointIncludesLastVisibleLine(t *testing.T) {
	m := model{width: 80, height: 24}
	_, _, bodyH, _, _ := layoutSizes(m.width, m.height)

	point, ok := m.transcriptMousePoint(tea.MouseMsg{X: 2, Y: bodyH - 1})
	if !ok {
		t.Fatalf("transcriptMousePoint(last visible line) ok = false")
	}
	if point.y != bodyH-2 {
		t.Fatalf("transcriptMousePoint(last visible line).y = %d, want %d", point.y, bodyH-2)
	}
}

func TestTranscriptMousePointUsesDynamicPromptHeight(t *testing.T) {
	input := textinput.New()
	m := model{
		input:           input,
		pendingQuestion: strings.Repeat("是否允许执行这项操作？", 12),
		pendingOptions:  []string{"允许一次", "拒绝"},
		width:           80,
		height:          30,
	}
	layout := m.mainViewLayout()
	_, _, staticH, _, _ := layoutSizes(m.width, m.height)
	if layout.transcriptH >= staticH {
		t.Fatalf("dynamic transcript height = %d, want less than fixed height %d", layout.transcriptH, staticH)
	}

	// Screen row 1 is the top content row inside the border. The last
	// visible row must map to the last row in the dynamically rendered
	// viewport, not to the old fixed-height viewport.
	point, ok := m.transcriptMousePoint(tea.MouseMsg{X: 2, Y: layout.transcriptH})
	if !ok {
		t.Fatal("transcriptMousePoint(last dynamic visible line) ok = false")
	}
	if point.y != layout.transcriptH-1 {
		t.Fatalf("transcriptMousePoint().y = %d, want %d", point.y, layout.transcriptH-1)
	}
}

func TestMarkdownLinkRendersAsUnderlinedTitle(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	got := renderTranscriptContent([]transcriptItem{{
		role: "assistant",
		text: "看 [GitHub](https://github.com/mnhkahn/xiaoli) 这里",
	}}, 80)
	plain := stripTestANSI(got)
	if !strings.Contains(plain, "GitHub") {
		t.Fatalf("rendered link title missing: %q", plain)
	}
	if strings.Contains(plain, "[GitHub](") || strings.Contains(plain, "https://github.com/mnhkahn/xiaoli") {
		t.Fatalf("rendered markdown link leaked raw syntax/url: %q", plain)
	}
	if !strings.Contains(got, "\x1b[4;") && !strings.Contains(got, ";4m") {
		t.Fatalf("rendered link lacks underline style: %q", got)
	}
}

func TestMarkdownBoldItalicRenderWithoutRawMarkers(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	got := renderTranscriptContent([]transcriptItem{{
		role: "assistant",
		text: "这是 **重点** 和 *轻声*",
	}}, 80)
	plain := stripTestANSI(got)
	if !strings.Contains(plain, "重点") || !strings.Contains(plain, "轻声") {
		t.Fatalf("rendered markdown text missing content: %q", plain)
	}
	if strings.Contains(plain, "**") || strings.Contains(plain, "*轻声*") {
		t.Fatalf("rendered markdown leaked emphasis markers: %q", plain)
	}
}

func TestMarkdownTableDoesNotLeakSeparatorSyntax(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	got := renderTranscriptContent([]transcriptItem{{
		role: "assistant",
		text: "| Name | Status |\n| --- | --- |\n| Tool | Ready |",
	}}, 80)
	plain := stripTestANSI(got)
	if !strings.Contains(plain, "Name") || !strings.Contains(plain, "Tool") {
		t.Fatalf("rendered markdown table missing cells: %q", plain)
	}
	if strings.Contains(plain, "| --- | --- |") {
		t.Fatalf("rendered markdown table leaked separator syntax: %q", plain)
	}
}

func TestTranscriptLinkClickOpensURL(t *testing.T) {
	input := textinput.New()
	m := model{
		input:        input,
		mouseEnabled: true,
		width:        80,
		height:       24,
		viewport:     viewport.New(80, 5),
		items: []transcriptItem{{
			role: "assistant",
			text: "[GitHub](https://github.com/mnhkahn/xiaoli)",
		}},
	}
	m.syncViewport(true)
	if len(m.transcriptLinks) != 1 {
		t.Fatalf("transcript links = %#v, want one link", m.transcriptLinks)
	}
	opened := ""
	oldOpenURL := openURLFunc
	openURLFunc = func(url string) error {
		opened = url
		return nil
	}
	defer func() { openURLFunc = oldOpenURL }()

	next, cmd := m.Update(tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 2, Y: 1})
	got := next.(model)
	if cmd == nil {
		t.Fatalf("link click returned nil command")
	}
	if got.selection.dragging {
		t.Fatalf("link click started text selection: %#v", got.selection)
	}
	msg := cmd()
	done, ok := msg.(linkOpenDoneMsg)
	if !ok {
		t.Fatalf("link open command returned %T, want linkOpenDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("link open command error = %v", done.err)
	}
	if opened != "https://github.com/mnhkahn/xiaoli" {
		t.Fatalf("opened URL = %q", opened)
	}
}

func TestTranscriptLinkHitboxUsesDisplayWidthAfterChineseText(t *testing.T) {
	m := model{
		mouseEnabled: true,
		width:        80,
		height:       24,
		viewport:     viewport.New(80, 5),
		items: []transcriptItem{{
			role: "assistant",
			text: "看 [GitHub](https://github.com/mnhkahn/xiaoli)",
		}},
	}
	m.syncViewport(true)
	if len(m.transcriptLinks) != 1 {
		t.Fatalf("transcript links = %#v, want one link", m.transcriptLinks)
	}
	if m.transcriptLinks[0].x0 != 3 {
		t.Fatalf("link x0 = %d, want display column 3 after Chinese prefix", m.transcriptLinks[0].x0)
	}
	if _, ok := m.transcriptLinkAt(tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 4, Y: 1}); ok {
		t.Fatalf("click before link title hit the link")
	}
	if url, ok := m.transcriptLinkAt(tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 5, Y: 1}); !ok || url != "https://github.com/mnhkahn/xiaoli" {
		t.Fatalf("click on link title = %q %v", url, ok)
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
		input:           input,
		pendingQuestion: "是否允许执行命令？",
		pendingOptions:  []string{"允许一次", "本会话允许", "拒绝"},
		pendingChoice:   0,
		width:           80,
		height:          24,
		viewport:        viewport.New(80, 5),
	}
	m.items = []transcriptItem{{role: "assistant", text: strings.Repeat("line\n", 40)}}
	m.syncViewport(true)
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
	if view := stripTestANSI(got.View()); !strings.Contains(view, "› 2. 本会话允许") {
		t.Fatalf("View after Down = %q, want selected second option", view)
	}
	if view := stripTestANSI(got.viewport.View()); strings.Contains(view, "› 2. 本会话允许") {
		t.Fatalf("viewport after Down = %q, want pending option only near input", view)
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
	if view := stripTestANSI(got.View()); !strings.Contains(view, "› 1. 允许一次") {
		t.Fatalf("View after Up = %q, want selected first option", view)
	}
	if view := stripTestANSI(got.viewport.View()); strings.Contains(view, "› 1. 允许一次") {
		t.Fatalf("viewport after Up = %q, want pending option only near input", view)
	}
	if got.viewport.YOffset != before {
		t.Fatalf("viewport offset changed after Up from %d to %d", before, got.viewport.YOffset)
	}
}

func TestPendingBashConfirmEnterApprovesWhileBusy(t *testing.T) {
	ctx, holder := agentbuiltin.NewToolUseConfirmHolder(context.Background())
	ctx = context.WithValue(ctx, agentbuiltin.SubAgentParentKey, "ses_enter_busy")
	tool := agentbuiltin.NewShellTool(agentbuiltin.ShellConfig{})
	if _, err := tool.InvokableRun(ctx, `{"command":"printf approved"}`); err != nil {
		t.Fatal(err)
	}
	confirm := holder.Get()
	if confirm == nil {
		t.Fatal("missing pending bash confirm")
	}
	t.Cleanup(func() { agentbuiltin.ClearBashApproval("ses_enter_busy") })
	input := textinput.New()
	m := model{
		app:                         newTestLocalApp(t, t.TempDir()),
		input:                       input,
		sessionID:                   "ses_enter_busy",
		pendingToolConfirm:          confirm,
		pendingToolConfirmReviewFix: false,
		pendingQuestion:             confirm.Question,
		pendingOptions:              append([]string(nil), confirm.Options...),
		pendingChoice:               0,
		busy:                        true,
		status:                      "running",
		width:                       100,
		height:                      24,
		viewport:                    viewport.New(100, 10),
		items:                       []transcriptItem{{role: "run-active", text: "Aligning"}},
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if cmd == nil {
		t.Fatal("Enter returned nil command, want approved bash command")
	}
	if !got.busy || got.status != "bash running" {
		t.Fatalf("busy/status = %v/%q, want bash running", got.busy, got.status)
	}
	if got.pendingToolConfirm != nil {
		t.Fatalf("pendingToolConfirm = %#v, want cleared", got.pendingToolConfirm)
	}
	if got.items[len(got.items)-1].role != "event" || !strings.Contains(got.items[len(got.items)-1].text, "approved bash: printf approved") {
		t.Fatalf("last item = %#v, want approved bash event", got.items[len(got.items)-1])
	}
}

func TestShiftTabTogglesAutoBashMode(t *testing.T) {
	m := model{
		input:    textinput.New(),
		width:    100,
		height:   24,
		viewport: viewport.New(100, 10),
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	got := next.(model)

	if cmd != nil {
		t.Fatalf("Shift+Tab returned command, want nil")
	}
	if !got.autoApproveBash {
		t.Fatal("autoApproveBash = false, want true")
	}
	if last := got.items[len(got.items)-1]; last.role != "system" || !strings.Contains(last.text, "已开启 bash 自动通过模式") {
		t.Fatalf("last item = %#v, want auto bash enabled message", last)
	}

	next, cmd = got.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	got = next.(model)

	if cmd != nil {
		t.Fatalf("second Shift+Tab returned command, want nil")
	}
	if got.autoApproveBash {
		t.Fatal("autoApproveBash = true, want false")
	}
	if last := got.items[len(got.items)-1]; last.role != "system" || !strings.Contains(last.text, "已关闭 bash 自动通过模式") {
		t.Fatalf("last item = %#v, want auto bash disabled message", last)
	}
}

func TestPendingBashConfirmCtrlAEnablesAutoBashAndApproves(t *testing.T) {
	ctx, holder := agentbuiltin.NewToolUseConfirmHolder(context.Background())
	ctx = context.WithValue(ctx, agentbuiltin.SubAgentParentKey, "ses_auto_ctrl_a")
	tool := agentbuiltin.NewShellTool(agentbuiltin.ShellConfig{})
	if _, err := tool.InvokableRun(ctx, `{"command":"printf auto"}`); err != nil {
		t.Fatal(err)
	}
	confirm := holder.Get()
	if confirm == nil {
		t.Fatal("missing pending bash confirm")
	}
	t.Cleanup(func() { agentbuiltin.ClearBashApproval("ses_auto_ctrl_a") })
	m := model{
		app:                         newTestLocalApp(t, t.TempDir()),
		input:                       textinput.New(),
		sessionID:                   "ses_auto_ctrl_a",
		pendingToolConfirm:          confirm,
		pendingToolConfirmReviewFix: false,
		pendingQuestion:             confirm.Question,
		pendingOptions:              append([]string(nil), confirm.Options...),
		pendingChoice:               0,
		busy:                        true,
		status:                      "running",
		width:                       100,
		height:                      24,
		viewport:                    viewport.New(100, 10),
		items:                       []transcriptItem{{role: "run-active", text: "Working"}},
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	got := next.(model)

	if cmd == nil {
		t.Fatal("Ctrl+A returned nil command, want approved bash command")
	}
	if !got.autoApproveBash {
		t.Fatal("autoApproveBash = false, want true")
	}
	if !got.busy || got.status != "bash running" {
		t.Fatalf("busy/status = %v/%q, want bash running", got.busy, got.status)
	}
	if got.pendingToolConfirm != nil {
		t.Fatalf("pendingToolConfirm = %#v, want cleared", got.pendingToolConfirm)
	}
	if got.items[len(got.items)-1].role != "event" || !strings.Contains(got.items[len(got.items)-1].text, "auto-approved bash: printf auto") {
		t.Fatalf("last item = %#v, want auto-approved bash event", got.items[len(got.items)-1])
	}
}

func TestPendingBashConfirmUsesConfirmSessionIDForAutoApprove(t *testing.T) {
	ctx, holder := agentbuiltin.NewToolUseConfirmHolder(context.Background())
	ctx = context.WithValue(ctx, agentbuiltin.SubAgentParentKey, "ses_confirm_owner")
	tool := agentbuiltin.NewShellTool(agentbuiltin.ShellConfig{})
	if _, err := tool.InvokableRun(ctx, `{"command":"printf session"}`); err != nil {
		t.Fatal(err)
	}
	confirm := holder.Get()
	if confirm == nil {
		t.Fatal("missing pending bash confirm")
	}
	t.Cleanup(func() { agentbuiltin.ClearBashApproval("ses_confirm_owner") })
	m := model{
		app:                         newTestLocalApp(t, t.TempDir()),
		input:                       textinput.New(),
		sessionID:                   "ses_different_active",
		pendingToolConfirm:          confirm,
		pendingToolConfirmReviewFix: false,
		pendingQuestion:             confirm.Question,
		pendingOptions:              append([]string(nil), confirm.Options...),
		pendingChoice:               0,
		busy:                        true,
		status:                      "running",
		width:                       100,
		height:                      24,
		viewport:                    viewport.New(100, 10),
		items:                       []transcriptItem{{role: "run-active", text: "Working"}},
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	got := next.(model)

	if cmd == nil {
		t.Fatal("Ctrl+A returned nil command, want approved bash command")
	}
	if last := got.items[len(got.items)-1]; last.role != "event" || !strings.Contains(last.text, "auto-approved bash: printf session") {
		t.Fatalf("last item = %#v, want auto-approved event", last)
	}
}

func TestAutoBashPermissionEventApprovesWithoutModal(t *testing.T) {
	ctx, holder := agentbuiltin.NewToolUseConfirmHolder(context.Background())
	ctx = context.WithValue(ctx, agentbuiltin.SubAgentParentKey, "ses_auto_event")
	tool := agentbuiltin.NewShellTool(agentbuiltin.ShellConfig{})
	if _, err := tool.InvokableRun(ctx, `{"command":"printf event"}`); err != nil {
		t.Fatal(err)
	}
	confirm := holder.Get()
	if confirm == nil {
		t.Fatal("missing pending bash confirm")
	}
	t.Cleanup(func() { agentbuiltin.ClearBashApproval("ses_auto_event") })
	m := model{
		app:             newTestLocalApp(t, t.TempDir()),
		input:           textinput.New(),
		sessionID:       "ses_auto_event",
		autoApproveBash: true,
		busy:            true,
		status:          "running",
		width:           100,
		height:          24,
		viewport:        viewport.New(100, 10),
		items:           []transcriptItem{{role: "run-active", text: "Working"}},
	}
	event := agentevent.Event{
		Type:      agentevent.TypePermissionAsked,
		SessionID: "ses_auto_event",
		Data: agentevent.PermissionAskedData{
			RequestID:   confirm.RequestID,
			SessionID:   confirm.SessionID,
			ToolName:    confirm.ToolName,
			ToolUseID:   confirm.ToolUseID,
			Question:    confirm.Question,
			Options:     append([]string(nil), confirm.Options...),
			Input:       map[string]string{"command": confirm.BashCommand},
			BashHash:    confirm.BashHash,
			ChannelName: confirm.ChannelName,
			DeviceID:    confirm.DeviceID,
		},
	}

	next, cmd := m.Update(eventMsg{event: event})
	got := next.(model)

	if cmd == nil {
		t.Fatal("permission event returned nil command, want auto-approved bash command")
	}
	if got.pendingToolConfirm != nil {
		t.Fatalf("pendingToolConfirm = %#v, want no visible modal", got.pendingToolConfirm)
	}
	if !got.busy || got.status != "bash running" {
		t.Fatalf("busy/status = %v/%q, want bash running", got.busy, got.status)
	}
	if last := got.items[len(got.items)-1]; last.role != "event" || !strings.Contains(last.text, "auto-approved bash: printf event") {
		t.Fatalf("last item = %#v, want auto-approved event", last)
	}
}

func TestBusyEnterQueuesUserPrompt(t *testing.T) {
	input := textinput.New()
	input.SetValue("顺手也检查 sitemap")
	m := model{
		input:    input,
		busy:     true,
		status:   "running",
		width:    100,
		height:   24,
		viewport: viewport.New(100, 10),
		items:    []transcriptItem{{role: "assistant", text: "正在执行"}},
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil while queuing", cmd)
	}
	if !got.busy || got.status != "queued 1" {
		t.Fatalf("busy/status = %v/%q, want queued busy state", got.busy, got.status)
	}
	if got.input.Value() != "" {
		t.Fatalf("input = %q, want cleared", got.input.Value())
	}
	if len(got.queuedUserPrompts) != 1 || got.queuedUserPrompts[0] != "顺手也检查 sitemap" {
		t.Fatalf("queuedUserPrompts = %#v, want queued prompt", got.queuedUserPrompts)
	}
	if got.items[len(got.items)-1].role != "user" || got.items[len(got.items)-1].text != "顺手也检查 sitemap" {
		t.Fatalf("last item = %#v, want queued user transcript", got.items[len(got.items)-1])
	}
}

func TestChatDoneStartsQueuedUserPrompt(t *testing.T) {
	oldStart := startChatCmd
	var gotPrompt string
	startChatCmd = func(ctx context.Context, agent *agentruntime.Agent, out chan<- tea.Msg, sessionID, cwd, text string, disabledTools []string) tea.Cmd {
		gotPrompt = text
		return func() tea.Msg { return nil }
	}
	t.Cleanup(func() { startChatCmd = oldStart })
	app := newTestLocalApp(t, t.TempDir())
	m := model{
		app:               app,
		input:             textinput.New(),
		sessionID:         "ses_queue_after_done",
		cwd:               "/tmp/repo",
		chatMsgs:          make(chan tea.Msg, 2),
		events:            make(chan agentevent.Event),
		busy:              true,
		status:            "running",
		width:             100,
		height:            24,
		viewport:          viewport.New(100, 10),
		queuedUserPrompts: []string{"顺手也检查 sitemap", "再看一下静态资源"},
	}
	close(m.events)

	next, cmd := m.Update(chatDoneMsg{reply: "当前任务完成"})
	got := next.(model)

	if cmd == nil {
		t.Fatal("chatDone returned nil cmd, want queued prompt chat")
	}
	if !got.busy || got.status != "running" {
		t.Fatalf("busy/status = %v/%q, want queued chat running", got.busy, got.status)
	}
	if len(got.queuedUserPrompts) != 0 {
		t.Fatalf("queuedUserPrompts = %#v, want drained", got.queuedUserPrompts)
	}
	if !strings.Contains(gotPrompt, "顺手也检查 sitemap") || !strings.Contains(gotPrompt, "再看一下静态资源") {
		t.Fatalf("queued prompt = %q, want drained user prompts", gotPrompt)
	}
}

func TestBashApprovalFollowupAppendsQueuedUserPrompt(t *testing.T) {
	oldStart := startChatCmd
	var gotPrompt string
	startChatCmd = func(ctx context.Context, agent *agentruntime.Agent, out chan<- tea.Msg, sessionID, cwd, text string, disabledTools []string) tea.Cmd {
		gotPrompt = text
		return func() tea.Msg { return nil }
	}
	t.Cleanup(func() { startChatCmd = oldStart })
	m := model{
		app:               newTestLocalApp(t, t.TempDir()),
		input:             textinput.New(),
		sessionID:         "ses_bash_queue",
		chatMsgs:          make(chan tea.Msg, 2),
		events:            make(chan agentevent.Event),
		queuedUserPrompts: []string{"顺手把测试也跑了"},
		width:             100,
		height:            24,
		viewport:          viewport.New(100, 10),
	}
	close(m.events)

	next, cmd := m.Update(bashApprovalDoneMsg{
		command:   "git status --short",
		output:    " M a.go\n",
		sessionID: "ses_bash_queue",
		toolUseID: "toolu_bash_queue",
	})
	got := next.(model)

	if cmd == nil {
		t.Fatal("bashApprovalDone returned nil cmd, want followup chat")
	}
	if len(got.queuedUserPrompts) != 0 {
		t.Fatalf("queuedUserPrompts = %#v, want drained", got.queuedUserPrompts)
	}
	if !strings.Contains(gotPrompt, "[TOOL_RESULT]") || !strings.Contains(gotPrompt, "git status --short") || !strings.Contains(gotPrompt, "顺手把测试也跑了") {
		t.Fatalf("bash followup prompt = %q, want tool result plus queued prompt", gotPrompt)
	}
}

func TestShellDoneStartsQueuedUserPrompt(t *testing.T) {
	oldStart := startChatCmd
	var gotPrompt string
	startChatCmd = func(ctx context.Context, agent *agentruntime.Agent, out chan<- tea.Msg, sessionID, cwd, text string, disabledTools []string) tea.Cmd {
		gotPrompt = text
		return func() tea.Msg { return nil }
	}
	t.Cleanup(func() { startChatCmd = oldStart })
	m := model{
		app:               newTestLocalApp(t, t.TempDir()),
		input:             textinput.New(),
		sessionID:         "ses_shell_queue",
		chatMsgs:          make(chan tea.Msg, 2),
		events:            make(chan agentevent.Event),
		queuedUserPrompts: []string{"shell 后继续总结"},
		busy:              true,
		status:            "shell running",
		width:             100,
		height:            24,
		viewport:          viewport.New(100, 10),
	}
	close(m.events)

	next, cmd := m.Update(shellDoneMsg{command: "pwd", output: "/tmp\n"})
	got := next.(model)

	if cmd == nil {
		t.Fatal("shellDone returned nil cmd, want queued prompt chat")
	}
	if !got.busy || got.status != "running" {
		t.Fatalf("busy/status = %v/%q, want queued chat running", got.busy, got.status)
	}
	if !strings.Contains(gotPrompt, "shell 后继续总结") {
		t.Fatalf("queued prompt = %q, want shell queued prompt", gotPrompt)
	}
}

func TestPendingAskPanelRendersNearInputWithoutViewportSync(t *testing.T) {
	input := textinput.New()
	m := model{
		input:           input,
		pendingQuestion: "是否允许执行命令：git status --short",
		pendingOptions:  []string{"允许一次 :: git status --short", "拒绝"},
		pendingChoice:   0,
		width:           90,
		height:          24,
		viewport:        viewport.New(90, 10),
	}
	m.viewport.SetContent("等待你确认命令。")

	got := stripTestANSI(m.View())

	for _, want := range []string{"是否允许执行命令", "› 1. 允许一次", "2. 拒绝"} {
		if !strings.Contains(got, want) {
			t.Fatalf("View() = %q, want pending approval panel containing %q", got, want)
		}
	}
}

func TestPendingBashConfirmRendersFixedApprovalModal(t *testing.T) {
	input := textinput.New()
	m := model{
		input:         input,
		pendingChoice: 0,
		width:         100,
		height:        28,
		viewport:      viewport.New(100, 10),
		items:         []transcriptItem{{role: "assistant", text: strings.Repeat("history\n", 20)}},
	}
	m.setPendingToolConfirm(&agentbuiltin.PendingToolUseConfirm{
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_modal",
		BashHash:    "hash_modal",
		BashCommand: "newrelic nrql query --accountId 6119564 --query SELECT",
		Question:    "是否允许执行命令：newrelic nrql query --accountId 6119564 --query SELECT",
		Options: []string{
			"允许一次 :: newrelic nrql query --accountId 6119564 --query SELECT",
			"本会话允许此命令 :: newrelic nrql query --accountId 6119564 --query SELECT",
			"拒绝",
		},
	}, false)
	m.syncViewport(true)

	got := stripTestANSI(m.View())

	for _, want := range []string{"BASH APPROVAL", "toolu_bash_modal", "newrelic nrql query", "› 1. 允许一次", "2. 自动通过本轮并执行"} {
		if !strings.Contains(got, want) {
			t.Fatalf("View() = %q, want fixed approval modal containing %q", got, want)
		}
	}
	if strings.Contains(stripTestANSI(m.viewport.View()), "BASH APPROVAL") {
		t.Fatalf("viewport contains approval modal; want modal outside transcript viewport")
	}
}

func TestViewKeepsApprovalModalVisibleWithLongTranscript(t *testing.T) {
	input := textinput.New()
	m := model{
		input: input,
		pendingToolConfirm: &agentbuiltin.PendingToolUseConfirm{
			ToolName:    "bash",
			ToolUseID:   "toolu_bash_visible",
			BashHash:    "hash_visible",
			BashCommand: "cd /Users/mnhkahn/code/cyeam_web && grep -rln 'HOMEHEAD' . --include='*.html'",
		},
		pendingQuestion: "是否允许执行命令：cd /Users/mnhkahn/code/cyeam_web && grep -rln 'HOMEHEAD' . --include='*.html'",
		pendingOptions: []string{
			"允许一次 :: cd /Users/mnhkahn/code/cyeam_web && grep -rln 'HOMEHEAD' . --include='*.html'",
			"拒绝",
		},
		pendingChoice: 0,
		width:         100,
		height:        24,
		viewport:      viewport.New(100, 10),
		items: []transcriptItem{
			{role: "assistant", text: strings.Repeat("很长的历史输出\n", 80)},
			{role: "system", text: strings.Repeat("等待你确认执行 bash 命令\n\n```bash\ngit status --short\n```\n\n", 5)},
		},
	}
	m.syncViewport(true)

	view := stripTestANSI(m.View())
	lines := strings.Split(view, "\n")

	if len(lines) > m.height {
		t.Fatalf("View() rendered %d lines, want at most terminal height %d\n%s", len(lines), m.height, view)
	}
	for _, want := range []string{"BASH APPROVAL", "toolu_bash_visible", "› 1. 允许一次", ">"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() = %q, want visible %q", view, want)
		}
	}
}

func TestPendingAskPanelRendersOnlyOnceInFullView(t *testing.T) {
	input := textinput.New()
	m := model{
		input:           input,
		items:           []transcriptItem{{role: "system", text: pendingBashApprovalText}},
		pendingQuestion: "是否允许执行命令：git status --short",
		pendingOptions:  []string{"允许一次 :: git status --short", "拒绝"},
		pendingChoice:   0,
		width:           90,
		height:          24,
		viewport:        viewport.New(90, 10),
	}
	m.syncViewport(true)

	got := stripTestANSI(m.View())

	if count := strings.Count(got, "是否允许执行命令"); count != 1 {
		t.Fatalf("View() rendered pending approval %d times, want once:\n%s", count, got)
	}
	if !strings.Contains(got, pendingBashApprovalText) {
		t.Fatalf("View() = %q, want transcript pending status", got)
	}
}

func TestPendingToolUseConfirmReplacesBashPlaceholderInTranscript(t *testing.T) {
	m := model{
		items: []transcriptItem{
			{role: "run-active", text: "Aligning"},
			{role: "assistant", text: "等待你确认命令。"},
			{role: "run-done", text: "Delivered"},
		},
	}

	m.markPendingToolUseConfirmInTranscript(&agentbuiltin.PendingToolUseConfirm{
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_test",
		Question:    "是否允许执行命令：git status --short",
		Options:     []string{"允许一次 :: git status --short", "拒绝"},
		BashHash:    "hash",
		BashCommand: "git status --short",
	})

	foundPrompt := false
	for _, item := range m.items {
		if item.role == "assistant" && isBashApprovalPlaceholder(item.text) {
			t.Fatalf("placeholder was left visible in transcript: %#v", m.items)
		}
		if item.role == "system" && strings.Contains(item.text, "toolu_bash_test") && strings.Contains(item.text, "git status --short") {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("items = %#v, want local pending approval prompt", m.items)
	}
}

func TestPendingToolUseConfirmAppendsAfterAssistantExplanation(t *testing.T) {
	m := model{
		items: []transcriptItem{
			{role: "run-active", text: "Aligning"},
			{role: "assistant", text: "字段名可能不对，先看 PageViewTiming 有哪些字段："},
			{role: "run-done", text: "Delivered"},
		},
	}

	m.markPendingToolUseConfirmInTranscript(&agentbuiltin.PendingToolUseConfirm{
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_nrql",
		Question:    "是否允许执行命令：newrelic nrql query --accountId 6119564",
		Options:     []string{"允许一次 :: newrelic nrql query --accountId 6119564", "拒绝"},
		BashHash:    "hash",
		BashCommand: "newrelic nrql query --accountId 6119564",
	})

	if len(m.items) != 4 {
		t.Fatalf("items len = %d, want appended pending item: %#v", len(m.items), m.items)
	}
	last := m.items[len(m.items)-1]
	if last.role != "system" || !strings.Contains(last.text, "toolu_bash_nrql") || !strings.Contains(last.text, "newrelic nrql query") {
		t.Fatalf("last item = %#v, want visible pending bash approval", last)
	}
	if !strings.Contains(m.items[1].text, "字段名可能不对") {
		t.Fatalf("assistant explanation was lost: %#v", m.items)
	}
}

func TestPermissionAskedEventLoadsPendingToolConfirm(t *testing.T) {
	m := model{
		input:    textinput.New(),
		width:    90,
		height:   24,
		viewport: viewport.New(90, 10),
		events:   make(chan agentevent.Event),
		items: []transcriptItem{
			{role: "run-active", text: "Aligning"},
			{role: "assistant", text: "只截到 string 字段，数值指标在下面。换个查法："},
			{role: "run-done", text: "Delivered"},
		},
	}
	close(m.events)

	next, _ := m.Update(eventMsg{event: agentevent.Event{
		Type:      agentevent.TypePermissionAsked,
		SessionID: "ses_evt_confirm",
		Data: agentevent.PermissionAskedData{
			RequestID:   "toolu_bash_evt",
			SessionID:   "ses_evt_confirm",
			ToolName:    "bash",
			ToolUseID:   "toolu_bash_evt",
			Question:    "是否允许执行命令：newrelic nrql query",
			Options:     []string{"允许一次 :: newrelic nrql query", "拒绝"},
			Input:       map[string]string{"command": "newrelic nrql query"},
			BashHash:    "hash_evt",
			ChannelName: channelName,
			DeviceID:    channelUser,
		},
	}})
	got := next.(model)

	if !got.hasPendingBashConfirm() {
		t.Fatalf("pending confirm missing after permission event: %#v", got.pendingToolConfirm)
	}
	if got.pendingToolConfirm.BashHash != "hash_evt" {
		t.Fatalf("BashHash = %q, want event hash", got.pendingToolConfirm.BashHash)
	}
	if got.status != "waiting approval" {
		t.Fatalf("status = %q, want waiting approval", got.status)
	}
	foundPrompt := false
	for _, item := range got.items {
		if item.role == "system" && strings.Contains(item.text, "toolu_bash_evt") && strings.Contains(item.text, "newrelic nrql query") {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("items = %#v, want visible pending approval prompt", got.items)
	}
}

func TestChatDoneInvalidatesFakeBashApprovalWithoutPendingConfirm(t *testing.T) {
	app := newTestLocalApp(t, t.TempDir())
	m := model{
		app:            app,
		input:          textinput.New(),
		width:          90,
		height:         24,
		viewport:       viewport.New(90, 10),
		streamingIndex: -1,
		items: []transcriptItem{
			{role: "run-active", text: "Aligning"},
			{role: "run-done", text: "Delivered"},
		},
	}
	fakeApproval := "等待你确认执行 bash 命令\n\ntool_use_id: toolu_bash_fake\n\n```bash\nnewrelic nrql query\n```\n\n请在下方审批面板选择允许或拒绝。"

	next, _ := m.Update(chatDoneMsg{reply: fakeApproval})
	got := next.(model)

	if got.hasPendingBashConfirm() {
		t.Fatalf("fake approval created pending confirm: %#v", got.pendingToolConfirm)
	}
	last := got.items[len(got.items)-1]
	if last.role != "system" || !strings.Contains(last.text, "模型没有真正调用 bash 工具") || !strings.Contains(last.text, "newrelic nrql query") {
		t.Fatalf("last item = %#v, want invalid fake approval warning with command", last)
	}
}

func TestChatDoneRetriesProseBashApprovalWithoutPendingConfirm(t *testing.T) {
	app := newTestLocalApp(t, t.TempDir())
	m := model{
		app:            app,
		input:          textinput.New(),
		sessionID:      "ses_fake_retry",
		cwd:            "/Users/mnhkahn/code/cyeam_web",
		width:          90,
		height:         24,
		viewport:       viewport.New(90, 10),
		chatMsgs:       make(chan tea.Msg, 4),
		events:         make(chan agentevent.Event),
		streamingIndex: -1,
		items: []transcriptItem{
			{role: "run-active", text: "Aligning"},
			{role: "run-done", text: "Delivered"},
		},
	}
	close(m.events)
	reply := "模板 define 可能在别处，或者用了不同语法。搜宽一点：\n\n```bash\ncd /Users/mnhkahn/code/cyeam_web && grep -rln 'HOMEHEAD' . --include='*.html' --include='*.tmpl' 2>/dev/null | head -20\n```\n\n批准即跑。"

	next, cmd := m.Update(chatDoneMsg{reply: reply})
	got := next.(model)

	if cmd == nil {
		t.Fatal("Update() returned nil cmd, want automatic retry that asks model to call bash tool")
	}
	if !got.busy || got.status != "running" {
		t.Fatalf("busy/status = %v/%q, want running retry", got.busy, got.status)
	}
	last := got.items[len(got.items)-1]
	if last.role != "system" || !strings.Contains(last.text, "模型没有真正调用 bash 工具") || !strings.Contains(last.text, "批准即跑") {
		t.Fatalf("last item = %#v, want invalid prose approval warning", last)
	}
}

func TestChatDoneInvalidatesPanelWaitWithoutPendingConfirm(t *testing.T) {
	app := newTestLocalApp(t, t.TempDir())
	m := model{
		app:            app,
		input:          textinput.New(),
		width:          90,
		height:         24,
		viewport:       viewport.New(90, 10),
		streamingIndex: -1,
		items: []transcriptItem{
			{role: "run-active", text: "Aligning"},
			{role: "run-done", text: "Delivered"},
		},
	}

	next, _ := m.Update(chatDoneMsg{reply: "等你在审批面板点允许。"})
	got := next.(model)

	if got.hasPendingBashConfirm() {
		t.Fatalf("panel wait created pending confirm: %#v", got.pendingToolConfirm)
	}
	last := got.items[len(got.items)-1]
	if last.role != "system" || !strings.Contains(last.text, "模型没有真正调用 bash 工具") || !strings.Contains(last.text, "审批面板") {
		t.Fatalf("last item = %#v, want invalid panel wait warning", last)
	}
}

func TestChatDoneConsumesPendingToolConfirmBySessionID(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	sessionID, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	app.Agent.StoreToolUseConfirmForTest(context.Background(), "local", &agentbuiltin.PendingToolUseConfirm{
		RequestID:   "toolu_bash_session",
		SessionID:   sessionID,
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_session",
		Question:    "是否允许执行命令：git status --short",
		Options:     []string{"允许一次 :: git status --short", "拒绝"},
		BashHash:    "hash",
		BashCommand: "git status --short",
	})
	m := model{
		app:       app,
		input:     textinput.New(),
		sessionID: sessionID,
		width:     90,
		height:    24,
		viewport:  viewport.New(90, 10),
		items: []transcriptItem{
			{role: "run-active", text: "Aligning"},
			{role: "assistant", text: "等待你确认执行 bash 命令\n\ntool_use_id: toolu_bash_session\n\n```bash\ngit status --short\n```\n\n请在下方审批面板选择允许或拒绝。"},
			{role: "run-done", text: "Delivered"},
		},
	}

	next, _ := m.Update(chatDoneMsg{reply: m.items[1].text})
	got := next.(model)

	if !got.hasPendingBashConfirm() {
		t.Fatalf("pending confirm missing after chatDone: %#v", got.pendingToolConfirm)
	}
	if got.pendingQuestion == "" || len(got.pendingOptions) == 0 {
		t.Fatalf("pending panel state = question %q options %#v", got.pendingQuestion, got.pendingOptions)
	}
	view := stripTestANSI(got.View())
	if !strings.Contains(view, "› 1. 允许一次") || !strings.Contains(view, "2. 自动通过本轮并执行") || !strings.Contains(view, "3. 拒绝") {
		t.Fatalf("View() = %q, want pending approval panel", view)
	}
}

func TestChatDoneQueuesMultiplePendingToolConfirms(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	sessionID, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	app.Agent.StoreToolUseConfirmForTest(context.Background(), "local", &agentbuiltin.PendingToolUseConfirm{
		RequestID:   "toolu_bash_first",
		SessionID:   sessionID,
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_first",
		Question:    "是否允许执行命令：git status --short",
		Options:     []string{"允许一次 :: git status --short", "拒绝"},
		BashHash:    "hash_first",
		BashCommand: "git status --short",
	})
	app.Agent.StoreToolUseConfirmForTest(context.Background(), "local", &agentbuiltin.PendingToolUseConfirm{
		RequestID:   "toolu_bash_second",
		SessionID:   sessionID,
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_second",
		Question:    "是否允许执行命令：git diff --stat",
		Options:     []string{"允许一次 :: git diff --stat", "拒绝"},
		BashHash:    "hash_second",
		BashCommand: "git diff --stat",
	})
	m := model{
		app:       app,
		input:     textinput.New(),
		sessionID: sessionID,
		width:     90,
		height:    24,
		viewport:  viewport.New(90, 10),
	}

	next, _ := m.Update(chatDoneMsg{reply: "我需要跑两个命令。"})
	got := next.(model)

	if got.pendingToolConfirm == nil || got.pendingToolConfirm.ToolUseID != "toolu_bash_first" {
		t.Fatalf("current pending = %#v, want first confirm", got.pendingToolConfirm)
	}
	if len(got.pendingToolConfirmQueue) != 1 || got.pendingToolConfirmQueue[0].confirm.ToolUseID != "toolu_bash_second" {
		t.Fatalf("queue = %#v, want second confirm queued", got.pendingToolConfirmQueue)
	}
	modal := stripTestANSI(renderPendingToolConfirmModal(got.pendingToolConfirm, got.pendingQuestion, got.pendingOptions, got.pendingChoice, 90))
	if !strings.Contains(modal, "git status --short") || strings.Contains(modal, "git diff --stat") {
		t.Fatalf("modal = %q, want only current approval in modal", modal)
	}
}

func TestPendingToolConfirmQueuePromotesAfterCurrentClear(t *testing.T) {
	m := model{
		pendingToolConfirm: &agentbuiltin.PendingToolUseConfirm{
			SessionID: "ses_queue",
			ToolName:  "bash",
			ToolUseID: "toolu_bash_first",
			BashHash:  "hash_first",
		},
		pendingToolConfirmQueue: []pendingToolConfirmQueueItem{{
			confirm: &agentbuiltin.PendingToolUseConfirm{
				SessionID:   "ses_queue",
				ToolName:    "bash",
				ToolUseID:   "toolu_bash_second",
				BashHash:    "hash_second",
				Question:    "是否允许执行命令：git diff --stat",
				Options:     []string{"允许一次 :: git diff --stat", "拒绝"},
				BashCommand: "git diff --stat",
			},
			reviewFix: true,
		}},
	}

	m.clearPendingToolConfirmState("ses_queue", false)

	if m.pendingToolConfirm == nil || m.pendingToolConfirm.ToolUseID != "toolu_bash_second" {
		t.Fatalf("current pending = %#v, want second confirm promoted", m.pendingToolConfirm)
	}
	if !m.pendingToolConfirmReviewFix {
		t.Fatalf("pendingToolConfirmReviewFix = false, want queued reviewFix preserved")
	}
	if len(m.pendingToolConfirmQueue) != 0 {
		t.Fatalf("queue len = %d, want empty", len(m.pendingToolConfirmQueue))
	}
}

func TestPermissionAskedEventDeduplicatesPendingToolConfirm(t *testing.T) {
	m := model{
		input:    textinput.New(),
		width:    90,
		height:   24,
		viewport: viewport.New(90, 10),
		events:   make(chan agentevent.Event),
	}
	close(m.events)
	event := agentevent.Event{
		Type:      agentevent.TypePermissionAsked,
		SessionID: "ses_evt_confirm",
		Data: agentevent.PermissionAskedData{
			RequestID:   "toolu_bash_evt",
			SessionID:   "ses_evt_confirm",
			ToolName:    "bash",
			ToolUseID:   "toolu_bash_evt",
			Question:    "是否允许执行命令：newrelic nrql query",
			Options:     []string{"允许一次 :: newrelic nrql query", "拒绝"},
			Input:       map[string]string{"command": "newrelic nrql query"},
			BashHash:    "hash_evt",
			ChannelName: channelName,
			DeviceID:    channelUser,
		},
	}

	next, _ := m.Update(eventMsg{event: event})
	got, _ := next.(model).Update(eventMsg{event: event})
	modelAfterDuplicate := got.(model)

	if modelAfterDuplicate.pendingToolConfirm == nil || modelAfterDuplicate.pendingToolConfirm.ToolUseID != "toolu_bash_evt" {
		t.Fatalf("pending confirm = %#v, want event confirm", modelAfterDuplicate.pendingToolConfirm)
	}
	if len(modelAfterDuplicate.pendingToolConfirmQueue) != 0 {
		t.Fatalf("queue len = %d, want duplicate ignored", len(modelAfterDuplicate.pendingToolConfirmQueue))
	}
}

func TestSyncViewportRefreshesWhenNonLastItemChanges(t *testing.T) {
	m := model{
		width:    80,
		height:   24,
		viewport: viewport.New(80, 10),
		items: []transcriptItem{
			{role: "run-active", text: "Aligning"},
			{role: "assistant", text: "给"},
			{role: "run-done", text: "Delivered"},
		},
	}
	m.syncViewport(true)
	m.items[1].text = "给 8 个带 `bg-fixed-layer` 的页面换成 CSS + preload，首页/pin/geek 直接受益。"

	m.syncViewport(true)

	got := stripTestANSI(m.viewport.View())
	if !strings.Contains(got, "bg-fixed-layer") {
		t.Fatalf("viewport = %q, want refreshed non-last assistant content", got)
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
	if later != first {
		t.Fatalf("status phrase changed within one event: %q / %q", first, later)
	}
}

func TestOnlyLatestRunEventAnimates(t *testing.T) {
	first := transcriptItem{role: "run-active", frame: 0}
	second := transcriptItem{role: "run-active"}
	got := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{first, second}, 80, 32))
	if strings.Count(got, "Aligning") != 2 {
		t.Fatalf("rendered active events = %q, want stable labels", got)
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

func TestGitCmsgCommitSuccessSplitsSummaryAndShellOutput(t *testing.T) {
	m := model{
		cwd:      t.TempDir(),
		width:    100,
		height:   30,
		viewport: viewport.New(100, 10),
		busy:     true,
		status:   "git commit && push",
	}
	output := "🔍 检测 localhost:8090 端口状态...\n✅ sitemap 生成成功，加入暂存区...\n[master fb03079] fix(github-stats): 增强 GitHub releases\nTo github.com:mnhkahn/cyeam_web.git\n   48186e2..fb03079  master -> master\n"

	next, _ := m.Update(gitCmsgCommitMsg{output: output, push: true})
	got := next.(model)

	if len(got.items) < 3 {
		t.Fatalf("items = %#v, want run event, summary, and shell output", got.items)
	}
	summary := got.items[len(got.items)-2]
	shell := got.items[len(got.items)-1]
	if summary.role != "system" || summary.text != "提交并推送完成。" {
		t.Fatalf("summary item = %#v, want system summary only", summary)
	}
	if shell.role != "shell" || shell.text != strings.TrimSpace(output) {
		t.Fatalf("shell item = %#v, want trimmed shell output", shell)
	}
}

func TestCodexReviewArgsDefaultToUncommitted(t *testing.T) {
	got, err := codexReviewArgs("")
	if err != nil {
		t.Fatalf("codexReviewArgs() error = %v", err)
	}
	if len(got) != 2 || got[0] != "review" || got[1] != "--uncommitted" {
		t.Fatalf("codexReviewArgs(empty) = %#v", got)
	}
	got, err = codexReviewArgs("重点看看 TUI 复制")
	if err != nil {
		t.Fatalf("codexReviewArgs(prompt) error = %v", err)
	}
	if len(got) != 2 || got[0] != "review" || !strings.Contains(got[1], "重点看看 TUI 复制") || !strings.Contains(got[1], "请用中文输出") {
		t.Fatalf("codexReviewArgs(prompt) = %#v", got)
	}
}

func TestCodexReviewArgsPreserveExplicitTarget(t *testing.T) {
	got, err := codexReviewArgs("--base main 重点看看 TUI 复制")
	if err != nil {
		t.Fatalf("codexReviewArgs(base) error = %v", err)
	}
	if len(got) != 4 || got[0] != "review" || got[1] != "--base" || got[2] != "main" || !strings.Contains(got[3], "重点看看 TUI 复制") || !strings.Contains(got[3], "请用中文输出") {
		t.Fatalf("codexReviewArgs(base) = %#v", got)
	}
	got, err = codexReviewArgs("--commit abc123")
	if err != nil {
		t.Fatalf("codexReviewArgs(commit) error = %v", err)
	}
	if len(got) != 3 || got[0] != "review" || got[1] != "--commit" || got[2] != "abc123" {
		t.Fatalf("codexReviewArgs(commit) = %#v", got)
	}
}

func TestReviewSlashStartsCodexReviewRunEvent(t *testing.T) {
	input := textinput.New()
	input.SetValue("/review")
	m := model{
		input:    input,
		width:    80,
		height:   24,
		viewport: viewport.New(80, 5),
		cwd:      "/tmp/repo",
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if cmd == nil {
		t.Fatalf("/review returned nil command")
	}
	if !got.busy || got.status != "review" || got.input.Value() != "" {
		t.Fatalf("/review state busy=%v status=%q input=%q", got.busy, got.status, got.input.Value())
	}
	if len(got.items) == 0 || got.items[len(got.items)-1].role != "run-active" || got.items[len(got.items)-1].text != "Codex review 1/3" {
		t.Fatalf("/review items = %#v, want Codex review 1/3 run event", got.items)
	}
}

func TestCodexReviewDoneAppendsResult(t *testing.T) {
	m := model{busy: true, status: "review", runPulseActive: true}
	next, _ := m.Update(codexReviewDoneMsg{output: "LGTM\n", args: []string{"review", "--uncommitted"}})
	got := next.(model)
	if got.busy || got.status != "idle" || got.runPulseActive {
		t.Fatalf("review done state busy=%v status=%q active=%v", got.busy, got.status, got.runPulseActive)
	}
	if len(got.items) != 2 {
		t.Fatalf("review done items = %#v, want run-done and assistant", got.items)
	}
	if got.items[0].role != "run-done" || got.items[1].role != "assistant" || !strings.Contains(got.items[1].text, "LGTM") {
		t.Fatalf("review done items = %#v", got.items)
	}
}

func TestCodexReviewLoopsIntoFixAndNextRound(t *testing.T) {
	oldStartChat := startReviewFixChatCmd
	defer func() { startReviewFixChatCmd = oldStartChat }()
	startReviewFixChatCmd = func(ctx context.Context, agent *agentruntime.Agent, out chan<- tea.Msg, sessionID, cwd, text string, disabledTools []string) tea.Cmd {
		if !strings.Contains(text, "Fix the following Codex review findings") {
			t.Fatalf("fix prompt = %q, want review findings", text)
		}
		return func() tea.Msg { return chatDoneMsg{reply: "fixed", reviewFix: true} }
	}

	m := model{
		busy:           true,
		status:         "review",
		runPulseActive: true,
		reviewLoop:     codexReviewLoop{Active: true, Round: 1, MaxRounds: 3, Args: []string{"review", "--uncommitted"}, CWD: "/tmp/repo"},
	}
	next, cmd := m.Update(codexReviewDoneMsg{output: "[P1] fix bug", args: []string{"review", "--uncommitted"}})
	got := next.(model)
	if cmd == nil {
		t.Fatalf("review findings returned nil fix command")
	}
	if !got.busy || got.status != "review fix" || got.reviewLoop.Round != 1 {
		t.Fatalf("review fix state busy=%v status=%q loop=%#v", got.busy, got.status, got.reviewLoop)
	}
	if len(got.items) < 2 || got.items[len(got.items)-2].role != "assistant" || !strings.Contains(got.items[len(got.items)-2].text, "第 1 轮审查发现问题") || !strings.Contains(got.items[len(got.items)-2].text, "[P1] fix bug") {
		t.Fatalf("review findings not printed before fix: %#v", got.items)
	}
	next, cmd = got.Update(chatDoneMsg{reply: "fixed", reviewFix: true})
	got = next.(model)
	if cmd == nil {
		t.Fatalf("review fix completion returned nil next review command")
	}
	if !got.busy || got.status != "review" || got.reviewLoop.Round != 2 {
		t.Fatalf("next review state busy=%v status=%q loop=%#v", got.busy, got.status, got.reviewLoop)
	}
	if got.items[len(got.items)-1].role != "run-active" || got.items[len(got.items)-1].text != "Codex review 2/3" {
		t.Fatalf("items = %#v, want round 2 active event", got.items)
	}
}

func TestCodexReviewFixPromptTruncatesLargeFindings(t *testing.T) {
	large := strings.Repeat("审查问题", 10000)
	prompt := codexReviewFixPrompt(large, codexReviewLoop{Round: 1, MaxRounds: 3})
	if !strings.Contains(prompt, "已截断") {
		t.Fatalf("fix prompt missing truncation marker")
	}
	if len([]rune(prompt)) > maxCodexReviewFixPromptChars+1000 {
		t.Fatalf("fix prompt length = %d, want bounded", len([]rune(prompt)))
	}
}

func TestReviewFixBashFollowupKeepsReviewLoop(t *testing.T) {
	oldStartChat := startReviewFixChatCmd
	defer func() { startReviewFixChatCmd = oldStartChat }()
	called := false
	startReviewFixChatCmd = func(ctx context.Context, agent *agentruntime.Agent, out chan<- tea.Msg, sessionID, cwd, text string, disabledTools []string) tea.Cmd {
		called = true
		if !strings.Contains(text, "[TOOL_RESULT]") {
			t.Fatalf("bash followup prompt = %q, want tool result", text)
		}
		return nil
	}

	m := model{
		app:        newTestLocalApp(t, t.TempDir()),
		chatMsgs:   make(chan tea.Msg, 1),
		busy:       true,
		status:     "bash running",
		cwd:        "/tmp/repo",
		sessionID:  "ses-review",
		reviewLoop: codexReviewLoop{Active: true, Round: 1, MaxRounds: 3, Args: []string{"review", "--uncommitted", codexReviewChinesePrompt}, CWD: "/tmp/repo"},
	}

	_, cmd := m.Update(bashApprovalDoneMsg{
		command:   "grep -n _seo controllers/base_controller.go",
		output:    "84: if _, exists := m[\"_seo\"]; !exists {",
		sessionID: "ses-review",
		toolUseID: "toolu_bash_review",
		reviewFix: true,
	})
	if cmd == nil {
		t.Fatalf("bash approval done returned nil command")
	}
	if !called {
		t.Fatalf("bash followup did not use review-fix chat path")
	}
}

func TestCtrlCPrioritizedOverExplorerAndPicker(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    model
	}{
		{
			name: "explorer",
			m: model{
				input:    textinput.New(),
				explorer: &tuiExplorer{mode: explorerDiff},
			},
		},
		{
			name: "workspace picker",
			m: model{
				input:           textinput.New(),
				workspacePicker: &workspacePicker{},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, cmd := tc.m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			got := next.(model)
			if cmd == nil {
				t.Fatalf("ctrl+c returned nil command")
			}
			if !got.quitting || got.busy {
				t.Fatalf("ctrl+c model quitting=%v busy=%v", got.quitting, got.busy)
			}
		})
	}
}

func TestChatTimeoutStopsBusyRun(t *testing.T) {
	canceled := false
	m := model{
		busy:           true,
		status:         "running",
		chatRunID:      7,
		activeCancel:   func() { canceled = true },
		runPulseActive: true,
		reviewLoop:     codexReviewLoop{Active: true, Round: 1, MaxRounds: 3},
	}

	next, cmd := m.Update(chatTimeoutMsg{runID: 7})
	got := next.(model)
	if cmd == nil {
		t.Fatalf("timeout returned nil command, want waitForEvent")
	}
	if !canceled {
		t.Fatalf("timeout did not cancel active call")
	}
	if got.busy || got.status != "idle" || got.runPulseActive || got.reviewLoop.Active {
		t.Fatalf("timeout model busy=%v status=%q pulse=%v review=%#v", got.busy, got.status, got.runPulseActive, got.reviewLoop)
	}
	if len(got.items) == 0 || got.items[len(got.items)-1].role != "error" {
		t.Fatalf("timeout items = %#v, want error", got.items)
	}
}

func TestStaleChatDoneCannotCancelActiveChat(t *testing.T) {
	staleCanceled := false
	activeCanceled := false
	m := model{
		busy:            true,
		status:          "running",
		chatRunID:       2,
		activeChatRunID: 2,
		activeCancel:    func() { activeCanceled = true },
	}

	next, _ := m.Update(chatDoneMsg{runID: 1, cancel: func() { staleCanceled = true }})
	got := next.(model)
	if !staleCanceled {
		t.Fatal("stale chat did not release its own context")
	}
	if activeCanceled {
		t.Fatal("stale chat canceled the active chat")
	}
	if got.activeChatRunID != 2 || !got.busy {
		t.Fatalf("active chat was changed: runID=%d busy=%v", got.activeChatRunID, got.busy)
	}
}

func TestBashFollowupWaitsForActiveChat(t *testing.T) {
	oldStart := startChatCmd
	var started int
	startChatCmd = func(ctx context.Context, agent *agentruntime.Agent, out chan<- tea.Msg, sessionID, cwd, text string, disabledTools []string) tea.Cmd {
		started++
		return func() tea.Msg { return nil }
	}
	t.Cleanup(func() { startChatCmd = oldStart })
	m := model{
		app:             newTestLocalApp(t, t.TempDir()),
		input:           textinput.New(),
		busy:            true,
		status:          "running",
		chatRunID:       1,
		activeChatRunID: 1,
		chatMsgs:        make(chan tea.Msg, 2),
		events:          make(chan agentevent.Event),
	}

	next, _ := m.Update(bashApprovalDoneMsg{command: "git status", sessionID: "ses_test", toolUseID: "toolu_1"})
	got := next.(model)
	if started != 0 || len(got.pendingBashFollowups) != 1 {
		t.Fatalf("bash followup started while chat was active: started=%d queue=%d", started, len(got.pendingBashFollowups))
	}

	next, _ = got.Update(chatDoneMsg{runID: 1})
	got = next.(model)
	if started != 1 || len(got.pendingBashFollowups) != 0 || got.activeChatRunID != 2 {
		t.Fatalf("queued followup was not started serially: started=%d queue=%d runID=%d", started, len(got.pendingBashFollowups), got.activeChatRunID)
	}
}

func TestCodexReviewStopsAfterMaxRounds(t *testing.T) {
	m := model{
		busy:           true,
		status:         "review",
		runPulseActive: true,
		reviewLoop:     codexReviewLoop{Active: true, Round: 3, MaxRounds: 3, Args: []string{"review", "--uncommitted"}, CWD: "/tmp/repo"},
	}
	next, cmd := m.Update(codexReviewDoneMsg{output: "[P1] still broken", args: []string{"review", "--uncommitted"}})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("max review returned command, want nil")
	}
	if got.busy || got.status != "idle" || got.reviewLoop.Active {
		t.Fatalf("max review state busy=%v status=%q loop=%#v", got.busy, got.status, got.reviewLoop)
	}
	last := got.items[len(got.items)-1]
	if last.role != "assistant" || !strings.Contains(last.text, "still broken") {
		t.Fatalf("last item = %#v, want final review output", last)
	}
}

func TestToolEventsRenderWorkplaceStatusRows(t *testing.T) {
	start := eventTranscriptItem(agentevent.Event{
		Type: agentevent.TypeAgentToolStarted,
		Data: map[string]any{"name": "bash", "arguments": `{"command":"git status --porcelain=v1"}`},
	})
	done := eventTranscriptItem(agentevent.Event{
		Type: agentevent.TypeAgentToolFinished,
		Data: map[string]any{"name": "bash", "arguments": `{"command":"git status --porcelain=v1"}`},
	})
	failed := eventTranscriptItem(agentevent.Event{
		Type: agentevent.TypeAgentToolFinished,
		Data: map[string]any{"name": "bash", "error": "exit status 1"},
	})
	if start.role != "tool-active" || done.role != "tool-done" || failed.role != "tool-failed" {
		t.Fatalf("tool event roles = %q/%q/%q", start.role, done.role, failed.role)
	}
	got := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{start, done, failed}, 100, 3))
	for _, want := range []string{"Tracing bash: git status --porcelain=v1", "Validated bash: git status --porcelain=v1", "Blocked bash"} {
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

func TestToolEventsRenderFileOperationDetails(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{name: "read_file", args: `{"path":"tui/cmd/xiaoli/main.go"}`, want: "Tracing read_file: tui/cmd/xiaoli/main.go"},
		{name: "edit_file", args: `{"path":"internal/agent/runtime/agent.go"}`, want: "Tracing edit_file: internal/agent/runtime/agent.go"},
		{name: "glob", args: `{"pattern":"**/*.go"}`, want: "Tracing glob: **/*.go"},
		{name: "grep", args: `{"pattern":"toolEvent","glob":"**/*.go"}`, want: `Tracing grep: "toolEvent" in **/*.go`},
		{name: "file_write", args: `{"filename":"report.md"}`, want: "Tracing file_write: report.md"},
		{name: "webfetch", args: `{"url":"https://example.com/article"}`, want: "Tracing webfetch: https://example.com/article"},
		{name: "websearch", args: `{"query":"latest AI news"}`, want: "Tracing websearch: latest AI news"},
	}
	for _, tc := range cases {
		item := eventTranscriptItem(agentevent.Event{
			Type: agentevent.TypeAgentToolStarted,
			Data: map[string]any{"name": tc.name, "arguments": tc.args},
		})
		got := stripTestANSI(renderTranscriptContentWithFrame([]transcriptItem{item}, 100, 3))
		if !strings.Contains(got, tc.want) {
			t.Fatalf("%s render = %q, missing %q", tc.name, got, tc.want)
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
	if cmd == nil {
		t.Fatalf("picker Tab returned nil command, want async git refresh")
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
	if got.gitStatus != "git ..." {
		t.Fatalf("gitStatus = %q, want loading placeholder", got.gitStatus)
	}
}

func TestLocalCDDefersGitRefresh(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	input := textinput.New()
	input.SetValue("/cd child")
	m := model{cwd: root, gitStatus: "old", input: input, width: 100, height: 30, viewport: viewport.New(0, 0)}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if cmd == nil {
		t.Fatalf("/cd returned nil command, want async git refresh")
	}
	if !samePath(got.cwd, child) {
		t.Fatalf("cwd = %q, want %q", got.cwd, child)
	}
	if got.gitStatus != "git ..." {
		t.Fatalf("gitStatus = %q, want loading placeholder", got.gitStatus)
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

func TestWorkspaceTitleFallsBackToFirstUserPrompt(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	sessionID, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	mem := agentruntime.NewLocalMemory(agentruntime.Config{
		StorageBackend: "local",
		LocalDataDir:   dataDir,
	})
	if err := mem.Save(context.Background(), sessionID, []*schema.Message{
		schema.AssistantMessage("ready", nil),
		schema.UserMessage("把默认通道写到配置文件里"),
		schema.AssistantMessage("ok", nil),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got := workspaceTitle(app, sessionID, t.TempDir())

	if got != "把默认通道写到配置文件里" {
		t.Fatalf("workspaceTitle() = %q, want first user prompt", got)
	}
}

func TestRestoreInputHistoryFromSessionUserMessages(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	sessionID, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	mem := agentruntime.NewLocalMemory(agentruntime.Config{
		StorageBackend: "local",
		LocalDataDir:   dataDir,
	})
	if err := mem.Save(context.Background(), sessionID, []*schema.Message{
		schema.UserMessage("第一个问题"),
		schema.AssistantMessage("ok", nil),
		schema.UserMessage("[TOOL_RESULT]\ntool=bash\nstatus=success"),
		schema.UserMessage("第二个问题"),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got := restoreInputHistory(app.Agent, sessionID)

	if len(got) != 2 || got[0] != "第一个问题" || got[1] != "第二个问题" {
		t.Fatalf("restoreInputHistory() = %#v, want restored user prompts only", got)
	}
}

func TestNewModelRestoresSessionInputHistory(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	sessionID, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	mem := agentruntime.NewLocalMemory(agentruntime.Config{
		StorageBackend: "local",
		LocalDataDir:   dataDir,
	})
	if err := mem.Save(context.Background(), sessionID, []*schema.Message{
		schema.UserMessage("恢复后的上一条输入"),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	m := newModel(app, sessionID, "")
	if !m.navigateInputHistory(-1) || m.input.Value() != "恢复后的上一条输入" {
		t.Fatalf("restored input history = %#v input=%q", m.inputHistory, m.input.Value())
	}
}

func TestOpenWorkspacePickerEnrichesDefaultSessionTitle(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	project := t.TempDir()
	sessionID, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	mem := agentruntime.NewLocalMemory(agentruntime.Config{
		StorageBackend: "local",
		LocalDataDir:   dataDir,
	})
	if err := mem.Save(context.Background(), sessionID, []*schema.Message{
		schema.UserMessage("修一下 head.html 的 SEO 字段"),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	statePath := filepath.Join(dataDir, "state", "workspaces.json")
	if err := recordWorkspaceSession(statePath, workspaceItem{
		CWD:        project,
		SessionID:  sessionID,
		Title:      "新会话",
		LastOpened: time.Now(),
	}); err != nil {
		t.Fatalf("recordWorkspaceSession() error = %v", err)
	}
	m := model{
		app:                app,
		cwd:                project,
		width:              120,
		height:             30,
		workspaceStatePath: statePath,
	}

	m.openWorkspacePicker()

	if m.workspacePicker == nil || len(m.workspacePicker.items) != 1 {
		t.Fatalf("workspacePicker = %#v, want one item", m.workspacePicker)
	}
	if got := m.workspacePicker.items[0].Title; got != "修一下 head.html 的 SEO 字段" {
		t.Fatalf("picker title = %q, want first user prompt", got)
	}
}

func TestWorkspacePickerCachesRenderedView(t *testing.T) {
	items := []workspaceItem{
		{CWD: "/Users/test/code/alpha", SessionID: "ses_alpha_123456789", Title: strings.Repeat("中文标题", 12), LastOpened: time.Now()},
		{CWD: "/Users/test/code/beta", SessionID: "ses_beta_123456789", Title: "Beta", LastOpened: time.Now().Add(-time.Hour)},
	}
	p := newWorkspacePicker(items, items[0].CWD, 120, 30)

	first := p.View()
	firstKey := p.viewKey
	if first == "" || firstKey == "" || len(p.labels) != len(items) {
		t.Fatalf("picker cache not populated: view=%q key=%q labels=%#v", first, firstKey, p.labels)
	}
	second := p.View()
	if second != first || p.viewKey != firstKey {
		t.Fatalf("second View missed cache")
	}

	p.move(1)
	if p.viewKey != "" {
		t.Fatalf("move did not invalidate cached view")
	}
	third := p.View()
	if third == first {
		t.Fatalf("selected row changed but rendered view did not")
	}
}

func TestWorkspacePickerCopiesSelectedSessionID(t *testing.T) {
	oldCopy := copyTextToClipboardFunc
	defer func() { copyTextToClipboardFunc = oldCopy }()
	copied := ""
	copyTextToClipboardFunc = func(text string) error {
		copied = text
		return nil
	}

	items := []workspaceItem{
		{CWD: "/Users/test/code/alpha", SessionID: "ses_first_full", Title: "Alpha", LastOpened: time.Now()},
		{CWD: "/Users/test/code/beta", SessionID: "ses_second_full", Title: "Beta", LastOpened: time.Now().Add(-time.Hour)},
	}
	m := model{
		width:           120,
		height:          30,
		workspacePicker: newWorkspacePicker(items, items[0].CWD, 120, 30),
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("Down returned cmd = %#v, want nil", cmd)
	}
	next, cmd = got.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	got = next.(model)
	if cmd != nil {
		t.Fatalf("Ctrl+Y returned cmd = %#v, want nil", cmd)
	}
	if copied != "ses_second_full" {
		t.Fatalf("copied = %q, want full selected session id", copied)
	}
	if got.workspacePicker == nil {
		t.Fatalf("workspace picker closed after copy")
	}
	if view := stripTestANSI(got.workspacePicker.View()); !strings.Contains(view, "Copied session ses_second_full") {
		t.Fatalf("picker feedback = %q, want copied session feedback", view)
	}
}

func TestWorkspaceGitSummaryShowsChangeSize(t *testing.T) {
	dir := t.TempDir()
	if _, err := runGit(dir, "version"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	mustRunGit(t, dir, "init")
	mustRunGit(t, dir, "config", "user.email", "test@example.invalid")
	mustRunGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, dir, "add", "a.txt")
	mustRunGit(t, dir, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("new\nextra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, dir, "add", "b.txt")
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := currentWorkspaceGitSummary(dir)
	for _, want := range []string{"changed 3 files", "+3 -1", "staged 1", "unstaged 1", "untracked 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("currentWorkspaceGitSummary() = %q, want %q", got, want)
		}
	}

	p := newWorkspacePicker([]workspaceItem{{CWD: dir, SessionID: "ses_test", Title: "Repo", LastOpened: time.Now()}}, dir, 120, 30)
	view := stripTestANSI(p.View())
	if strings.Contains(view, "changed 3 files") || strings.Contains(view, "+3 -1") {
		t.Fatalf("workspace picker eagerly rendered git summary:\n%s", view)
	}
	if len(p.gitByCWD) != 0 {
		t.Fatalf("workspace picker eagerly computed git summaries: %#v", p.gitByCWD)
	}
}

func TestParseWorkspaceGitStatus(t *testing.T) {
	got := parseWorkspaceGitStatus(" M a.go\nM  b.go\nA  c.go\n?? d.go\n")
	if got.Files != 4 || got.Staged != 2 || got.Unstaged != 1 || got.Untracked != 1 {
		t.Fatalf("parseWorkspaceGitStatus() = %#v", got)
	}
}

func TestParseGitNumstatSkipsBinaryFiles(t *testing.T) {
	added, deleted := parseGitNumstat("10\t2\ta.go\n-\t-\timage.png\n3\t0\tb.go\n")
	if added != 13 || deleted != 2 {
		t.Fatalf("parseGitNumstat() = +%d -%d, want +13 -2", added, deleted)
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

func TestSwitchWorkspaceUpdatesChannelSession(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	currentDir := t.TempDir()
	nextDir := t.TempDir()
	currentSession, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession(current) error = %v", err)
	}
	nextSession, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession(next) error = %v", err)
	}
	app.Agent.SessionManager().SetChannelSession(context.Background(), channelName, channelUser, currentSession)
	m := model{
		app:                app,
		input:              textinput.New(),
		cwd:                currentDir,
		sessionID:          currentSession,
		width:              100,
		height:             30,
		workspaceStatePath: filepath.Join(dataDir, "state", "workspaces.json"),
	}

	m.switchWorkspace(workspaceItem{CWD: nextDir, SessionID: nextSession, Title: "Next"})

	if !samePath(m.cwd, nextDir) || m.sessionID != nextSession {
		t.Fatalf("switchWorkspace cwd/session = %q/%q, want %q/%q", m.cwd, m.sessionID, nextDir, nextSession)
	}
	if got := app.Agent.SessionManager().GetChannelSession(context.Background(), channelName, channelUser); got != nextSession {
		t.Fatalf("channel session = %q, want %q", got, nextSession)
	}
}

func TestSwitchWorkspaceRestoresInputHistoryForTargetSession(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	currentDir := t.TempDir()
	nextDir := t.TempDir()
	currentSession, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession(current) error = %v", err)
	}
	nextSession, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession(next) error = %v", err)
	}
	mem := agentruntime.NewLocalMemory(agentruntime.Config{
		StorageBackend: "local",
		LocalDataDir:   dataDir,
	})
	if err := mem.Save(context.Background(), nextSession, []*schema.Message{
		schema.UserMessage("目标 session 的历史输入"),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	m := model{
		app:                app,
		input:              textinput.New(),
		cwd:                currentDir,
		sessionID:          currentSession,
		inputHistory:       []string{"当前 session 的历史输入"},
		historyIndex:       1,
		historyDraft:       "draft",
		width:              100,
		height:             30,
		workspaceStatePath: filepath.Join(dataDir, "state", "workspaces.json"),
	}

	m.switchWorkspace(workspaceItem{CWD: nextDir, SessionID: nextSession, Title: "Next"})

	if len(m.inputHistory) != 1 || m.inputHistory[0] != "目标 session 的历史输入" {
		t.Fatalf("inputHistory after switch = %#v, want target session history", m.inputHistory)
	}
	if m.historyIndex != 0 || m.historyDraft != "" {
		t.Fatalf("history cursor after switch = %d/%q, want reset", m.historyIndex, m.historyDraft)
	}
	if !m.navigateInputHistory(-1) || m.input.Value() != "目标 session 的历史输入" {
		t.Fatalf("up after switch input = %q history=%#v", m.input.Value(), m.inputHistory)
	}
}

func TestSwitchWorkspaceCreatesSessionWhenMissing(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	nextDir := t.TempDir()
	m := model{
		app:                app,
		input:              textinput.New(),
		cwd:                t.TempDir(),
		width:              100,
		height:             30,
		workspaceStatePath: filepath.Join(dataDir, "state", "workspaces.json"),
	}

	m.switchWorkspace(workspaceItem{CWD: nextDir, Title: "Next"})

	if !samePath(m.cwd, nextDir) || strings.TrimSpace(m.sessionID) == "" {
		t.Fatalf("switchWorkspace cwd/session = %q/%q, want %q/new session", m.cwd, m.sessionID, nextDir)
	}
	if got := app.Agent.SessionManager().GetChannelSession(context.Background(), channelName, channelUser); got != m.sessionID {
		t.Fatalf("channel session = %q, want created %q", got, m.sessionID)
	}
	item, ok := findWorkspace(m.workspaceStatePath, nextDir)
	if !ok || item.SessionID != m.sessionID {
		t.Fatalf("recorded workspace = %#v, %v; want session %q", item, ok, m.sessionID)
	}
}

func TestSwitchWorkspaceClearsPendingToolConfirmState(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	nextDir := t.TempDir()
	m := model{
		app:                app,
		input:              textinput.New(),
		cwd:                t.TempDir(),
		width:              100,
		height:             30,
		workspaceStatePath: filepath.Join(dataDir, "state", "workspaces.json"),
		pendingToolConfirm: &agentbuiltin.PendingToolUseConfirm{
			ToolName:  "bash",
			ToolUseID: "old-tool",
			BashHash:  "old-hash",
		},
		pendingQuestion: "是否允许执行命令？",
		pendingOptions:  []string{"允许一次", "拒绝"},
		pendingChoice:   1,
	}

	m.switchWorkspace(workspaceItem{CWD: nextDir, Title: "Next"})

	if m.pendingToolConfirm != nil || m.pendingQuestion != "" || len(m.pendingOptions) != 0 || m.pendingChoice != 0 {
		t.Fatalf("pending tool confirm state not cleared: confirm=%#v question=%q options=%v choice=%d", m.pendingToolConfirm, m.pendingQuestion, m.pendingOptions, m.pendingChoice)
	}
}

func TestStaleBashContinuationIsBlockedLocally(t *testing.T) {
	input := textinput.New()
	input.SetValue("go on")
	m := model{
		input: input,
		items: []transcriptItem{{role: "assistant", text: "等待你确认命令。"}},
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}
	if len(got.items) != 2 || got.items[1].role != "system" || !strings.Contains(got.items[1].text, "没有正在等待确认的命令") {
		t.Fatalf("items = %#v, want local stale approval warning", got.items)
	}
	if got.input.Value() != "" {
		t.Fatalf("input = %q, want cleared", got.input.Value())
	}
}

func TestProseBashApprovalConsentIsBlockedLocally(t *testing.T) {
	input := textinput.New()
	input.SetValue("同意")
	m := model{
		app:   newTestLocalApp(t, t.TempDir()),
		input: input,
		items: []transcriptItem{{role: "assistant", text: "请批准：\n```bash\ncd /tmp && grep -rn '/css/' main.go controllers/ 2>/dev/null | head -20\n```"}},
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}
	if len(got.items) != 2 || got.items[1].role != "system" || !strings.Contains(got.items[1].text, "这不是有效的本地审批") {
		t.Fatalf("items = %#v, want local prose approval warning", got.items)
	}
	if got.input.Value() != "" {
		t.Fatalf("input = %q, want cleared", got.input.Value())
	}
}

func TestRestoreTranscriptMarksStaleBashApproval(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	sessionID, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	mem := agentruntime.NewLocalMemory(agentruntime.Config{
		StorageBackend: "local",
		LocalDataDir:   dataDir,
	})
	if err := mem.Save(context.Background(), sessionID, []*schema.Message{
		schema.UserMessage("跑一下检查"),
		schema.AssistantMessage("等待你确认命令。", nil),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	items := restoreTranscript(app.Agent, sessionID)

	if len(items) < 3 {
		t.Fatalf("restoreTranscript() items = %#v, want restored messages", items)
	}
	last := items[len(items)-1]
	if last.role != "system" || !strings.Contains(last.text, "命令确认已失效") {
		t.Fatalf("last restored item = %#v, want stale approval warning", last)
	}
}

func TestRestoreTranscriptKeepsRichStaleBashApprovalDetails(t *testing.T) {
	dataDir := t.TempDir()
	app := newTestLocalApp(t, dataDir)
	sessionID, err := app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	mem := agentruntime.NewLocalMemory(agentruntime.Config{
		StorageBackend: "local",
		LocalDataDir:   dataDir,
	})
	richApproval := "等待你确认执行 bash 命令\n\n```bash\ngit status --short\n```\n\n请在下方审批面板选择允许或拒绝。"
	if err := mem.Save(context.Background(), sessionID, []*schema.Message{
		schema.UserMessage("跑一下检查"),
		schema.AssistantMessage(richApproval, nil),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	items := restoreTranscript(app.Agent, sessionID)

	if len(items) < 3 {
		t.Fatalf("restoreTranscript() items = %#v, want restored messages", items)
	}
	last := items[len(items)-1]
	if last.role != "system" || !strings.Contains(last.text, "命令确认已失效") || !strings.Contains(last.text, "git status --short") {
		t.Fatalf("last restored item = %#v, want stale warning with command", last)
	}
}

func TestStaleRichBashApprovalConsentAfterResumeIsBlockedLocally(t *testing.T) {
	input := textinput.New()
	input.SetValue("同意")
	m := model{
		app:   newTestLocalApp(t, t.TempDir()),
		input: input,
		items: []transcriptItem{
			{role: "system", text: staleBashApprovalText + "\n\n等待你确认执行 bash 命令\n\ntool_use_id: toolu_bash_old\n\n```bash\ngit status --short\n```\n\n请在下方审批面板选择允许或拒绝。"},
			{role: "system", text: "Resumed session ses_old"},
		},
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}
	if len(got.items) != 3 || got.items[2].role != "system" || !strings.Contains(got.items[2].text, "这不是有效的本地审批") {
		t.Fatalf("items = %#v, want local stale approval warning", got.items)
	}
	if got.input.Value() != "" {
		t.Fatalf("input = %q, want cleared", got.input.Value())
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

func TestVerticalKeysUseInputHistoryWhenInputFocused(t *testing.T) {
	input := textinput.New()
	input.SetValue("draft")
	m := model{input: input, focus: focusInput}
	m.recordInputHistory("first")
	m.recordInputHistory("second")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("up returned command, want nil")
	}
	if got.input.Value() != "second" {
		t.Fatalf("input after up = %q, want history item", got.input.Value())
	}
}

func TestVerticalKeysScrollTranscriptWhenTranscriptFocused(t *testing.T) {
	input := textinput.New()
	vp := viewport.New(20, 2)
	vp.SetContent("one\ntwo\nthree\nfour")
	vp.GotoBottom()
	m := model{input: input, viewport: vp, focus: focusTranscript}
	m.recordInputHistory("history")
	before := m.viewport.YOffset

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("up returned command, want nil")
	}
	if got.viewport.YOffset >= before {
		t.Fatalf("viewport offset = %d, want less than %d", got.viewport.YOffset, before)
	}
	if got.input.Value() != "" {
		t.Fatalf("input changed to %q, want unchanged", got.input.Value())
	}
}

func TestVerticalKeysUseInputHistoryWhenInputActiveAfterTranscriptFocus(t *testing.T) {
	input := textinput.New()
	input.Focus()
	input.SetValue("draft")
	vp := viewport.New(20, 2)
	vp.SetContent("one\ntwo\nthree\nfour")
	vp.GotoBottom()
	m := model{input: input, viewport: vp, focus: focusTranscript}
	m.recordInputHistory("history")
	before := m.viewport.YOffset

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("up returned command, want nil")
	}
	if got.input.Value() != "history" {
		t.Fatalf("input after up = %q, want history", got.input.Value())
	}
	if got.viewport.YOffset != before {
		t.Fatalf("viewport offset = %d, want unchanged %d", got.viewport.YOffset, before)
	}
}

func TestVerticalKeysKeepPendingSelectionPriority(t *testing.T) {
	vp := viewport.New(20, 2)
	vp.SetContent("one\ntwo\nthree\nfour")
	vp.GotoBottom()
	m := model{
		input:          textinput.New(),
		viewport:       vp,
		focus:          focusTranscript,
		pendingOptions: []string{"允许", "拒绝"},
	}
	before := m.viewport.YOffset

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := next.(model)
	if got.pendingChoice != 1 {
		t.Fatalf("pendingChoice = %d, want 1", got.pendingChoice)
	}
	if got.viewport.YOffset != before {
		t.Fatalf("viewport offset = %d, want unchanged %d", got.viewport.YOffset, before)
	}
}

func TestDoubleTabExitsExplorerAndOpensWorkspacePicker(t *testing.T) {
	m := model{
		input:              textinput.New(),
		cwd:                t.TempDir(),
		width:              100,
		height:             30,
		explorer:           &tuiExplorer{mode: explorerDiff},
		workspaceStatePath: filepath.Join(t.TempDir(), "workspaces.json"),
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := next.(model)
	if cmd != nil {
		t.Fatalf("first tab returned command, want nil")
	}
	if got.explorer == nil {
		t.Fatalf("first tab closed explorer")
	}
	if got.workspacePicker != nil {
		t.Fatalf("first tab opened workspace picker")
	}

	next, cmd = got.Update(tea.KeyMsg{Type: tea.KeyTab})
	got = next.(model)
	if cmd == nil {
		t.Fatalf("second tab returned nil command, want async git refresh")
	}
	if got.explorer != nil {
		t.Fatalf("second tab kept explorer open")
	}
	if got.workspacePicker == nil {
		t.Fatalf("second tab did not open workspace picker")
	}
}

func TestTypingReturnsFocusToInput(t *testing.T) {
	input := textinput.New()
	input.Focus()
	vp := viewport.New(20, 2)
	vp.SetContent("one\ntwo\nthree\nfour")
	vp.GotoBottom()
	m := model{input: input, viewport: vp, focus: focusTranscript}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := next.(model)
	if got.focus != focusInput {
		t.Fatalf("focus = %v, want input", got.focus)
	}
	if got.input.Value() != "x" {
		t.Fatalf("input = %q, want typed rune", got.input.Value())
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

func TestParsePlanCommand(t *testing.T) {
	cmd, ok := parsePlanCommand("/plan fix auth")
	if !ok || cmd.Action != planCommandStart || cmd.Prompt != "fix auth" {
		t.Fatalf("parsePlanCommand(/plan fix auth) = %#v/%v", cmd, ok)
	}
	cmd, ok = parsePlanCommand("/plan")
	if !ok || cmd.Action != planCommandEnter {
		t.Fatalf("parsePlanCommand(/plan) = %#v/%v, want enter", cmd, ok)
	}
	cmd, ok = parsePlanCommand("/plan off")
	if !ok || cmd.Action != planCommandExit {
		t.Fatalf("parsePlanCommand(/plan off) = %#v/%v, want exit", cmd, ok)
	}
}

func TestPlanPromptForbidsModification(t *testing.T) {
	got := planPrompt("fix auth")
	for _, want := range []string{"fix auth", "只输出计划", "不要修改文件", "不要执行会改变状态的命令"} {
		if !strings.Contains(got, want) {
			t.Fatalf("planPrompt() missing %q in %q", want, got)
		}
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
