package main

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

var (
	diffAddStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	diffDelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	diffMetaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	diffHunkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
)

func highlightCode(path, content string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	lexer := lexers.Match(path)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return content
	}
	style := styles.Get("onedark")
	if style == nil {
		style = styles.Fallback
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return content
	}
	return strings.TrimRight(buf.String(), "\n")
}

func highlightDiffLine(line string) string {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "@@"):
		return diffHunkStyle.Render(line)
	case strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "+++"):
		return diffAddStyle.Render(line)
	case strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "---"):
		return diffDelStyle.Render(line)
	case strings.HasPrefix(trimmed, "diff --git"),
		strings.HasPrefix(trimmed, "index "),
		strings.HasPrefix(trimmed, "new file mode"),
		strings.HasPrefix(trimmed, "deleted file mode"),
		strings.HasPrefix(trimmed, "similarity index"),
		strings.HasPrefix(trimmed, "rename from"),
		strings.HasPrefix(trimmed, "rename to"),
		strings.HasPrefix(trimmed, "---"),
		strings.HasPrefix(trimmed, "+++"):
		return diffMetaStyle.Render(line)
	default:
		return eventStyle.Render(line)
	}
}

func languageForPath(path string) string {
	lexer := lexers.Match(path)
	if lexer == nil {
		return strings.TrimPrefix(filepath.Ext(path), ".")
	}
	config := lexer.Config()
	if config == nil {
		return ""
	}
	return config.Name
}
