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

func newTreeExplorer(cwd string, width, height int) *tuiExplorer {
	ex := &tuiExplorer{
		mode:      explorerTree,
		cwd:       cwd,
		width:     width,
		height:    height,
		expanded:  map[string]bool{"": true},
		leftWidth: explorerLeftWidth(width),
	}
	ex.reloadTree()
	ex.selectFirstFile()
	return ex
}

func newDiffExplorer(cwd string, width, height int) *tuiExplorer {
	ex := &tuiExplorer{
		mode:      explorerDiff,
		cwd:       cwd,
		width:     width,
		height:    height,
		leftWidth: explorerLeftWidth(width),
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
	e.leftWidth = explorerLeftWidth(width)
}

func (e *tuiExplorer) handleKey(msg tea.KeyMsg) (*tuiExplorer, tea.Cmd, bool) {
	if e == nil {
		return e, nil, false
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
		if e.mode == explorerTree && e.editor != nil {
			e.focusRight = true
			return e, nil, true
		}
	case "l", "right":
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
		if e.mode == explorerTree {
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
	rightTotalWidth := max(20, e.width-e.leftWidth)
	rightContentWidth := max(12, rightTotalWidth-boxStyle.GetHorizontalFrameSize())
	previewHeight := max(0, max(4, e.height-5-boxStyle.GetVerticalFrameSize())-3)
	x := msg.X - e.leftWidth - 2
	y := msg.Y - 4
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
		msg.Y >= 4 &&
		msg.Y < 4+previewHeight &&
		len(e.previewLines()) > 0
	return point, ok
}

func (e *tuiExplorer) View() string {
	if e == nil {
		return ""
	}
	width := max(40, e.width)
	height := max(10, e.height)
	e.leftWidth = explorerLeftWidth(width)
	frameW := boxStyle.GetHorizontalFrameSize()
	leftContentWidth := max(12, e.leftWidth-frameW)
	rightTotalWidth := max(20, width-e.leftWidth)
	rightContentWidth := max(12, rightTotalWidth-frameW)
	bodyHeight := max(4, height-5-boxStyle.GetVerticalFrameSize())

	title := "Xiaoli /" + string(e.mode)
	if e.mode == explorerTree {
		title = "Xiaoli /tree"
	}
	header := titleStyle.Render(title) + "  " + hintStyle.Render("cwd: "+compactPath(e.cwd, max(12, width-28)))
	help := hintStyle.Render("tree: j/k move · l/tab edit · editor: i insert · :w save · q close")

	left := e.renderLeft(leftContentWidth, bodyHeight)
	right := e.renderRight(rightContentWidth, bodyHeight)
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
	footer := ""
	if e.err != "" {
		footer = eventStyle.Render(truncateDisplay(e.err, width))
	}
	return lipgloss.JoinVertical(lipgloss.Left, fitDisplay(header, width), body, fitDisplay(help, width), footer)
}

func (e *tuiExplorer) renderLeft(width, height int) string {
	title := "Project Tree"
	if e.mode == explorerDiff {
		title = "Changed Files"
	}
	lines := []string{titleStyle.Render(title), ""}
	visible := e.entries
	start := clamp(e.leftScroll, 0, len(visible))
	end := min(len(visible), start+max(0, height-2))
	for i := start; i < end; i++ {
		entry := visible[i]
		prefix := "  "
		style := eventStyle
		if i == e.selected {
			prefix = "› "
			style = explorerSelectedStyle()
		}
		line := prefix + e.entryLabel(entry)
		lines = append(lines, style.Render(fitDisplay(line, width)))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
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
	lines := []string{titleStyle.Render(title), ""}
	if e.mode == explorerTree && e.editor != nil {
		return e.editor.render(width, height)
	}
	allLines := e.previewLines()
	start := clamp(e.rightScroll, 0, len(allLines))
	end := min(len(allLines), start+max(0, height-3))
	for y := start; y < end; y++ {
		line := allLines[y]
		if e.mode == explorerDiff {
			line = styleDiffLine(line, width)
		} else {
			line = stylePreviewLine(line, width)
		}
		if e.selection.active || e.selection.dragging {
			startSel, endSel := normalizedSelection(e.selection)
			line = renderSelectionOverlayLine(line, y, startSel, endSel, width)
		}
		lines = append(lines, line)
	}
	if e.previewPath != "" {
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
	return max(1, e.height-7)
}

func (e *tuiExplorer) previewHeight() int {
	return max(1, e.height-8)
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
