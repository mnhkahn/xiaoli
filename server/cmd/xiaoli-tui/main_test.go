package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestComposeSidebarKeepsKeys(t *testing.T) {
	top := []string{"title", "status", "model", "cwd", "context", "log"}
	middle := []string{"tasks", "- one", "MCP", "- up"}
	keys := []string{"keys", "enter send", "wheel scroll", "esc quit"}

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

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}
