package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"

	agentchannel "xiaoli/server/internal/agent/channel"
	"xiaoli/server/internal/agent/localapp"
	"xiaoli/server/internal/agent/localconfig"
	agentruntime "xiaoli/server/internal/agent/runtime"
	"xiaoli/server/internal/agent/slash"
	agentbuiltin "xiaoli/server/internal/agent/tool/builtin"
	agentevent "xiaoli/server/internal/event"
)

const (
	channelName = "tui"
	channelUser = "local"
)

type transcriptItem struct {
	role string
	text string
}

type slashSuggestion = slash.Suggestion

type chatDoneMsg struct {
	reply string
	err   error
}

type chatDeltaMsg struct {
	delta string
}

type eventMsg struct {
	event agentevent.Event
}

type model struct {
	app             *localapp.App
	events          chan agentevent.Event
	chatMsgs        chan tea.Msg
	input           textinput.Model
	items           []transcriptItem
	sessionID       string
	cwd             string
	logPath         string
	contextUsage    *agentruntime.ContextUsage
	scroll          int
	viewport        viewport.Model
	streamingIndex  int
	width           int
	height          int
	busy            bool
	status          string
	lastError       string
	pendingBashHash string
	pendingQuestion string
	pendingOptions  []string
	pendingChoice   int
	quitting        bool
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	userStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	agentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("114"))
	eventStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	boxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	sideStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
)

func main() {
	configPath := flag.String("config", "", "path to local xiaoli settings.json")
	initConfig := flag.Bool("init", false, "create default local settings and secrets files")
	prompt := flag.String("prompt", "", "extra system prompt appended after AGENT.md/SOUL.md")
	resumeSession := flag.String("s", "", "session id to resume")
	renderSession := flag.String("render-session", "", "render a session frame and exit")
	renderWidth := flag.Int("width", 160, "render-session terminal width")
	renderHeight := flag.Int("height", 40, "render-session terminal height")
	flag.Parse()

	if *initConfig {
		cfg, err := localconfig.EnsureDefaults(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "xiaoli-tui init: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Created local Xiaoli config under %s\n", cfg.DataDir)
		fmt.Println("Edit settings.json to set models.default, then put API keys in secrets.json or env.")
		return
	}

	logPath := configureTUILogger(*configPath)
	app, err := localapp.New(localapp.Options{ConfigPath: *configPath, Prompt: *prompt, Ensure: *initConfig})
	if err != nil {
		fmt.Fprintf(os.Stderr, "xiaoli-tui: %v\n\n", err)
		fmt.Fprintln(os.Stderr, "Run with -init to create local settings, then configure models.default and API key.")
		os.Exit(1)
	}
	defer app.Close()

	if strings.TrimSpace(*renderSession) != "" {
		m := newModel(app, *renderSession, logPath)
		m.width = *renderWidth
		m.height = *renderHeight
		m.input.Width = max(20, layoutMainWidth(m.width, m.height)-2)
		m.refreshContextUsage()
		m.syncViewport(true)
		fmt.Print(m.View())
		return
	}

	p := tea.NewProgram(newModel(app, *resumeSession, logPath), tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "xiaoli-tui: %v\n", err)
		os.Exit(1)
	}
	if m, ok := finalModel.(model); ok {
		printExitSummary(app, m, *configPath)
	} else {
		printExitSummary(app, model{app: app}, *configPath)
	}
}

