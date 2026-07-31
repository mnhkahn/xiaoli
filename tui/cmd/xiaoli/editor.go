package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type editorMode string

const (
	editorNormal  editorMode = "NORMAL"
	editorInsert  editorMode = "INSERT"
	editorCommand editorMode = "COMMAND"
)

type editorResult struct {
	Command string
}

type miniEditor struct {
	path      string
	lines     []string
	cursorX   int
	cursorY   int
	scrollY   int
	mode      editorMode
	command   string
	dirty     bool
	lastError string
}

func newMiniEditor(path, text string) *miniEditor {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	return &miniEditor{
		path:  path,
		lines: lines,
		mode:  editorNormal,
	}
}

func (e *miniEditor) handleKey(msg tea.KeyMsg) editorResult {
	if e == nil {
		return editorResult{}
	}
	switch e.mode {
	case editorInsert:
		return e.handleInsertKey(msg)
	case editorCommand:
		return e.handleCommandKey(msg)
	default:
		return e.handleNormalKey(msg)
	}
}

func (e *miniEditor) handleNormalKey(msg tea.KeyMsg) editorResult {
	switch msg.String() {
	case "h", "left":
		e.moveCursor(0, -1)
	case "j", "down":
		e.moveCursor(1, 0)
	case "k", "up":
		e.moveCursor(-1, 0)
	case "l", "right":
		e.moveCursor(0, 1)
	case "0", "home":
		e.cursorX = 0
	case "$", "end":
		e.cursorX = len([]rune(e.currentLine()))
	case "G":
		e.cursorY = max(0, len(e.lines)-1)
		e.clampCursor()
	case "i":
		e.mode = editorInsert
	case ":":
		e.mode = editorCommand
		e.command = ""
	case "x":
		e.deleteRune()
	}
	e.ensureCursorVisible(12)
	return editorResult{}
}

func (e *miniEditor) handleInsertKey(msg tea.KeyMsg) editorResult {
	switch msg.Type {
	case tea.KeyEsc:
		e.mode = editorNormal
	case tea.KeyEnter:
		e.insertNewline()
	case tea.KeyBackspace:
		e.backspace()
	case tea.KeyRunes:
		e.insertText(string(msg.Runes))
	}
	e.ensureCursorVisible(12)
	return editorResult{}
}

func (e *miniEditor) handleCommandKey(msg tea.KeyMsg) editorResult {
	switch msg.Type {
	case tea.KeyEsc:
		e.mode = editorNormal
		e.command = ""
	case tea.KeyEnter:
		cmd := strings.TrimSpace(e.command)
		e.mode = editorNormal
		e.command = ""
		return editorResult{Command: cmd}
	case tea.KeyBackspace:
		if len(e.command) > 0 {
			e.command = e.command[:len(e.command)-1]
		}
	case tea.KeyRunes:
		e.command += string(msg.Runes)
	}
	return editorResult{}
}

func (e *miniEditor) moveCursor(dy, dx int) {
	e.cursorY = clamp(e.cursorY+dy, 0, max(0, len(e.lines)-1))
	e.cursorX += dx
	e.clampCursor()
}

func (e *miniEditor) clampCursor() {
	if len(e.lines) == 0 {
		e.lines = []string{""}
	}
	e.cursorY = clamp(e.cursorY, 0, len(e.lines)-1)
	lineLen := len([]rune(e.currentLine()))
	e.cursorX = clamp(e.cursorX, 0, lineLen)
}

func (e *miniEditor) currentLine() string {
	if e.cursorY < 0 || e.cursorY >= len(e.lines) {
		return ""
	}
	return e.lines[e.cursorY]
}

func (e *miniEditor) insertText(text string) {
	line := []rune(e.currentLine())
	left := string(line[:clamp(e.cursorX, 0, len(line))])
	right := string(line[clamp(e.cursorX, 0, len(line)):])
	e.lines[e.cursorY] = left + text + right
	e.cursorX += len([]rune(text))
	e.dirty = true
}

func (e *miniEditor) insertNewline() {
	line := []rune(e.currentLine())
	x := clamp(e.cursorX, 0, len(line))
	left := string(line[:x])
	right := string(line[x:])
	e.lines[e.cursorY] = left
	e.lines = append(e.lines[:e.cursorY+1], append([]string{right}, e.lines[e.cursorY+1:]...)...)
	e.cursorY++
	e.cursorX = 0
	e.dirty = true
}

