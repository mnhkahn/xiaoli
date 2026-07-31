package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type explorerMode string

const (
	explorerTree explorerMode = "tree"
	explorerDiff explorerMode = "diff"
)

type explorerEntry struct {
	Path   string
	Label  string
	Status string
	Staged bool
	Depth  int
	IsDir  bool
}

type tuiExplorer struct {
	mode        explorerMode
	cwd         string
	width       int
	height      int
	leftWidth   int
	selected    int
	leftScroll  int
	rightScroll int
	entries     []explorerEntry
	expanded    map[string]bool
	previewPath string
	preview     string
	previewRaw  string
	previewCode bool
	editor      *miniEditor
	focusRight  bool
	selection   transcriptSelection
	err         string
}

// explorerPreviewMouseTop is the terminal mouse row at which preview content
// starts. Mouse coordinates are relative to the explorer body; the first row
// is the persistent pane header, which includes the selected file name.
const explorerPreviewMouseTop = 1

func newTreeExplorer(cwd string, width, height int) *tuiExplorer {
	ex := &tuiExplorer{
		mode:      explorerTree,
		cwd:       cwd,
		width:     width,
		height:    height,
		expanded:  map[string]bool{"": true},
		leftWidth: explorerLeftWidth(max(1, width-1)),
	}
	ex.reloadTree()
	ex.selectFirstFile()
	return ex
}

func newDiffExplorer(cwd string, width, height int) *tuiExplorer {
	if root, err := gitWorktreeRoot(cwd); err == nil {
		cwd = root
	}
	ex := &tuiExplorer{
		mode:      explorerDiff,
		cwd:       cwd,
		width:     width,
		height:    height,
		leftWidth: explorerLeftWidth(max(1, width-1)),
	}
	ex.reloadDiff()
	ex.refreshPreview()
	return ex
}

func (e *tuiExplorer) resize(width, height int) {
	if e == nil {
		return
	}
	e.width = width
	e.height = height
	e.leftWidth = explorerLeftWidth(e.renderWidth())
}

func (e *tuiExplorer) handleKey(msg tea.KeyMsg) (*tuiExplorer, tea.Cmd, bool) {
	if e == nil {
		return e, nil, false
	}
	if e.mode == explorerDiff && e.focusRight {
		switch msg.String() {
		case "tab", "h", "left":
			e.focusRight = false
			return e, nil, true
		case "up", "k":
			e.scrollPreview(-1)
			return e, nil, true
		case "down", "j":
			e.scrollPreview(1)
			return e, nil, true
		case "pgup", "ctrl+u":
			e.scrollPreview(-e.previewHeight())
			return e, nil, true
		case "pgdown", "ctrl+d", "space":
			e.scrollPreview(e.previewHeight())
			return e, nil, true
		case "home":
			e.rightScroll = 0
			return e, nil, true
		case "end":
			e.rightScroll = max(0, len(e.previewLines())-e.previewHeight())
			return e, nil, true
		}
	}
	if e.mode == explorerTree && e.focusRight && e.editor != nil {
		if msg.String() == "tab" {
			e.focusRight = false
			return e, nil, true
		}
		result := e.editor.handleKey(msg)
		if result.Command != "" {
			e.handleEditorCommand(result.Command)
		}
		return e, nil, true
	}
	switch msg.String() {
	case "q", "esc":
		return nil, nil, true
	case "tab":
		if e.mode == explorerDiff {
			e.focusRight = true
			return e, nil, true
		}
		if e.mode == explorerTree && e.editor != nil {
			e.focusRight = true
			return e, nil, true
		}
	case "l", "right":
		if e.mode == explorerDiff {
			e.focusRight = true
			return e, nil, true
		}
		if e.mode == explorerTree && e.selected >= 0 && e.selected < len(e.entries) {
			entry := e.entries[e.selected]
			if entry.IsDir {
				e.expanded[entry.Path] = true
				e.reloadTree()
				e.ensureSelectionVisible()
			} else if e.editor != nil {
				e.focusRight = true
			}
			return e, nil, true
		}
	case "h", "left":
		if e.mode == explorerTree || e.mode == explorerDiff {
			e.focusRight = false
			return e, nil, true
		}
	case "up", "k":
		e.moveSelection(-1)
		return e, nil, true
	case "down", "j":
		e.moveSelection(1)
		return e, nil, true
	case "pgup", "ctrl+u":
		e.moveSelection(-e.listHeight())
		return e, nil, true
	case "pgdown", "ctrl+d":
		e.moveSelection(e.listHeight())
		return e, nil, true
	case "home":
		e.selected = 0
		e.ensureSelectionVisible()
		e.refreshPreview()
		return e, nil, true
	case "end":
		if len(e.entries) > 0 {
			e.selected = len(e.entries) - 1
			e.ensureSelectionVisible()
			e.refreshPreview()
		}
		return e, nil, true
	case "enter":
		e.activateSelected()
		return e, nil, true
	case " ", "space":
		if e.mode == explorerDiff {
			e.activateSelected()
			return e, nil, true
		}
	case "y":
		text := e.copyText()
		if strings.TrimSpace(text) == "" {
			e.err = "没有可复制的内容"
			return e, nil, true
		}
		if err := copyTextToClipboard(text); err != nil {
			e.err = "复制失败：" + err.Error()
		} else {
			e.err = "已复制"
		}
		return e, nil, true
	}
	return e, nil, false
}