func configureTUILogger(configPath string) string {
	cfg, err := localconfig.Load(configPath)
	if err != nil {
		discardTUILogs()
		return ""
	}
	logDir := filepath.Join(cfg.DataDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		discardTUILogs()
		return ""
	}
	logPath := filepath.Join(logDir, "tui.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		discardTUILogs()
		return ""
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	logger.StdLogger = logger.NewWriterLogger(f, log.LstdFlags|log.Lshortfile, 3)
	return logPath
}

func discardTUILogs() {
	log.SetOutput(io.Discard)
	logger.SetOutput(io.Discard)
	logger.StdLogger = logger.NewWriterLogger(io.Discard, log.LstdFlags|log.Lshortfile, 3)
}

func newModel(app *localapp.App, resumeSessionID string, logPath string) model {
	input := textinput.New()
	input.Placeholder = "Ask Xiaoli..."
	input.Focus()
	input.CharLimit = 4096
	input.Width = 80
	cwd, _ := os.Getwd()

	events := make(chan agentevent.Event, 64)
	chatMsgs := make(chan tea.Msg, 128)
	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 3
	app.Bus.SubscribeAll(func(_ context.Context, e agentevent.Event) error {
		select {
		case events <- e:
		default:
		}
		return nil
	})

	items := []transcriptItem{{
		role: "system",
		text: "Xiaoli TUI ready. Press Ctrl+C to quit.",
	}}
	if strings.TrimSpace(resumeSessionID) != "" {
		items = restoreTranscript(app.Agent, resumeSessionID)
		items = append(items, transcriptItem{role: "system", text: "Resumed session " + resumeSessionID})
	}

	return model{
		app:            app,
		events:         events,
		chatMsgs:       chatMsgs,
		input:          input,
		sessionID:      strings.TrimSpace(resumeSessionID),
		cwd:            cwd,
		logPath:        logPath,
		viewport:       vp,
		status:         "idle",
		streamingIndex: -1,
		items:          items,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, waitForEvent(m.events))
}

func printExitSummary(app *localapp.App, m model, configPath string) {
	sessionID := strings.TrimSpace(m.sessionID)
	if sessionID == "" && app != nil && app.Agent != nil && app.Agent.SessionManager() != nil {
		sessionID = app.Agent.SessionManager().GetChannelSession(context.Background(), channelName, channelUser)
	}
	title := "未开始"
	if sessionID != "" && app != nil && app.Agent != nil && app.Agent.SessionManager() != nil {
		if info, err := app.Agent.SessionManager().Get(context.Background(), sessionID); err == nil && strings.TrimSpace(info.Title) != "" {
			title = info.Title
		}
	}
	fmt.Println(exitLogo())
	fmt.Printf("  Session   %s\n", title)
	if sessionID != "" {
		fmt.Printf("  ID        %s\n", sessionID)
		fmt.Printf("  Continue  %s\n", continueCommand(sessionID, configPath))
	} else {
		fmt.Println("  Continue  xiaoli-tui")
	}
}

func exitLogo() string {
	return renderBanner("cyeam.com")
}

func renderBanner(text string) string {
	glyphs := bannerGlyphs()
	lines := make([]string, 5)
	for _, r := range strings.ToUpper(text) {
		glyph, ok := glyphs[r]
		if !ok {
			glyph = glyphs[' ']
		}
		for i := range lines {
			if lines[i] != "" {
				lines[i] += " "
			}
			lines[i] += glyph[i]
		}
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return "\n  " + strings.Join(lines, "\n  ") + "\n"
}

func bannerGlyphs() map[rune][5]string {
	return map[rune][5]string{
		' ': {"   ", "   ", "   ", "   ", "   "},
		'.': {"  ", "  ", "  ", "  ", "o "},
		'A': {" ___ ", "/ _ \\", "| |_|", "|  _|", "|_|  "},
		'C': {" ___ ", "/ __|", "| |  ", "| |__", "\\___|"},
		'E': {" ___ ", "| __|", "| _| ", "| |__", "|___|"},
		'M': {" __  __ ", "|  \\/  |", "| |\\/| |", "| |  | |", "|_|  |_|"},
		'O': {" ___ ", "/ _ \\", "| | |", "| |_|", "\\___/"},
		'Y': {"__   __", "\\ \\ / /", " \\ V / ", "  | |  ", "  |_|  "},
	}
}

func continueCommand(sessionID string, configPath string) string {
	parts := []string{"xiaoli-tui"}
	if strings.TrimSpace(configPath) != "" {
		parts = append(parts, "-config", shellQuote(configPath))
	}
	parts = append(parts, "-s", shellQuote(sessionID))
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return r == '\'' || r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\\'
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		mainW, _, _, _ := layoutSizes(m.width, m.height)
		m.input.Width = max(20, mainW-2)
		m.refreshContextUsage()
		m.syncViewport(false)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+l":
			m.items = []transcriptItem{{role: "system", text: "Transcript cleared."}}
			m.scroll = 0
			m.syncViewport(true)
			return m, nil
		case "ctrl+y":
			text := latestAssistantText(m.items)
			if strings.TrimSpace(text) == "" {
				m.items = append(m.items, transcriptItem{role: "event", text: "没有可复制的 assistant 回复。"})
			} else if err := copyTextToClipboard(text); err != nil {
				m.items = append(m.items, transcriptItem{role: "error", text: "复制失败：" + err.Error()})
			} else {
				m.items = append(m.items, transcriptItem{role: "event", text: "已复制最近一条 assistant 回复。"})
			}
			m.syncViewport(true)
			return m, nil
		case "home":
			m.viewport.GotoTop()
			return m, nil
		case "end":
			m.viewport.GotoBottom()
			return m, nil
		case "left", "shift+tab":
			if m.hasPendingOptions() {
				m.pendingChoice = (m.pendingChoice + len(m.pendingOptions) - 1) % len(m.pendingOptions)
				return m, nil
			}
		case "right":
			if m.hasPendingOptions() {
				m.pendingChoice = (m.pendingChoice + 1) % len(m.pendingOptions)
				return m, nil
			}
		case "tab":
			if m.hasPendingOptions() {
				m.pendingChoice = (m.pendingChoice + 1) % len(m.pendingOptions)
				return m, nil
			}
			if suggestions := m.slashSuggestions(1); len(suggestions) > 0 {
				m.input.SetValue("/" + suggestions[0].Name + " ")
				m.input.CursorEnd()
				return m, nil
			}
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" && m.hasPendingOptions() {
				text = m.pendingOptions[m.pendingChoice]
			}
			if text == "" || m.busy {
				return m, nil
			}
			if selected, ok := m.pendingOptionByInput(text); ok {
				text = selected
			}
			if strings.EqualFold(text, "/quit") || strings.EqualFold(text, "/exit") {
				m.quitting = true
				return m, tea.Quit
			}
			if m.handleCopyCommand(text) {
				m.input.SetValue("")
				m.syncViewport(true)
				return m, nil
			}
			m.lastError = ""
			m.items = append(m.items, transcriptItem{role: "user", text: text})
			if m.pendingBashHash != "" {
				if isReject(text) {
					agentbuiltin.ClearBashApproval(m.activeSessionID())
					m.pendingBashHash = ""
					m.pendingQuestion = ""
					m.pendingOptions = nil
					m.pendingChoice = 0
					m.input.SetValue("")
					m.items = append(m.items, transcriptItem{role: "system", text: "已拒绝执行命令。"})
					return m, nil
				}
				if isApprove(text) {
					agentbuiltin.StoreBashApproval(m.activeSessionID(), m.pendingBashHash)
					m.pendingBashHash = ""
					m.pendingQuestion = ""
				}
			}
			if m.hasPendingOptions() {
				m.pendingQuestion = ""
				m.pendingOptions = nil
				m.pendingChoice = 0
			}
			if cmd := m.handleSlash(text); cmd.handled {
				m.input.SetValue("")
				if cmd.sessionID != "" && cmd.sessionID != m.sessionID {
					m.sessionID = cmd.sessionID
					m.items = restoreTranscript(m.app.Agent, m.sessionID)
					m.items = append(m.items, transcriptItem{role: "system", text: cmd.reply})
				} else {
					m.items = append(m.items, transcriptItem{role: "system", text: cmd.reply})
				}
				m.status = "idle"
				m.refreshContextUsage()
				m.syncViewport(true)
				return m, nil
			} else if cmd.prompt != "" {
				text = cmd.prompt
			}
			m.input.SetValue("")
			m.busy = true
			m.status = "running"
			m.streamingIndex = -1
			m.scroll = 0
			m.syncViewport(true)
			return m, tea.Batch(startChat(m.app.Agent, m.chatMsgs, m.sessionID, text), waitForChat(m.chatMsgs), waitForEvent(m.events))
		}
	case chatDeltaMsg:
		if msg.delta != "" {
			if m.streamingIndex < 0 || m.streamingIndex >= len(m.items) {
				m.items = append(m.items, transcriptItem{role: "assistant", text: ""})
				m.streamingIndex = len(m.items) - 1
			}
			m.items[m.streamingIndex].text += msg.delta
			m.scroll = 0
			m.syncViewport(true)
		}
		m.refreshContextUsage()
		return m, waitForChat(m.chatMsgs)
	case chatDoneMsg:
		m.busy = false
		m.status = "idle"
		if m.sessionID == "" {
			if sid := m.currentChannelSession(); sid != "" {
				m.sessionID = sid
			}
		}
		if msg.err != nil {
			m.lastError = msg.err.Error()
			m.items = append(m.items, transcriptItem{role: "error", text: msg.err.Error()})
		} else {
			if m.streamingIndex >= 0 && m.streamingIndex < len(m.items) {
				if strings.TrimSpace(msg.reply) != "" {
					m.items[m.streamingIndex].text = msg.reply
				}
			} else {
				m.items = append(m.items, transcriptItem{role: "assistant", text: msg.reply})
			}
		}
		m.streamingIndex = -1
		if ask := m.app.Agent.ConsumeAskData(channelUser); ask != nil {
			m.pendingQuestion = ask.Question
			m.pendingOptions = append([]string(nil), ask.Options...)
			m.pendingChoice = 0
			if ask.BashHash != "" {
				m.pendingBashHash = ask.BashHash
				m.status = "waiting approval"
			} else if len(m.pendingOptions) > 0 {
				m.status = "waiting input"
			}
			m.items = append(m.items, transcriptItem{role: "system", text: formatAsk(ask)})
		}
		m.refreshContextUsage()
		m.syncViewport(true)
		return m, waitForEvent(m.events)
	case eventMsg:
		m.items = append(m.items, transcriptItem{role: "event", text: eventSummary(msg.event)})
		m.refreshContextUsage()
		m.syncViewport(true)
		return m, waitForEvent(m.events)
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, tea.Batch(vpCmd, cmd)
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "Loading..."
	}
	mainW, sideW, bodyH, promptW := layoutSizes(m.width, m.height)

	m.syncViewport(false)
	transcript := m.viewport.View()
	topParts := []string{
		boxStyle.Width(mainW).Height(bodyH).Render(transcript),
	}
	if sideW > 0 {
		sidebar := renderSidebar(m, sideW, bodyH)
		topParts = append(topParts, sideStyle.Width(sideW).Height(bodyH).Render(sidebar))
	}
	top := lipgloss.JoinHorizontal(lipgloss.Top, topParts...)

	promptParts := []string{}
	if suggestions := m.slashSuggestions(8); len(suggestions) > 0 {
		promptParts = append(promptParts, renderSlashSuggestions(suggestions, promptW-2))
	}
	if m.hasPendingOptions() {
		promptParts = append(promptParts, renderPendingOptions(m.pendingOptions, m.pendingChoice, promptW-2))
	}
	promptParts = append(promptParts, m.input.View())
	prompt := boxStyle.Width(promptW).Render(strings.Join(promptParts, "\n"))
	return lipgloss.JoinVertical(lipgloss.Left, top, prompt)
}

