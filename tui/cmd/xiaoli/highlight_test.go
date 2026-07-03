package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestHighlightCodeKeepsContent(t *testing.T) {
	got := highlightCode("main.go", "package main\nfunc main() {}\n")
	if !strings.Contains(got, "package") || !strings.Contains(got, "main") {
		t.Fatalf("highlightCode() = %q, want original tokens", got)
	}
}

func TestHighlightDiffLineAddsANSI(t *testing.T) {
	got := highlightDiffLine("+added")
	if strings.TrimSpace(stripTestANSI(got)) != "+added" {
		t.Fatalf("highlightDiffLine() = %q, want visible added line", got)
	}
}

func TestStyleDiffLineFitsWidth(t *testing.T) {
	got := styleDiffLine(strings.Repeat("+abcdef", 20), 30)
	if w := lipgloss.Width(got); w > 30 {
		t.Fatalf("styleDiffLine width = %d, want <= 30: %q", w, got)
	}
}