func (e *tuiExplorer) handleMouse(msg tea.MouseMsg) bool {
	if e == nil {
		return false
	}
	left := msg.X < e.leftWidth
	switch msg.Type {
	case tea.MouseWheelUp:
		if left {
			e.leftScroll = max(0, e.leftScroll-3)
		} else if e.editor != nil && e.mode == explorerTree {
			e.editor.scrollBy(-3, e.previewHeight())
		} else {
			e.rightScroll = max(0, e.rightScroll-3)
		}
		return true
	case tea.MouseWheelDown:
		if left {
			e.leftScroll = min(max(0, len(e.entries)-e.listHeight()), e.leftScroll+3)
		} else if e.editor != nil && e.mode == explorerTree {
			e.editor.scrollBy(3, e.previewHeight())
		} else {
			e.rightScroll = min(max(0, len(e.previewLines())-e.previewHeight()), e.rightScroll+3)
		}
		return true
	case tea.MouseLeft:
		if left {
			if msg.Action != tea.MouseActionPress {
				return true
			}
			row := msg.Y - 3
			if row < 0 || row >= e.listHeight() {
				return true
			}
			idx := e.leftScroll + row
			if idx < 0 || idx >= len(e.entries) {
				return true
			}
			e.selected = idx
			e.ensureSelectionVisible()
			e.focusRight = false
			e.selection = transcriptSelection{}
			e.refreshPreview()
			return true
		}
		return e.handlePreviewSelectionMouse(msg)
	case tea.MouseRelease:
		return e.handlePreviewSelectionMouse(msg)
	default:
		if msg.Action == tea.MouseActionMotion {
			return e.handlePreviewSelectionMouse(msg)
		}
		return false
	}
}

func (e *tuiExplorer) handlePreviewSelectionMouse(msg tea.MouseMsg) bool {
	if e == nil || e.mode == explorerTree && e.editor != nil {
		return false
	}
	point, ok := e.previewMousePoint(msg)
	if msg.Action == tea.MouseActionRelease || msg.Type == tea.MouseRelease {
		if !e.selection.dragging {
			return false
		}
		if ok {
			e.selection.focus = point
		}
		e.selection.dragging = false
		text := selectedTranscriptText(e.previewLines(), e.selection)
		e.selection.text = text
		if strings.TrimSpace(text) == "" {
			e.selection = transcriptSelection{}
			e.err = ""
			return true
		}
		e.selection.active = true
		if err := copyTextToClipboard(text); err != nil {
			e.err = "复制失败：" + err.Error()
		} else {
			e.err = "已复制选中内容"
		}
		return true
	}
	if !ok {
		if e.selection.dragging && msg.Action == tea.MouseActionMotion {
			e.selection.focus = point
			e.selection.active = true
			return true
		}
		return false
	}
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft && msg.Type != tea.MouseLeft {
			return false
		}
		e.focusRight = true
		e.selection = transcriptSelection{dragging: true, anchor: point, focus: point}
		e.err = "选择中"
		return true
	case tea.MouseActionMotion:
		if !e.selection.dragging {
			return false
		}
		e.selection.focus = point
		if point != e.selection.anchor {
			e.selection.active = true
		}
		return true
	default:
		return false
	}
}

