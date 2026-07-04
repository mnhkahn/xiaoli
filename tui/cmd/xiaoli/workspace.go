package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const workspaceStateFileName = "tui_workspaces.json"
const maxWorkspaceSessionsPerProject = 3

type workspaceState struct {
	Items []workspaceItem `json:"items"`
}

type workspaceItem struct {
	CWD        string             `json:"cwd"`
	SessionID  string             `json:"session_id,omitempty"`
	Title      string             `json:"title"`
	Model      string             `json:"model"`
	LastOpened time.Time          `json:"last_opened"`
	Sessions   []workspaceSession `json:"sessions,omitempty"`
}

type workspaceSession struct {
	SessionID  string    `json:"session_id"`
	Title      string    `json:"title,omitempty"`
	Model      string    `json:"model,omitempty"`
	LastOpened time.Time `json:"last_opened"`
}

type workspacePicker struct {
	items    []workspaceItem
	selected int
	width    int
	height   int
	err      string
}

func workspaceStatePath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "."
	}
	return filepath.Join(dataDir, "state", workspaceStateFileName)
}

func loadWorkspaces(path string) []workspaceItem {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state workspaceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	items := normalizeWorkspaceItems(state.Items)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].LastOpened.After(items[j].LastOpened)
	})
	return items
}

func loadWorkspaceSessions(path string) []workspaceItem {
	items := loadWorkspaces(path)
	out := make([]workspaceItem, 0)
	for _, item := range items {
		sessions := item.Sessions
		sort.SliceStable(sessions, func(i, j int) bool {
			return sessions[i].LastOpened.After(sessions[j].LastOpened)
		})
		for i, session := range sessions {
			if i >= maxWorkspaceSessionsPerProject {
				break
			}
			if strings.TrimSpace(session.SessionID) == "" {
				continue
			}
			out = append(out, workspaceItem{
				CWD:        item.CWD,
				SessionID:  session.SessionID,
				Title:      firstNonEmpty(session.Title, item.Title),
				Model:      firstNonEmpty(session.Model, item.Model),
				LastOpened: session.LastOpened,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastOpened.After(out[j].LastOpened)
	})
	return out
}

func recordWorkspaceSession(path string, item workspaceItem) error {
	item.CWD = strings.TrimSpace(item.CWD)
	item.SessionID = strings.TrimSpace(item.SessionID)
	if item.CWD == "" || item.SessionID == "" {
		return nil
	}
	if abs, err := filepath.Abs(item.CWD); err == nil {
		item.CWD = abs
	}
	if item.LastOpened.IsZero() {
		item.LastOpened = time.Now()
	}
	state := workspaceState{Items: loadWorkspaces(path)}
	updated := false
	for i := range state.Items {
		if samePath(state.Items[i].CWD, item.CWD) {
			state.Items[i] = mergeWorkspaceSession(state.Items[i], item)
			updated = true
			break
		}
	}
	if !updated {
		item.Sessions = []workspaceSession{workspaceSessionFromItem(item)}
		state.Items = append(state.Items, item)
	}
	state.Items = normalizeWorkspaceItems(state.Items)
	state.Items = enforceUniqueWorkspaceSessions(state.Items)
	sort.SliceStable(state.Items, func(i, j int) bool {
		return state.Items[i].LastOpened.After(state.Items[j].LastOpened)
	})
	if len(state.Items) > 20 {
		state.Items = state.Items[:20]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func mergeWorkspaceSession(old, next workspaceItem) workspaceItem {
	old.CWD = firstNonEmpty(next.CWD, old.CWD)
	old.Title = firstNonEmpty(next.Title, old.Title)
	old.Model = firstNonEmpty(next.Model, old.Model)
	old.LastOpened = next.LastOpened
	nextSession := workspaceSessionFromItem(next)
	replaced := false
	for i := range old.Sessions {
		if old.Sessions[i].SessionID == nextSession.SessionID {
			old.Sessions[i] = nextSession
			replaced = true
			break
		}
	}
	if !replaced {
		old.Sessions = append([]workspaceSession{nextSession}, old.Sessions...)
	}
	old.Sessions = trimWorkspaceSessions(old.Sessions)
	old.SessionID = old.Sessions[0].SessionID
	return old
}

func normalizeWorkspaceItems(items []workspaceItem) []workspaceItem {
	for i := range items {
		if abs, err := filepath.Abs(strings.TrimSpace(items[i].CWD)); err == nil {
			items[i].CWD = abs
		}
		if strings.TrimSpace(items[i].SessionID) != "" && len(items[i].Sessions) == 0 {
			items[i].Sessions = []workspaceSession{workspaceSessionFromItem(items[i])}
		}
		items[i].Sessions = trimWorkspaceSessions(items[i].Sessions)
		if len(items[i].Sessions) > 0 {
			items[i].SessionID = items[i].Sessions[0].SessionID
			items[i].LastOpened = items[i].Sessions[0].LastOpened
			if items[i].Title == "" {
				items[i].Title = items[i].Sessions[0].Title
			}
			if items[i].Model == "" {
				items[i].Model = items[i].Sessions[0].Model
			}
		} else {
			items[i].SessionID = ""
		}
	}
	return enforceUniqueWorkspaceSessions(items)
}

func enforceUniqueWorkspaceSessions(items []workspaceItem) []workspaceItem {
	owner := map[string]int{}
	ownerTime := map[string]time.Time{}
	for i := range items {
		for _, session := range items[i].Sessions {
			sessionID := strings.TrimSpace(session.SessionID)
			if sessionID == "" {
				continue
			}
			if prev, ok := owner[sessionID]; !ok || session.LastOpened.After(ownerTime[sessionID]) {
				owner[sessionID] = i
				ownerTime[sessionID] = session.LastOpened
			} else if ok && prev == i && session.LastOpened.After(ownerTime[sessionID]) {
				ownerTime[sessionID] = session.LastOpened
			}
		}
	}
	for i := range items {
		filtered := items[i].Sessions[:0]
		for _, session := range items[i].Sessions {
			if owner[strings.TrimSpace(session.SessionID)] == i {
				filtered = append(filtered, session)
			}
		}
		items[i].Sessions = trimWorkspaceSessions(filtered)
		if len(items[i].Sessions) > 0 {
			items[i].SessionID = items[i].Sessions[0].SessionID
			items[i].LastOpened = items[i].Sessions[0].LastOpened
		} else {
			items[i].SessionID = ""
		}
	}
	return items
}

func trimWorkspaceSessions(sessions []workspaceSession) []workspaceSession {
	seen := map[string]bool{}
	clean := make([]workspaceSession, 0, len(sessions))
	for _, session := range sessions {
		session.SessionID = strings.TrimSpace(session.SessionID)
		if session.SessionID == "" || seen[session.SessionID] {
			continue
		}
		if session.LastOpened.IsZero() {
			session.LastOpened = time.Now()
		}
		seen[session.SessionID] = true
		clean = append(clean, session)
	}
	sort.SliceStable(clean, func(i, j int) bool {
		return clean[i].LastOpened.After(clean[j].LastOpened)
	})
	if len(clean) > maxWorkspaceSessionsPerProject {
		clean = clean[:maxWorkspaceSessionsPerProject]
	}
	return clean
}

func workspaceSessionFromItem(item workspaceItem) workspaceSession {
	return workspaceSession{
		SessionID:  strings.TrimSpace(item.SessionID),
		Title:      item.Title,
		Model:      item.Model,
		LastOpened: item.LastOpened,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func findWorkspace(path, cwd string) (workspaceItem, bool) {
	for _, item := range loadWorkspaces(path) {
		if samePath(item.CWD, cwd) {
			return item, true
		}
	}
	return workspaceItem{}, false
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(strings.TrimSpace(a))
	bb, errB := filepath.Abs(strings.TrimSpace(b))
	if errA == nil {
		a = aa
	}
	if errB == nil {
		b = bb
	}
	if realA, err := filepath.EvalSymlinks(a); err == nil {
		a = realA
	}
	if realB, err := filepath.EvalSymlinks(b); err == nil {
		b = realB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func newWorkspacePicker(items []workspaceItem, cwd string, width, height int) *workspacePicker {
	p := &workspacePicker{items: append([]workspaceItem(nil), items...), width: width, height: height}
	if len(p.items) == 0 {
		p.err = "No recent projects yet"
		return p
	}
	for i, item := range p.items {
		if samePath(item.CWD, cwd) {
			p.selected = i
			break
		}
	}
	return p
}

func (p *workspacePicker) resize(width, height int) {
	if p == nil {
		return
	}
	p.width = width
	p.height = height
}

func (p *workspacePicker) handleKey(msg tea.KeyMsg) (workspaceItem, bool, bool) {
	if p == nil {
		return workspaceItem{}, false, false
	}
	switch msg.String() {
	case "esc", "q":
		return workspaceItem{}, false, true
	case "up", "k":
		p.move(-1)
		return workspaceItem{}, false, false
	case "down", "j":
		p.move(1)
		return workspaceItem{}, false, false
	case "tab":
		p.move(1)
		if p.selected >= 0 && p.selected < len(p.items) {
			return p.items[p.selected], true, true
		}
		return workspaceItem{}, false, false
	case "shift+tab":
		p.move(-1)
		if p.selected >= 0 && p.selected < len(p.items) {
			return p.items[p.selected], true, true
		}
		return workspaceItem{}, false, false
	case "enter":
		if p.selected >= 0 && p.selected < len(p.items) {
			return p.items[p.selected], true, true
		}
		return workspaceItem{}, false, false
	default:
		return workspaceItem{}, false, false
	}
}

func (p *workspacePicker) move(delta int) {
	if p == nil || len(p.items) == 0 {
		return
	}
	p.selected = (p.selected + delta + len(p.items)) % len(p.items)
}

func (p *workspacePicker) View() string {
	if p == nil {
		return ""
	}
	width := max(40, p.width)
	height := max(10, p.height)
	bodyH := max(4, height-4)
	bodyW := max(20, width-boxStyle.GetHorizontalFrameSize())
	lines := []string{titleStyle.Render("Projects"), ""}
	if p.err != "" {
		lines = append(lines, eventStyle.Render(p.err))
	} else {
		limit := min(len(p.items), bodyH-2)
		start := 0
		if p.selected >= limit {
			start = p.selected - limit + 1
		}
		for i := start; i < min(len(p.items), start+limit); i++ {
			item := p.items[i]
			prefix := "  "
			style := eventStyle
			if i == p.selected {
				prefix = "› "
				style = explorerSelectedStyle()
			}
			lines = append(lines, style.Render(fitDisplay(prefix+workspaceLabel(item, bodyW-2), bodyW)))
		}
	}
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	body := boxStyle.Width(bodyW).Height(bodyH).Render(strings.Join(lines, "\n"))
	help := hintStyle.Render(fitDisplay("Enter switch · Tab next · Shift+Tab prev · Esc close · ↑/↓ move", width))
	return lipgloss.JoinVertical(lipgloss.Left, body, help)
}

func workspaceLabel(item workspaceItem, width int) string {
	cwd := compactPath(item.CWD, max(16, width/3))
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = filepath.Base(filepath.Clean(item.CWD))
	}
	age := relativeAge(item.LastOpened)
	parts := []string{
		cwd,
		truncateDisplay(title, max(12, width/3)),
	}
	if item.SessionID != "" {
		parts = append(parts, shortID(item.SessionID))
	}
	if age != "" {
		parts = append(parts, age)
	}
	return strings.Join(parts, "  ")
}

func relativeAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < time.Minute {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