func layoutSizes(width, height int) (mainW, sideW, bodyH, promptW int) {
	sideW = 30
	bodyH = max(8, height-5)
	promptW = max(20, width-boxStyle.GetHorizontalFrameSize())
	topGap := 1
	mainW = width - sideW - boxStyle.GetHorizontalFrameSize() - sideStyle.GetHorizontalFrameSize() - topGap
	if mainW < 40 {
		mainW = max(20, width-sideStyle.GetHorizontalFrameSize()-topGap)
		sideW = 0
	}
	return mainW, sideW, bodyH, promptW
}

func layoutMainWidth(width, height int) int {
	mainW, _, _, _ := layoutSizes(width, height)
	return mainW
}

func (m *model) syncViewport(gotoBottom bool) {
	if m == nil || m.width <= 0 || m.height <= 0 {
		return
	}
	mainW, _, bodyH, _ := layoutSizes(m.width, m.height)
	if mainW <= 0 || bodyH <= 0 {
		return
	}
	atBottom := m.viewport.AtBottom()
	m.viewport.Width = mainW
	m.viewport.Height = bodyH
	m.viewport.SetContent(renderTranscriptContent(m.items, mainW))
	if gotoBottom || atBottom {
		m.viewport.GotoBottom()
	}
}

func renderTranscriptContent(items []transcriptItem, width int) string {
	lines := make([]string, 0, len(items)*2)
	textWidth := max(20, width-2)
	for _, item := range items {
		var plain string
		style := eventStyle
		switch item.role {
		case "user":
			plain = wrapWithPrefix("› ", item.text, textWidth)
			style = userStyle
		case "assistant":
			plain = wrapText(item.text, textWidth)
			style = agentStyle
		case "event":
			plain = wrapWithPrefix("· ", item.text, textWidth)
			style = eventStyle
		case "error":
			plain = wrapWithPrefix("error: ", item.text, textWidth)
			style = errStyle
		default:
			plain = wrapText(item.text, textWidth)
			style = eventStyle
		}
		for _, line := range strings.Split(plain, "\n") {
			lines = append(lines, style.Render(fitDisplay(line, width)))
		}
		lines = append(lines, "")
	}
	if len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func renderTranscript(items []transcriptItem, width, height int, scroll int) string {
	lines := make([]string, 0, len(items)*2)
	textWidth := max(20, width-5)
	for _, item := range items {
		switch item.role {
		case "user":
			lines = append(lines, userStyle.Render(wrapWithPrefix("› ", item.text, textWidth)))
		case "assistant":
			lines = append(lines, agentStyle.Render(wrapText(item.text, textWidth)))
		case "event":
			lines = append(lines, eventStyle.Render(wrapWithPrefix("· ", item.text, textWidth)))
		case "error":
			lines = append(lines, errStyle.Render(wrapWithPrefix("error: ", item.text, textWidth)))
		default:
			lines = append(lines, eventStyle.Render(wrapText(item.text, textWidth)))
		}
	}
	content := strings.Join(lines, "\n\n")
	renderedLines := strings.Split(content, "\n")
	totalLines := len(renderedLines)
	if len(renderedLines) > height {
		maxScroll := len(renderedLines) - height
		if scroll > maxScroll {
			scroll = maxScroll
		}
		if scroll < 0 {
			scroll = 0
		}
		end := len(renderedLines) - scroll
		start := end - height
		if start < 0 {
			start = 0
		}
		renderedLines = renderedLines[start:end]
	}
	return renderScrollGutter(renderedLines, width, height, totalLines, scroll)
}

func wrapText(text string, width int) string {
	return lipgloss.NewStyle().Width(max(1, width)).Render(text)
}

func wrapWithPrefix(prefix, text string, width int) string {
	if width <= len([]rune(prefix))+1 {
		return wrapText(prefix+text, width)
	}
	bodyWidth := width - len([]rune(prefix))
	wrapped := strings.Split(wrapText(text, bodyWidth), "\n")
	for i := range wrapped {
		if i == 0 {
			wrapped[i] = prefix + wrapped[i]
		} else {
			wrapped[i] = strings.Repeat(" ", len([]rune(prefix))) + wrapped[i]
		}
	}
	return strings.Join(wrapped, "\n")
}

func renderScrollGutter(lines []string, width, height, totalLines, scroll int) string {
	if height <= 0 {
		return ""
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	gutterX := max(1, width-1)
	if totalLines <= height {
		for i := range lines {
			lines[i] = fitDisplay(lines[i], gutterX) + " "
		}
		return strings.Join(lines, "\n")
	}
	maxScroll := totalLines - height
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	thumbHeight := max(1, height*height/totalLines)
	trackHeight := max(1, height-thumbHeight)
	topLine := maxScroll - scroll
	thumbTop := 0
	if maxScroll > 0 {
		thumbTop = topLine * trackHeight / maxScroll
	}
	for i := range lines {
		bar := "│"
		if i >= thumbTop && i < thumbTop+thumbHeight {
			bar = "█"
		}
		lines[i] = fitDisplay(lines[i], gutterX) + eventStyle.Render(bar)
	}
	return strings.Join(lines, "\n")
}

func fitDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w == width {
		return s
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return truncateDisplay(s, width)
}

func padDisplay(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return truncateDisplay(s, width)
	}
	return s + strings.Repeat(" ", width-w)
}

func renderSidebar(m model, width, height int) string {
	if width <= 0 {
		return ""
	}
	bodyWidth := max(8, width-4)
	keys := []string{
		"keys",
		"enter send",
		"tab choice",
		"keys scroll",
		"ctrl+y copy reply",
		"esc quit",
	}
	top := sidebarTopLines(m, bodyWidth)
	middle := sidebarMiddleLines(m, bodyWidth, max(0, height-len(top)-len(keys)))
	lines := composeSidebar(top, middle, keys, height)
	return strings.Join(lines, "\n")
}

func composeSidebar(top, middle, keys []string, height int) []string {
	if height <= 0 {
		return nil
	}
	if len(keys) > height {
		return append([]string{}, keys[:height]...)
	}
	topBudget := height - len(keys)
	if len(top) > topBudget {
		top = truncateLines(top, topBudget)
		middle = nil
	} else {
		middle = truncateLines(middle, height-len(keys)-len(top))
	}
	lines := append([]string{}, top...)
	lines = append(lines, middle...)
	for len(lines)+len(keys) < height {
		lines = append(lines, "")
	}
	lines = append(lines, keys...)
	return lines
}

func truncateLines(lines []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	if len(lines) <= limit {
		return lines
	}
	if limit == 1 {
		return []string{"..."}
	}
	out := append([]string{}, lines[:limit-1]...)
	return append(out, "...")
}

func sidebarTopLines(m model, width int) []string {
	lines := []string{
		titleStyle.Render("Xiaoli"),
		"",
		fmt.Sprintf("status: %s", m.status),
		"model: " + truncateDisplay(m.app.Agent.CurrentLLMModel(), width),
	}
	if m.sessionID != "" {
		lines = append(lines, "session: "+shortID(m.sessionID))
	}
	if m.cwd != "" {
		lines = append(lines, "cwd: "+compactPath(m.cwd, width-5))
	}
	if ctxUsage := m.contextUsage; ctxUsage != nil && ctxUsage.ContextLength > 0 {
		lines = append(lines, "", "context")
		lines = append(lines, strings.Split(renderContextUsage(ctxUsage, width), "\n")...)
	}
	if m.logPath != "" {
		lines = append(lines, "log: "+truncateDisplay(filepath.Base(m.logPath), width))
	}
	return lines
}

func sidebarMiddleLines(m model, width int, budget int) []string {
	if budget <= 0 {
		return nil
	}
	var sections [][]string
	if m.pendingQuestion != "" {
		section := []string{"", "pending"}
		section = append(section, limitLines(wrapText(m.pendingQuestion, width), 2)...)
		if len(m.pendingOptions) > 0 {
			section = append(section, limitLines(wrapText("choose: "+strings.Join(m.pendingOptions, " / "), width), 2)...)
		}
		sections = append(sections, section)
	}
	if m.lastError != "" {
		section := []string{"", errStyle.Render("last error")}
		for _, line := range limitLines(wrapText(m.lastError, width), 2) {
			section = append(section, errStyle.Render(line))
		}
		sections = append(sections, section)
	}
	if tasks := m.app.Agent.TaskStatusList(); len(tasks) > 0 {
		section := []string{"", "tasks"}
		for i, task := range tasks {
			if i >= 3 {
				section = append(section, fmt.Sprintf("+%d more", len(tasks)-i))
				break
			}
			section = append(section, truncateDisplay(fmt.Sprintf("- %s %s", task.ID, task.Status), width))
		}
		sections = append(sections, section)
	}
	statuses := m.app.Agent.MCPStatus()
	if len(statuses) > 0 {
		section := []string{"", "MCP"}
		for i, s := range statuses {
			if i >= 3 {
				section = append(section, fmt.Sprintf("+%d more", len(statuses)-i))
				break
			}
			state := "down"
			if s.Connected {
				state = "up"
			}
			section = append(section, truncateDisplay(fmt.Sprintf("- %s %s", state, s.URL), width))
		}
		sections = append(sections, section)
	}

	var out []string
	for _, section := range sections {
		if len(out)+len(section) > budget {
			remaining := budget - len(out)
			if remaining <= 0 {
				break
			}
			out = append(out, section[:remaining]...)
			break
		}
		out = append(out, section...)
	}
	return out
}

func limitLines(text string, limit int) []string {
	lines := strings.Split(text, "\n")
	if limit > 0 && len(lines) > limit {
		lines = append(lines[:limit], "...")
	}
	return lines
}

func truncateDisplay(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	if width <= 3 {
		runes := []rune(s)
		for len(runes) > 0 && lipgloss.Width(string(runes)) > width {
			runes = runes[:len(runes)-1]
		}
		return string(runes)
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)+"...") > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "..."
}