func (e *tuiExplorer) previewMousePoint(msg tea.MouseMsg) (selectionPoint, bool) {
	rightTotalWidth := max(20, e.renderWidth()-e.leftWidth)
	rightContentWidth := max(12, rightTotalWidth-boxStyle.GetHorizontalFrameSize())
	previewHeight := max(0, explorerBodyHeight(e.renderHeight())-1)
	x := msg.X - e.leftWidth - 2
	if e.mode == explorerTree && e.previewCode {
		gutterWidth := codeLineNumberWidth(len(e.previewLines()))
		x -= gutterWidth
		rightContentWidth = max(1, rightContentWidth-gutterWidth)
	}
	y := msg.Y - explorerPreviewMouseTop
	point := selectionPoint{x: x, y: e.rightScroll + y}
	if x < 0 {
		point.x = 0
	}
	if y < 0 {
		point.y = e.rightScroll
	}
	if point.x > rightContentWidth-1 {
		point.x = max(0, rightContentWidth-1)
	}
	maxLine := len(e.previewLines()) - 1
	if maxLine < 0 {
		maxLine = 0
	}
	if point.y > maxLine {
		point.y = maxLine
	}
	ok := msg.X >= e.leftWidth+2 &&
		msg.X < e.leftWidth+2+rightContentWidth &&
		msg.Y >= explorerPreviewMouseTop &&
		msg.Y < explorerPreviewMouseTop+previewHeight &&
		len(e.previewLines()) > 0
	return point, ok
}

func (e *tuiExplorer) View() string {
	if e == nil {
		return ""
	}
	// Leave the terminal's final column unused. Otty applies VT auto-wrap to
	// output that lands there; during a diff redraw that pending wrap can shift
	// the whole alternate screen up one row.
	width := e.renderWidth()
	// Otty scrolls the alternate screen when a redraw touches its final row.
	// Keep one vertical safety row, so keyboard navigation can never advance
	// the whole explorer frame up the terminal.
	height := e.renderHeight()
	e.leftWidth = explorerLeftWidth(width)
	frameW := boxStyle.GetHorizontalFrameSize()
	leftContentWidth := max(12, e.leftWidth-frameW)
	rightTotalWidth := max(20, width-e.leftWidth)
	rightContentWidth := max(12, rightTotalWidth-frameW)
	// Reserve one row each for the explorer header, help, and status footer,
	// plus the two border rows. Keeping this calculation in one place prevents
	// the top of either pane from scrolling out of a short terminal.
	bodyHeight := explorerBodyHeight(height)

	title := "Xiaoli /" + string(e.mode)
	if e.mode == explorerTree {
		title = "Xiaoli /tree"
	}
	header := titleStyle.Render(title) + "  " + hintStyle.Render("cwd: "+compactPath(e.cwd, max(12, width-28)))
	helpText := "tree: j/k move · l/tab edit · editor: i insert · :w save · q close"
	if e.mode == explorerDiff {
		helpText = "diff: j/k choose file · →/l view diff · ←/h return · preview: j/k scroll · space stage · q close"
	}
	help := hintStyle.Render(helpText)

	// lipgloss applies horizontal padding inside Width(), so text must be
	// fitted to the inner width. Otherwise a long path wraps and increases the
	// box height beyond the terminal viewport.
	left := e.renderLeft(max(1, leftContentWidth-boxStyle.GetHorizontalPadding()), bodyHeight)
	right := e.renderRight(max(1, rightContentWidth-boxStyle.GetHorizontalPadding()), bodyHeight)
	leftBox := boxStyle
	rightBox := boxStyle
	if e.focusRight {
		rightBox = boxStyle.BorderForeground(lipgloss.Color("75"))
	} else {
		leftBox = boxStyle.BorderForeground(lipgloss.Color("75"))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		leftBox.Width(leftContentWidth).Height(bodyHeight).Render(left),
		rightBox.Width(rightContentWidth).Height(bodyHeight).Render(right),
	)
	footer := " "
	if e.err != "" {
		footer = eventStyle.Render(truncateDisplay(e.err, width))
	}
	view := lipgloss.JoinVertical(lipgloss.Left, fitDisplay(header, width), body, fitDisplay(help, width), footer)
	// Bubble Tea discards lines from the *top* when a View is taller than the
	// reported terminal. Some Lipgloss combinations can grow a row after the
	// pane geometry has been calculated, so enforce the final budget here and
	// sacrifice only bottom chrome if necessary.
	return limitExplorerViewHeight(view, height)
}

