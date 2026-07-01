package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

func TestComposeSidebarKeepsKeys(t *testing.T) {
	top := []string{"title", "status", "model", "cwd", "context", "log"}
	middle := []string{"tasks", "- one", "MCP", "- up"}
	keys := []string{"workspace", "repo", "main"}

	got := composeSidebar(top, middle, keys, 8)
	if len(got) != 8 {
		t.Fatalf("len(composeSidebar) = %d, want 8", len(got))
	}
	for _, want := range keys {
		if !containsLine(got, want) {
			t.Fatalf("composeSidebar() missing key line %q in %#v", want, got)
		}
	}
	if !containsLine(got, "...") {
		t.Fatalf("composeSidebar() = %#v, want ellipsis when top is truncated", got)
	}
}

func TestSidebarFooterShowsCWDAndGit(t *testing.T) {
	lines := sidebarFooterLines(model{cwd: "/tmp/work/repo", gitStatus: "main ↑2"}, 40)
	if !containsLine(lines, "repo") {
		t.Fatalf("sidebarFooterLines() = %#v, want cwd basename", lines)
	}
	if !containsLine(lines, "main ↑2") {
		t.Fatalf("sidebarFooterLines() = %#v, want git status", lines)
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