func (m *model) refreshContextUsage() {
	if m == nil || m.app == nil || m.app.Agent == nil {
		return
	}
	ctxUsage := m.app.Agent.CurrentContext(context.Background(), channelName, channelUser)
	if ctxUsage != nil && ctxUsage.ContextLength > 0 {
		m.contextUsage = ctxUsage
	}
}

func (m model) slashSuggestions(limit int) []slashSuggestion {
	value := strings.TrimSpace(m.input.Value())
	if !strings.HasPrefix(value, "/") || strings.Contains(strings.TrimPrefix(value, "/"), " ") {
		return nil
	}
	deps := &tuiSlashDeps{app: m.app, currentSessionID: m.sessionID}
	handler := slash.NewHandler(deps)
	out := handler.Suggestions(context.Background(), value)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func renderSlashSuggestions(items []slashSuggestion, width int) string {
	var lines []string
	for i, item := range items {
		prefix := "  "
		if i == 0 {
			prefix = "> "
		}
		desc := strings.TrimSpace(item.Description)
		line := fmt.Sprintf("%s/%s", prefix, item.Name)
		if item.Kind != "" {
			line += " [" + item.Kind + "]"
		}
		if desc != "" {
			line += " - " + desc
		}
		if width > 0 && len([]rune(line)) > width {
			runes := []rune(line)
			line = string(runes[:max(0, width-3)]) + "..."
		}
		lines = append(lines, hintStyle.Render(line))
	}
	if len(lines) > 0 {
		lines = append(lines, hintStyle.Render("Tab complete"))
	}
	return strings.Join(lines, "\n")
}

func renderPendingOptions(options []string, selected int, width int) string {
	if len(options) == 0 {
		return ""
	}
	if selected < 0 || selected >= len(options) {
		selected = 0
	}
	parts := make([]string, 0, len(options))
	for i, opt := range options {
		label := fmt.Sprintf("%d %s", i+1, opt)
		if i == selected {
			label = "[" + label + "]"
		}
		parts = append(parts, label)
	}
	line := "选择: " + strings.Join(parts, "  ")
	if width > 0 && lipgloss.Width(line) > width {
		line = truncateDisplay(line, width)
	}
	return hintStyle.Render(line)
}

func (m model) hasPendingOptions() bool {
	return len(m.pendingOptions) > 0
}

func (m model) pendingOptionByInput(text string) (string, bool) {
	if !m.hasPendingOptions() {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	if n, err := strconv.Atoi(text); err == nil && n >= 1 && n <= len(m.pendingOptions) {
		return m.pendingOptions[n-1], true
	}
	for _, opt := range m.pendingOptions {
		if strings.EqualFold(text, opt) {
			return opt, true
		}
	}
	return "", false
}

func (m *model) handleCopyCommand(text string) bool {
	args := strings.Fields(strings.TrimSpace(text))
	if len(args) == 0 || args[0] != "/copy" {
		return false
	}
	target := "reply"
	if len(args) > 1 {
		target = strings.ToLower(args[1])
	}
	var content string
	var label string
	switch target {
	case "all", "transcript":
		content = transcriptPlainText(m.items)
		label = "全部 transcript"
	default:
		content = latestAssistantText(m.items)
		label = "最近一条 assistant 回复"
	}
	if strings.TrimSpace(content) == "" {
		m.items = append(m.items, transcriptItem{role: "event", text: "没有可复制的内容。"})
		return true
	}
	if err := copyTextToClipboard(content); err != nil {
		m.items = append(m.items, transcriptItem{role: "error", text: "复制失败：" + err.Error()})
		return true
	}
	m.items = append(m.items, transcriptItem{role: "event", text: "已复制" + label + "。"})
	return true
}

func (m model) handleSlash(text string) slashResult {
	ctx := context.WithValue(context.Background(), slash.CtxDeviceID, channelUser)
	ctx = context.WithValue(ctx, slash.CtxChannelName, channelName)
	result := slashResult{sessionID: m.sessionID}
	deps := &tuiSlashDeps{
		app:              m.app,
		currentSessionID: m.sessionID,
		setSession: func(id string) {
			result.sessionID = id
		},
	}
	handler := slash.NewHandler(deps)
	if reply, handled := handler.Handle(ctx, agentchannel.TypeLark, text); handled {
		result.handled = true
		result.reply = reply
		return result
	}
	if prompt, ok := handler.SkillPrompt(ctx, text); ok {
		result.prompt = prompt
	}
	return result
}

func (m model) currentChannelSession() string {
	if m.app == nil || m.app.Agent == nil || m.app.Agent.SessionManager() == nil {
		return ""
	}
	return m.app.Agent.SessionManager().GetChannelSession(context.Background(), channelName, channelUser)
}

func (m model) activeSessionID() string {
	if m.sessionID != "" {
		return m.sessionID
	}
	return m.currentChannelSession()
}

type slashResult struct {
	handled   bool
	reply     string
	prompt    string
	sessionID string
}

func restoreTranscript(agent *agentruntime.Agent, sessionID string) []transcriptItem {
	items := []transcriptItem{{role: "system", text: "Resumed session " + sessionID}}
	if agent == nil || agent.MemoryReader() == nil || sessionID == "" {
		return items
	}
	raw, err := agent.MemoryReader().LoadRaw(context.Background(), sessionID)
	if err != nil {
		return items
	}
	var msgs []*schema.Message
	if err := json.Unmarshal(raw.Raw, &msgs); err != nil {
		return items
	}
	for _, msg := range msgs {
		if msg == nil || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		role := "system"
		switch msg.Role {
		case schema.User:
			role = "user"
		case schema.Assistant:
			role = "assistant"
		}
		items = append(items, transcriptItem{role: role, text: msg.Content})
	}
	return items
}

func startChat(agent *agentruntime.Agent, out chan<- tea.Msg, sessionID, text string) tea.Cmd {
	return func() tea.Msg {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			reply, err := agent.ChatWithContextOptions(ctx, channelUser, channelUser, text, agentruntime.ChatOptions{
				Channel:   channelName,
				SessionID: sessionID,
				Stream: func(delta string) bool {
					select {
					case out <- chatDeltaMsg{delta: delta}:
						return true
					case <-ctx.Done():
						return false
					}
				},
			})
			out <- chatDoneMsg{reply: reply, err: err}
		}()
		return nil
	}
}

func waitForChat(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func waitForEvent(ch <-chan agentevent.Event) tea.Cmd {
	return func() tea.Msg {
		e := <-ch
		return eventMsg{event: e}
	}
}

func eventSummary(e agentevent.Event) string {
	switch e.Type {
	case agentevent.TypeAgentRunStarted:
		return "run started"
	case agentevent.TypeAgentRunCompleted:
		return "run completed"
	case agentevent.TypeAgentRunFailed:
		return "run failed"
	case agentevent.TypeAgentToolStarted:
		if data, ok := e.Data.(map[string]any); ok {
			return fmt.Sprintf("tool started: %v", data["name"])
		}
		return "tool started"
	case agentevent.TypeAgentToolFinished:
		if data, ok := e.Data.(map[string]any); ok {
			if errText, ok := data["error"].(string); ok && errText != "" {
				return fmt.Sprintf("tool failed: %v: %s", data["name"], errText)
			}
			return fmt.Sprintf("tool finished: %v", data["name"])
		}
		return "tool finished"
	default:
		return e.Type
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func compactPath(path string, maxLen int) string {
	if maxLen < 12 || len(path) <= maxLen {
		return path
	}
	parts := strings.Split(path, string(os.PathSeparator))
	if len(parts) >= 2 {
		last := parts[len(parts)-1]
		parent := parts[len(parts)-2]
		short := "..." + string(os.PathSeparator) + parent + string(os.PathSeparator) + last
		if len(short) <= maxLen {
			return short
		}
	}
	return "..." + path[len(path)-maxLen+3:]
}

func renderContextUsage(ctx *agentruntime.ContextUsage, width int) string {
	if ctx == nil || ctx.ContextLength <= 0 {
		return ""
	}
	pct := float64(ctx.EstimatedInput) / float64(ctx.ContextLength) * 100
	if pct < 0 {
		pct = 0
	}
	barWidth := max(8, min(18, width-10))
	filled := int(float64(barWidth) * pct / 100)
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	var b strings.Builder
	fmt.Fprintf(&b, "%s / %s\n", commaInt(ctx.EstimatedInput), commaInt(ctx.ContextLength))
	fmt.Fprintf(&b, "%.1f%% %s", pct, bar)
	if ctx.MaxTokens > 0 {
		fmt.Fprintf(&b, "\nout: %s", commaInt(ctx.MaxTokens))
	}
	if ctx.CompressAt > 0 {
		fmt.Fprintf(&b, "\ncompact: %s", commaInt(ctx.CompressAt))
	}
	return b.String()
}

func commaInt(n int) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return sign + s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return sign + strings.Join(parts, ",")
}

func isApprove(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "允许", "同意", "批准", "yes", "y", "ok", "approve":
		return true
	default:
		return false
	}
}

func isReject(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "拒绝", "不同意", "否", "不", "no", "n", "reject", "deny":
		return true
	default:
		return false
	}
}

func formatAsk(ask *agentbuiltin.AskData) string {
	if ask == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(ask.Question)
	if len(ask.Options) > 0 {
		b.WriteString("\n选项：")
		b.WriteString(strings.Join(ask.Options, " / "))
	}
	return b.String()
}

func latestAssistantText(items []transcriptItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].role == "assistant" && strings.TrimSpace(items[i].text) != "" {
			return items[i].text
		}
	}
	return ""
}

func transcriptPlainText(items []transcriptItem) string {
	var lines []string
	for _, item := range items {
		text := strings.TrimSpace(item.text)
		if text == "" {
			continue
		}
		role := item.role
		if role == "" {
			role = "system"
		}
		lines = append(lines, role+": "+text)
	}
	return strings.Join(lines, "\n\n")
}

func copyTextToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
	default:
		if path, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command(path)
		} else if path, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command(path, "-selection", "clipboard")
		} else if path, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command(path, "--clipboard", "--input")
		} else {
			return fmt.Errorf("未找到可用剪贴板程序（wl-copy/xclip/xsel）")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