func limitExplorerViewHeight(view string, height int) string {
	if height <= 0 || view == "" {
		return ""
	}
	lines := strings.Split(view, "\n")
	if len(lines) <= height {
		return view
	}
	return strings.Join(lines[:height], "\n")
}

func (e *tuiExplorer) renderLeft(width, height int) string {
	title := "Project Tree"
	if e.mode == explorerDiff {
		title = "Changed Files"
	}
	// Keep the pane chrome outside the scrollable list.  In particular, the
	// Changed Files label must not be part of the slice selected by leftScroll:
	// moving through a large diff should only move file rows beneath it.
	listHeight := max(0, height-1)
	showScrollBar := len(e.entries) > listHeight && width > 1
	contentWidth := width
	if showScrollBar {
		contentWidth--
	}
	lines := []string{titleStyle.Render(fitDisplay(title, contentWidth))}
	visible := e.entries
	start := clamp(e.leftScroll, 0, len(visible))
	end := min(len(visible), start+listHeight)
	for i := start; i < end; i++ {
		entry := visible[i]
		prefix := "  "
		style := eventStyle
		if i == e.selected {
			prefix = "› "
			style = explorerSelectedStyle()
		}
		line := prefix + e.entryLabel(entry)
		lines = append(lines, style.Render(fitDisplay(line, contentWidth)))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	if !showScrollBar {
		return strings.Join(lines, "\n")
	}

	// The scrollbar is rendered beside, rather than inside, the file rows so
	// long paths can never wrap and push the explorer box down the terminal.
	for row := range lines {
		marker := " "
		if row > 0 {
			marker = e.leftScrollBarMarker(row-1, listHeight)
		}
		lines[row] += marker
	}
	return strings.Join(lines, "\n")
}

func (e *tuiExplorer) renderWidth() int {
	if e == nil {
		return 1
	}
	return max(1, e.width-1)
}

func (e *tuiExplorer) renderHeight() int {
	if e == nil {
		return 1
	}
	return max(1, e.height-1)
}

func (e *tuiExplorer) leftScrollBarMarker(row, viewportHeight int) string {
	if viewportHeight <= 0 || len(e.entries) <= viewportHeight {
		return " "
	}
	thumbHeight := max(1, (viewportHeight*viewportHeight)/len(e.entries))
	thumbHeight = min(viewportHeight, thumbHeight)
	maxScroll := max(1, len(e.entries)-viewportHeight)
	thumbStart := ((viewportHeight - thumbHeight) * clamp(e.leftScroll, 0, maxScroll)) / maxScroll
	if row >= thumbStart && row < thumbStart+thumbHeight {
		return "█"
	}
	return "│"
}

func explorerSelectedStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("62")).
		Bold(true)
}