func (e *miniEditor) backspace() {
	if e.cursorX > 0 {
		line := []rune(e.currentLine())
		x := clamp(e.cursorX, 0, len(line))
		e.lines[e.cursorY] = string(line[:x-1]) + string(line[x:])
		e.cursorX--
		e.dirty = true
		return
	}
	if e.cursorY > 0 {
		prevLen := len([]rune(e.lines[e.cursorY-1]))
		e.lines[e.cursorY-1] += e.lines[e.cursorY]
		e.lines = append(e.lines[:e.cursorY], e.lines[e.cursorY+1:]...)
		e.cursorY--
		e.cursorX = prevLen
		e.dirty = true
	}
}

func (e *miniEditor) deleteRune() {
	line := []rune(e.currentLine())
	if e.cursorX >= len(line) {
		return
	}
	e.lines[e.cursorY] = string(line[:e.cursorX]) + string(line[e.cursorX+1:])
	e.dirty = true
}

func (e *miniEditor) text() string {
	return strings.Join(e.lines, "\n")
}

func (e *miniEditor) save(cwd string) error {
	clean := filepath.Clean(e.path)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path outside project: %s", e.path)
	}
	if err := os.WriteFile(filepath.Join(cwd, clean), []byte(e.text()), 0o644); err != nil {
		e.lastError = err.Error()
		return err
	}
	e.dirty = false
	e.lastError = ""
	return nil
}

func (e *miniEditor) render(width, height int) string {
	if e == nil {
		return ""
	}
	width = max(8, width)
	height = max(3, height)
	e.ensureCursorVisible(max(1, height-2))
	status := string(e.mode)
	if e.dirty {
		status += " modified"
	}
	if e.mode == editorCommand {
		status += " :" + e.command
	}
	if e.lastError != "" {
		status += " " + e.lastError
	}
	lines := []string{titleStyle.Render(fitDisplay(status, width))}
	bodyHeight := max(1, height-2)
	end := min(len(e.lines), e.scrollY+bodyHeight)
	lineNumberWidth := codeLineNumberWidth(len(e.lines))
	for y := e.scrollY; y < end; y++ {
		line := e.lines[y]
		contentWidth := max(1, width-lineNumberWidth)
		if y == e.cursorY {
			line = renderEditorCursor(line, e.cursorX)
			line = editorCursorLineStyle().Render(fitDisplay(line, contentWidth))
		} else {
			line = fitDisplay(line, contentWidth)
		}
		lines = append(lines, renderCodeLineNumber(y+1, lineNumberWidth)+line)
	}
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, hintStyle.Render(fitDisplay("i insert · :w save · :q close · tab tree", width)))
	return strings.Join(lines, "\n")
}

func (e *miniEditor) ensureCursorVisible(height int) {
	if height <= 0 {
		return
	}
	if e.cursorY < e.scrollY {
		e.scrollY = e.cursorY
	}
	if e.cursorY >= e.scrollY+height {
		e.scrollY = e.cursorY - height + 1
	}
	e.scrollY = clamp(e.scrollY, 0, max(0, len(e.lines)-height))
}

func (e *miniEditor) scrollBy(delta, height int) {
	if e == nil || height <= 0 {
		return
	}
	bodyHeight := max(1, height-2)
	e.scrollY = clamp(e.scrollY+delta, 0, max(0, len(e.lines)-bodyHeight))
	if e.cursorY < e.scrollY {
		e.cursorY = e.scrollY
	}
	if e.cursorY >= e.scrollY+bodyHeight {
		e.cursorY = min(len(e.lines)-1, e.scrollY+bodyHeight-1)
	}
	e.clampCursor()
}

func renderEditorCursor(line string, cursorX int) string {
	runes := []rune(line)
	cursorX = clamp(cursorX, 0, len(runes))
	if len(runes) == 0 {
		return editorCursorStyle().Render(" ")
	}
	if cursorX == len(runes) {
		return string(runes) + editorCursorStyle().Render(" ")
	}
	return string(runes[:cursorX]) + editorCursorStyle().Render(string(runes[cursorX])) + string(runes[cursorX+1:])
}

func editorCursorStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("16")).
		Background(lipgloss.Color("229")).
		Bold(true)
}

func editorCursorLineStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color("236"))
}