func (e *tuiExplorer) renderRight(width, height int) string {
	title := "Preview"
	if e.mode == explorerDiff {
		title = "Diff"
		if e.previewPath != "" {
			title += " · " + e.previewPath
		}
	} else if e.editor != nil {
		title = "Editor"
		if e.focusRight {
			title = "Editor · focus"
		}
	} else if e.previewPath != "" {
		if lang := languageForPath(e.previewPath); lang != "" {
			title = "Preview · " + lang
		}
	}
	lines := []string{titleStyle.Render(fitDisplay(title, width))}
	if e.mode == explorerTree && e.editor != nil {
		return e.editor.render(width, height)
	}
	allLines := e.previewLines()
	lineNumberWidth := 0
	if e.mode == explorerTree && e.previewCode {
		lineNumberWidth = codeLineNumberWidth(len(allLines))
	}
	start := clamp(e.rightScroll, 0, len(allLines))
	end := min(len(allLines), start+max(0, height-1))
	for y := start; y < end; y++ {
		line := allLines[y]
		contentWidth := max(1, width-lineNumberWidth)
		if e.mode == explorerDiff {
			line = styleDiffLine(line, width)
		} else {
			line = stylePreviewLine(line, contentWidth)
		}
		if e.selection.active || e.selection.dragging {
			startSel, endSel := normalizedSelection(e.selection)
			line = renderSelectionOverlayLine(line, y, startSel, endSel, contentWidth)
		}
		if lineNumberWidth > 0 {
			line = renderCodeLineNumber(y+1, lineNumberWidth) + line
		}
		lines = append(lines, line)
	}
	if e.mode != explorerDiff && e.previewPath != "" {
		lines = append(lines, hintStyle.Render(fitDisplay("file: "+e.previewPath, width)))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (e *tuiExplorer) entryLabel(entry explorerEntry) string {
	if e.mode == explorerDiff {
		marker := "[ ]"
		if entry.Staged {
			marker = "[+]"
		} else if strings.HasPrefix(entry.Status, "??") {
			marker = "[?]"
		}
		return strings.TrimSpace(marker + " " + entry.Status + "  " + entry.Path)
	}
	indent := strings.Repeat("  ", entry.Depth)
	if entry.IsDir {
		marker := "▸"
		if e.expanded[entry.Path] {
			marker = "▾"
		}
		return indent + marker + " " + entry.Label
	}
	return indent + "  " + entry.Label
}

func (e *tuiExplorer) reloadTree() {
	paths, err := projectFiles(e.cwd)
	if err != nil {
		e.err = err.Error()
		paths = nil
	}
	root := buildTree(paths)
	e.entries = e.visibleTreeEntries(root)
}

func (e *tuiExplorer) reloadDiff() {
	entries, err := changedFiles(e.cwd)
	if err != nil {
		e.err = err.Error()
	}
	e.entries = entries
}

func (e *tuiExplorer) visibleTreeEntries(root *treeNode) []explorerEntry {
	var out []explorerEntry
	var walk func(node *treeNode, depth int)
	walk = func(node *treeNode, depth int) {
		for _, child := range node.sortedChildren() {
			path := child.path
			out = append(out, explorerEntry{
				Path:  path,
				Label: child.name,
				Depth: depth,
				IsDir: child.dir,
			})
			if child.dir && e.expanded[path] {
				walk(child, depth+1)
			}
		}
	}
	walk(root, 0)
	return out
}

func (e *tuiExplorer) selectFirstFile() {
	for attempt := 0; attempt < 8; attempt++ {
		for i, entry := range e.entries {
			if !entry.IsDir {
				e.selected = i
				e.refreshPreview()
				return
			}
		}
		expanded := false
		for _, entry := range e.entries {
			if entry.IsDir && !e.expanded[entry.Path] {
				e.expanded[entry.Path] = true
				expanded = true
				break
			}
		}
		if !expanded {
			break
		}
		e.reloadTree()
	}
	e.refreshPreview()
}

func (e *tuiExplorer) activateSelected() {
	if len(e.entries) == 0 || e.selected < 0 || e.selected >= len(e.entries) {
		return
	}
	entry := e.entries[e.selected]
	if e.mode == explorerTree && entry.IsDir {
		e.expanded[entry.Path] = !e.expanded[entry.Path]
		e.reloadTree()
		if e.selected >= len(e.entries) {
			e.selected = max(0, len(e.entries)-1)
		}
		e.ensureSelectionVisible()
		return
	}
	if e.mode == explorerDiff && !entry.IsDir {
		if err := e.toggleSelectedStage(entry); err != nil {
			e.err = err.Error()
		}
		return
	}
	e.refreshPreview()
}

func (e *tuiExplorer) toggleSelectedStage(entry explorerEntry) error {
	add, args := stageToggleArgs(entry)
	if _, err := runGit(e.cwd, args...); err != nil {
		action := "暂存"
		if !add {
			action = "取消暂存"
		}
		return fmt.Errorf("%s失败: %w", action, err)
	}
	if add {
		e.err = "已暂存 " + entry.Path
	} else {
		e.err = "已取消暂存 " + entry.Path
	}
	e.reloadDiff()
	if e.selected >= len(e.entries) {
		e.selected = max(0, len(e.entries)-1)
	}
	e.ensureSelectionVisible()
	e.refreshPreview()
	return nil
}

func (e *tuiExplorer) refreshPreview() {
	e.rightScroll = 0
	e.selection = transcriptSelection{}
	e.preview = ""
	e.previewRaw = ""
	e.previewCode = false
	e.previewPath = ""
	e.editor = nil
	if len(e.entries) == 0 || e.selected < 0 || e.selected >= len(e.entries) {
		if e.mode == explorerDiff {
			e.preview = "No changes."
		}
		return
	}
	entry := e.entries[e.selected]
	if entry.IsDir {
		e.preview = "Click a file to preview it."
		e.previewPath = entry.Path
		return
	}
	e.previewPath = entry.Path
	var err error
	if e.mode == explorerDiff {
		e.preview, err = fileDiff(e.cwd, entry)
		e.previewRaw = e.preview
	} else {
		e.preview, err = filePreview(e.cwd, entry.Path)
		e.previewRaw = e.preview
		if err == nil {
			if canEditPreview(e.previewRaw) {
				e.editor = newMiniEditor(entry.Path, e.previewRaw)
			}
			e.preview = highlightCode(entry.Path, e.preview)
			e.previewCode = true
		}
	}
	if err != nil {
		e.preview = err.Error()
		e.previewRaw = e.preview
	}
}

func canEditPreview(text string) bool {
	if strings.Contains(text, "binary file preview skipped") {
		return false
	}
	if strings.Contains(text, "preview skipped") {
		return false
	}
	return true
}

func (e *tuiExplorer) handleEditorCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	switch cmd {
	case "w":
		if e.editor == nil {
			return
		}
		if err := e.editor.save(e.cwd); err != nil {
			e.err = err.Error()
			return
		}
		e.err = "已保存 " + e.editor.path
		e.previewRaw = e.editor.text()
		e.preview = highlightCode(e.editor.path, e.previewRaw)
	case "q":
		if e.editor != nil && e.editor.dirty {
			e.err = "文件已修改，使用 :w 保存或 Esc 返回"
			return
		}
		e.focusRight = false
	case "wq":
		e.handleEditorCommand("w")
		if e.err == "" || strings.HasPrefix(e.err, "已保存 ") {
			e.focusRight = false
		}
	}
}

func (e *tuiExplorer) moveSelection(delta int) {
	if len(e.entries) == 0 {
		return
	}
	e.selected = clamp(e.selected+delta, 0, len(e.entries)-1)
	e.ensureSelectionVisible()
	e.refreshPreview()
}

func (e *tuiExplorer) scrollPreview(delta int) {
	if e == nil || delta == 0 {
		return
	}
	e.rightScroll = clamp(e.rightScroll+delta, 0, max(0, len(e.previewLines())-e.previewHeight()))
	e.selection = transcriptSelection{}
}

func (e *tuiExplorer) ensureSelectionVisible() {
	height := e.listHeight()
	if height <= 0 {
		return
	}
	if e.selected < e.leftScroll {
		e.leftScroll = e.selected
	}
	if e.selected >= e.leftScroll+height {
		e.leftScroll = e.selected - height + 1
	}
	e.leftScroll = clamp(e.leftScroll, 0, max(0, len(e.entries)-height))
}

func (e *tuiExplorer) listHeight() int {
	return max(1, explorerBodyHeight(e.renderHeight())-1)
}

func (e *tuiExplorer) previewHeight() int {
	return max(1, explorerBodyHeight(e.renderHeight())-1)
}

func explorerBodyHeight(height int) int {
	const explorerChromeHeight = 5 // header, help, footer, and two border rows
	return max(1, height-explorerChromeHeight)
}

func (e *tuiExplorer) previewLines() []string {
	if e.preview == "" {
		return nil
	}
	return strings.Split(e.preview, "\n")
}

func (e *tuiExplorer) copyText() string {
	if e.mode == explorerDiff {
		return e.previewRaw
	}
	if len(e.entries) == 0 || e.selected < 0 || e.selected >= len(e.entries) {
		return ""
	}
	if e.entries[e.selected].IsDir {
		return ""
	}
	text, err := readProjectFile(e.cwd, e.entries[e.selected].Path, 512*1024)
	if err != nil {
		return ""
	}
	return text
}

type treeNode struct {
	name     string
	path     string
	dir      bool
	children map[string]*treeNode
}

func buildTree(paths []string) *treeNode {
	root := &treeNode{dir: true, children: map[string]*treeNode{}}
	for _, path := range paths {
		parts := strings.Split(filepath.ToSlash(path), "/")
		cur := root
		var prefix []string
		for i, part := range parts {
			if part == "" {
				continue
			}
			prefix = append(prefix, part)
			child, ok := cur.children[part]
			if !ok {
				child = &treeNode{
					name:     part,
					path:     strings.Join(prefix, "/"),
					dir:      i < len(parts)-1,
					children: map[string]*treeNode{},
				}
				cur.children[part] = child
			}
			if i < len(parts)-1 {
				child.dir = true
			}
			cur = child
		}
	}
	return root
}

func (n *treeNode) sortedChildren() []*treeNode {
	if n == nil {
		return nil
	}
	out := make([]*treeNode, 0, len(n.children))
	for _, child := range n.children {
		out = append(out, child)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dir != out[j].dir {
			return out[i].dir
		}
		return out[i].name < out[j].name
	})
	return out
}

func projectFiles(cwd string) ([]string, error) {
	if out, err := runGit(cwd, "ls-files", "-co", "--exclude-standard"); err == nil {
		return cleanPathLines(out), nil
	}
	var paths []string
	err := filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == "node_modules" || name == "dist" || name == "build") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(cwd, path)
		if err == nil {
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func changedFiles(cwd string) ([]explorerEntry, error) {
	out, err := runGit(cwd, "status", "--porcelain=v1")
	if err != nil {
		return nil, err
	}
	var entries []explorerEntry
	for _, line := range strings.Split(out, "\n") {
		entry, ok := parseChangedFileStatus(line)
		if ok {
			entries = append(entries, entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func gitWorktreeRoot(cwd string) (string, error) {
	root, err := runGit(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(strings.TrimSpace(root))
}

func parseChangedFileStatus(line string) (explorerEntry, bool) {
	if len(line) < 4 {
		return explorerEntry{}, false
	}
	rawStatus := line[:2]
	status := strings.TrimSpace(rawStatus)
	path := strings.TrimSpace(line[3:])
	if strings.Contains(path, " -> ") {
		parts := strings.Split(path, " -> ")
		path = strings.TrimSpace(parts[len(parts)-1])
	}
	if path == "" {
		return explorerEntry{}, false
	}
	staged := rawStatus[0] != ' ' && rawStatus[0] != '?'
	return explorerEntry{Path: path, Status: status, Staged: staged}, true
}

func stageToggleArgs(entry explorerEntry) (bool, []string) {
	if entry.Staged {
		return false, []string{"restore", "--staged", "--", entry.Path}
	}
	return true, []string{"add", "--", entry.Path}
}

func filePreview(cwd, rel string) (string, error) {
	return readProjectFile(cwd, rel, 256*1024)
}

func fileDiff(cwd string, entry explorerEntry) (string, error) {
	if strings.HasPrefix(entry.Status, "??") {
		text, err := readProjectFile(cwd, entry.Path, 256*1024)
		if err != nil {
			return "", err
		}
		return "untracked file: " + entry.Path + "\n\n" + text, nil
	}
	var parts []string
	if !entry.Staged {
		if out, err := runGit(cwd, "diff", "--", entry.Path); err == nil && strings.TrimSpace(out) != "" {
			parts = append(parts, out)
		}
	}
	if entry.Staged {
		if out, err := runGit(cwd, "diff", "--cached", "--", entry.Path); err == nil && strings.TrimSpace(out) != "" {
			parts = append(parts, out)
		}
	}
	if entry.Staged && strings.Contains(entry.Status, "M") {
		if out, err := runGit(cwd, "diff", "--", entry.Path); err == nil && strings.TrimSpace(out) != "" {
			parts = append(parts, out)
		}
	}
	if !entry.Staged && strings.Contains(entry.Status, "M") {
		if out, err := runGit(cwd, "diff", "--cached", "--", entry.Path); err == nil && strings.TrimSpace(out) != "" {
			parts = append(parts, out)
		}
	}
	if len(parts) == 0 {
		if out, err := runGit(cwd, "diff", "--", entry.Path); err == nil && strings.TrimSpace(out) != "" {
			parts = append(parts, out)
		}
	}
	if len(parts) == 0 {
		if out, err := runGit(cwd, "diff", "--cached", "--", entry.Path); err == nil && strings.TrimSpace(out) != "" {
			parts = append(parts, out)
		}
	}
	if len(parts) == 0 {
		return "No diff for " + entry.Path, nil
	}
	return strings.Join(parts, "\n"), nil
}

func readProjectFile(cwd, rel string, maxBytes int64) (string, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("path outside project: %s", rel)
	}
	path := filepath.Join(cwd, clean)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", rel)
	}
	if info.Size() > maxBytes {
		return fmt.Sprintf("%s is %.1f KB, preview skipped", rel, float64(info.Size())/1024), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "binary file preview skipped", nil
	}
	return string(data), nil
}

func runGit(cwd string, args ...string) (string, error) {
	gitArgs := append([]string{"-c", "core.quotepath=false"}, args...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func cleanPathLines(s string) []string {
	var paths []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, filepath.ToSlash(line))
		}
	}
	sort.Strings(paths)
	return paths
}

func visibleLines(lines []string, scroll, height int) []string {
	if height <= 0 || len(lines) == 0 {
		return nil
	}
	scroll = clamp(scroll, 0, max(0, len(lines)-height))
	end := min(len(lines), scroll+height)
	return lines[scroll:end]
}

func stylePreviewLine(line string, width int) string {
	return fitDisplay(line, width)
}

// codeLineNumberWidth reserves a stable gutter: enough digits for the last
// line, plus a separator and one trailing space.
func codeLineNumberWidth(lineCount int) int {
	width := len(fmt.Sprintf("%d", max(1, lineCount)))
	return width + 3
}

func renderCodeLineNumber(line, width int) string {
	return diffMetaStyle.Render(fmt.Sprintf("%*d │ ", width-3, line))
}

func styleDiffLine(line string, width int) string {
	return highlightDiffLine(fitDisplay(line, width))
}

func explorerLeftWidth(width int) int {
	if width < 90 {
		return max(28, width/2)
	}
	return min(48, max(32, width/3))
}

func clamp(n, low, high int) int {
	if n < low {
		return low
	}
	if n > high {
		return high
	}
	return n
}
