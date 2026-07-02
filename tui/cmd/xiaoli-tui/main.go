package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"

	agentchannel "github.com/mnhkahn/xiaoli-esp32/internal/agent/channel"
	"github.com/mnhkahn/xiaoli-esp32/internal/agent/localapp"
	"github.com/mnhkahn/xiaoli-esp32/internal/agent/localconfig"
	agentruntime "github.com/mnhkahn/xiaoli-esp32/internal/agent/runtime"
	"github.com/mnhkahn/xiaoli-esp32/internal/agent/slash"
	agentbuiltin "github.com/mnhkahn/xiaoli-esp32/internal/agent/tool/builtin"
	agentevent "github.com/mnhkahn/xiaoli-esp32/internal/event"
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

type gitSyncTickMsg struct{}

type gitSyncDoneMsg struct {
	action string
	output string
	err    error
}

type bashApprovalDoneMsg struct {
	command   string
	output    string
	err       error
	sessionID string
}

type gitSyncState struct {
	Branch string
	Ahead  int
	Behind int
	Dirty  int
	Valid  bool
}

func (s gitSyncState) Actionable() bool {
	return s.Valid && (s.Ahead > 0 || s.Behind > 0)
}

type gitSyncFeedback struct {
	Loading bool
	Action  string
	Result  string
	Frame   int
}

type model struct {
	app             *localapp.App
	events          chan agentevent.Event
	chatMsgs        chan tea.Msg
	activeCancel    context.CancelFunc
	chatCanceled    bool
	input           textinput.Model
	inputHistory    []string
	historyIndex    int
	historyDraft    string
	shellHistory    []string
	shellHistIndex  int
	shellHistDraft  string
	items           []transcriptItem
	sessionID       string
	cwd             string
	gitStatus       string
	gitSync         gitSyncState
	gitSyncFeedback gitSyncFeedback
	logPath         string
	contextUsage    *agentruntime.ContextUsage
	scroll          int
	viewport        viewport.Model
	streamingIndex  int
	hadChatInput    bool
	width           int
	height          int
	busy            bool
	status          string
	lastError       string
	pendingBashHash string
	pendingQuestion string
	pendingOptions  []string
	pendingChoice   int
	pendingGitCmsg  gitCmsgPending
	explorer        *tuiExplorer
	mouseEnabled    bool
	quitting        bool
}

var (
	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	userStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	agentStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("114"))
	shellStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	eventStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	hintStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	boxStyle        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	sideStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
	gitOKStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	gitPushStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true)
	gitPullStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	gitDirtyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	gitActionStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true)
	gitLoadingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	gitResultStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	gitFailedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
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
		if localconfig.NeedsModelWizard(cfg) {
			fmt.Println("No local model configured yet. Let's set one up.")
			cfg, err = localconfig.RunModelWizard(*configPath, os.Stdin, os.Stdout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "xiaoli-tui init model: %v\n", err)
				os.Exit(1)
			}
		}
		fmt.Printf("Created local Xiaoli config under %s\n", cfg.DataDir)
		fmt.Println("You can edit settings.json and secrets.json later to adjust models.")
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
	gitSync := gitSyncStateForCWD(cwd)

	return model{
		app:            app,
		events:         events,
		chatMsgs:       chatMsgs,
		input:          input,
		sessionID:      strings.TrimSpace(resumeSessionID),
		cwd:            cwd,
		gitStatus:      gitSync.Format(),
		gitSync:        gitSync,
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
	fmt.Println(exitLogo())
	if !m.hadChatInput {
		return
	}
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
	fmt.Printf("  Session   %s\n", title)
	if sessionID != "" {
		fmt.Printf("  ID        %s\n", sessionID)
		fmt.Printf("  Continue  %s\n", continueCommand(sessionID, configPath))
	} else {
		fmt.Println("  Continue  xiaoli-tui")
	}
}

func exitLogo() string {
	return renderFigletLogo()
}

func renderFigletLogo() string {
	lines := []string{
		"  ___ _   _  ___  __ _ _ __ ___      ___ ___  _ __ ___",
		" / __| | | |/ _ \\/ _` | '_ ` _ \\    / __/ _ \\| '_ ` _ \\",
		"| (__| |_| |  __/ (_| | | | | | |  | (_| (_) | | | | | |",
		" \\___|\\__, |\\___|\\__,_|_| |_| |_| (_)___\\___/|_| |_| |_|",
		"      |___/",
	}
	mainColors := []lipgloss.Color{"81", "45", "51", "49", "86"}
	shadow := lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Faint(true)
	fill := lipgloss.NewStyle().Foreground(lipgloss.Color("30")).Faint(true)
	var out []string
	out = append(out, "")
	out = append(out, "")
	for _, line := range lines {
		out = append(out, fill.Render("   "+line))
	}
	out = append(out, fmt.Sprintf("\x1b[%dA", len(lines)+1))
	for _, line := range lines {
		out = append(out, shadow.Render("    "+line))
	}
	out = append(out, fmt.Sprintf("\x1b[%dA", len(lines)))
	for i, line := range lines {
		color := mainColors[i%len(mainColors)]
		out = append(out, lipgloss.NewStyle().Bold(true).Foreground(color).Render("  "+line))
	}
	out = append(out, "")
	return strings.Join(out, "\n")
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
		if m.explorer != nil {
			m.explorer.resize(m.width, m.height)
		}
		mainW, _, _, _ := layoutSizes(m.width, m.height)
		m.input.Width = max(20, mainW-2)
		m.refreshContextUsage()
		m.syncViewport(false)
		return m, nil
	case tea.MouseMsg:
		if !m.mouseEnabled {
			return m, nil
		}
		if m.explorer != nil && m.explorer.handleMouse(msg) {
			return m, nil
		}
		if m.handleGitSyncClick(msg) {
			return m, tea.Batch(startGitSync(m.cwd, m.gitSync), tickGitSyncSpinner())
		}
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		return m, vpCmd
	case tea.KeyMsg:
		if m.explorer != nil {
			next, cmd, handled := m.explorer.handleKey(msg)
			if handled {
				m.explorer = next
				if m.explorer == nil {
					m.refreshGitSync()
				}
				return m, cmd
			}
		}
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "esc":
			if m.busy {
				if m.activeCancel != nil {
					m.activeCancel()
					m.activeCancel = nil
				}
				m.chatCanceled = true
				m.busy = false
				m.status = "idle"
				m.items = append(m.items, transcriptItem{role: "event", text: "已暂停当前执行。"})
				m.syncViewport(true)
				return m, nil
			}
			m.clearInputDraft()
			return m, nil
		case "ctrl+o":
			m.mouseEnabled = !m.mouseEnabled
			if m.mouseEnabled {
				return m, tea.EnableMouseCellMotion
			}
			return m, tea.DisableMouse
		case "ctrl+k":
			if m.busy || m.hasPendingOptions() {
				return m, nil
			}
			m.clearInputDraft()
			m.explorer = newDiffExplorer(m.cwd, m.width, m.height)
			return m, nil
		case "ctrl+s":
			if m.startGitSyncFeedback() {
				return m, tea.Batch(startGitSync(m.cwd, m.gitSync), tickGitSyncSpinner())
			}
			return m, nil
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
		case "up":
			if m.navigateInputHistory(-1) {
				return m, nil
			}
		case "down":
			if m.navigateInputHistory(1) {
				return m, nil
			}
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
			if isShellInput(m.input.Value()) {
				if suggestions := m.shellSuggestions(1); len(suggestions) > 0 {
					m.input.SetValue(applyShellCompletion(m.input.Value(), suggestions[0].Name))
					m.input.CursorEnd()
					return m, nil
				}
			}
			if suggestions := m.slashSuggestions(1); len(suggestions) > 0 {
				m.input.SetValue("/" + suggestions[0].Name + " ")
				m.input.CursorEnd()
				return m, nil
			}
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" && m.hasPendingOptions() {
				text = pendingOptionValue(m.pendingOptions[m.pendingChoice])
			}
			if text == "" || m.busy {
				return m, nil
			}
			if isShellInput(m.input.Value()) {
				command := shellCommand(m.input.Value())
				if command == "" {
					return m, nil
				}
				m.recordShellHistory(command)
				m.input.SetValue("")
				m.busy = true
				m.status = "shell running"
				m.items = append(m.items, transcriptItem{role: "event", text: "shell started: " + command})
				m.syncViewport(true)
				runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				m.activeCancel = cancel
				m.chatCanceled = false
				return m, startShellCommand(runCtx, m.cwd, command)
			}
			m.recordInputHistory(text)
			if selected, ok := m.pendingOptionByInput(text); ok {
				text = selected
			}
			if strings.EqualFold(text, "/quit") || strings.EqualFold(text, "/exit") {
				m.quitting = true
				return m, tea.Quit
			}
			if explorer := m.openExplorerCommand(text); explorer != nil {
				m.explorer = explorer
				m.input.SetValue("")
				return m, nil
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
					sessionID := m.activeSessionID()
					command, ok := agentbuiltin.PendingBashCommand(sessionID, m.pendingBashHash)
					agentbuiltin.ClearBashApproval(sessionID)
					m.pendingBashHash = ""
					m.pendingQuestion = ""
					m.pendingOptions = nil
					m.pendingChoice = 0
					m.input.SetValue("")
					if !ok {
						m.items = append(m.items, transcriptItem{role: "error", text: "待审批命令已失效，请重新发起。"})
						m.syncViewport(true)
						return m, nil
					}
					m.busy = true
					m.status = "bash running"
					m.items = append(m.items, transcriptItem{role: "event", text: "approved bash: " + command})
					m.syncViewport(true)
					runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					m.activeCancel = cancel
					m.chatCanceled = false
					return m, startApprovedBashCommand(runCtx, m.cwd, command, sessionID)
				}
			}
			if m.pendingGitCmsg.Active {
				cmd := m.handleGitCmsgChoice(text)
				return m, cmd
			}
			if m.hasPendingOptions() {
				m.pendingQuestion = ""
				m.pendingOptions = nil
				m.pendingChoice = 0
			}
			if cmd := m.startGitCmsgSlash(text); cmd != nil {
				m.input.SetValue("")
				m.busy = true
				m.status = "commit"
				m.items = append(m.items, transcriptItem{role: "event", text: "commit started"})
				m.syncViewport(true)
				return m, cmd
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
			m.hadChatInput = true
			m.busy = true
			m.status = "running"
			m.streamingIndex = -1
			m.scroll = 0
			m.syncViewport(true)
			chatCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			m.activeCancel = cancel
			m.chatCanceled = false
			return m, tea.Batch(startChat(chatCtx, m.app.Agent, m.chatMsgs, m.sessionID, text), waitForChat(m.chatMsgs), waitForEvent(m.events))
		}
	case chatDeltaMsg:
		if m.chatCanceled {
			return m, waitForChat(m.chatMsgs)
		}
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
		if m.activeCancel != nil {
			m.activeCancel()
			m.activeCancel = nil
		}
		if m.chatCanceled && errors.Is(msg.err, context.Canceled) {
			m.chatCanceled = false
			m.syncViewport(true)
			return m, waitForEvent(m.events)
		}
		m.chatCanceled = false
		m.refreshGitSync()
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
		}
		m.refreshContextUsage()
		m.syncViewport(true)
		return m, waitForEvent(m.events)
	case shellDoneMsg:
		m.busy = false
		m.status = "idle"
		if m.activeCancel != nil {
			m.activeCancel()
			m.activeCancel = nil
		}
		if m.chatCanceled && errors.Is(msg.err, context.Canceled) {
			m.chatCanceled = false
			m.syncViewport(true)
			return m, nil
		}
		m.chatCanceled = false
		if strings.TrimSpace(msg.cwd) != "" {
			m.cwd = msg.cwd
		}
		m.refreshGitSync()
		m.items = append(m.items, msg.transcriptItem())
		m.syncViewport(true)
		return m, nil
	case bashApprovalDoneMsg:
		if m.activeCancel != nil {
			m.activeCancel()
			m.activeCancel = nil
		}
		if m.chatCanceled && errors.Is(msg.err, context.Canceled) {
			m.chatCanceled = false
			m.syncViewport(true)
			return m, nil
		}
		m.chatCanceled = false
		m.busy = true
		m.status = "running"
		m.items = append(m.items, bashApprovalTranscriptItem(msg))
		m.syncViewport(true)
		prompt := formatApprovedBashFollowup(msg.command, msg.output, msg.err)
		chatCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		m.activeCancel = cancel
		m.chatCanceled = false
		return m, tea.Batch(startChat(chatCtx, m.app.Agent, m.chatMsgs, msg.sessionID, prompt), waitForChat(m.chatMsgs), waitForEvent(m.events))
	case gitCmsgPrepareMsg:
		m.busy = false
		m.status = "idle"
		if m.activeCancel != nil {
			m.activeCancel()
			m.activeCancel = nil
		}
		if m.chatCanceled && errors.Is(msg.err, context.Canceled) {
			m.chatCanceled = false
			m.syncViewport(true)
			return m, nil
		}
		m.chatCanceled = false
		m.refreshGitSync()
		if msg.err != nil {
			m.lastError = msg.err.Error()
			m.items = append(m.items, transcriptItem{role: "error", text: msg.err.Error()})
			m.syncViewport(true)
			return m, nil
		}
		m.pendingGitCmsg = gitCmsgPending{Active: true, Args: msg.args, Message: msg.message}
		m.pendingQuestion = formatGitCmsgQuestion(msg)
		m.pendingOptions = []string{"提交并推送", "确认提交", "重新生成", "取消操作"}
		m.pendingChoice = 0
		m.items = append(m.items, transcriptItem{role: "assistant", text: m.pendingQuestion})
		m.syncViewport(true)
		return m, nil
	case gitCmsgCommitMsg:
		m.busy = false
		m.status = "idle"
		if m.activeCancel != nil {
			m.activeCancel()
			m.activeCancel = nil
		}
		if m.chatCanceled && errors.Is(msg.err, context.Canceled) {
			m.chatCanceled = false
			m.syncViewport(true)
			return m, nil
		}
		m.chatCanceled = false
		m.refreshGitSync()
		m.pendingGitCmsg = gitCmsgPending{}
		m.pendingQuestion = ""
		m.pendingOptions = nil
		m.pendingChoice = 0
		if msg.err != nil {
			m.lastError = msg.err.Error()
			m.items = append(m.items, transcriptItem{role: "error", text: msg.err.Error()})
		} else {
			doneText := "提交完成。"
			if msg.push {
				doneText = "提交并推送完成。"
			}
			m.items = append(m.items, transcriptItem{role: "system", text: doneText + "\n" + strings.TrimSpace(msg.output)})
		}
		m.syncViewport(true)
		return m, nil
	case gitSyncDoneMsg:
		m.busy = false
		m.status = "idle"
		m.refreshGitSync()
		result := "pushed"
		if strings.HasPrefix(msg.action, "pull") {
			result = "pulled"
		}
		if msg.err != nil {
			result = "failed"
		}
		m.gitSyncFeedback = gitSyncFeedback{Result: result}
		text := "$ git " + msg.action
		if strings.TrimSpace(msg.output) != "" {
			text += "\n" + strings.TrimRight(msg.output, "\n")
		}
		if msg.err != nil {
			m.items = append(m.items, transcriptItem{role: "error", text: text + "\n" + msg.err.Error()})
		} else {
			m.items = append(m.items, transcriptItem{role: "shell", text: text})
		}
		m.syncViewport(true)
		return m, nil
	case gitSyncTickMsg:
		if !m.gitSyncFeedback.Loading {
			return m, nil
		}
		m.gitSyncFeedback.Frame++
		return m, tickGitSyncSpinner()
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
	if m.explorer != nil {
		m.explorer.resize(m.width, m.height)
		return m.explorer.View()
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
	if suggestions := m.shellSuggestions(8); len(suggestions) > 0 {
		promptParts = append(promptParts, renderShellSuggestions(suggestions, promptW-2))
	} else if suggestions := m.slashSuggestions(8); len(suggestions) > 0 {
		promptParts = append(promptParts, renderSlashSuggestions(suggestions, promptW-2))
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
	m.viewport.SetContent(m.renderTranscriptContent(mainW))
	if gotoBottom || atBottom {
		m.viewport.GotoBottom()
	}
}

func (m model) renderTranscriptContent(width int) string {
	content := renderTranscriptContent(m.items, width)
	if m.hasPendingOptions() || strings.TrimSpace(m.pendingQuestion) != "" {
		panel := renderPendingAskPanel(m.pendingQuestion, m.pendingOptions, m.pendingChoice, width)
		if strings.TrimSpace(content) == "" {
			return panel
		}
		return content + "\n\n" + panel
	}
	return content
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
		case "shell":
			plain = wrapText(item.text, textWidth)
			style = shellStyle
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
		case "shell":
			lines = append(lines, shellStyle.Render(wrapText(item.text, textWidth)))
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
	footer := sidebarFooterLines(m, bodyWidth)
	top := sidebarTopLines(m, bodyWidth)
	middle := sidebarMiddleLines(m, bodyWidth, max(0, height-len(top)-len(footer)))
	lines := composeSidebar(top, middle, footer, height)
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
	if ctxUsage := m.contextUsage; ctxUsage != nil && ctxUsage.ContextLength > 0 {
		lines = append(lines, "", "context")
		lines = append(lines, strings.Split(renderContextUsage(ctxUsage, width), "\n")...)
	}
	if m.logPath != "" {
		lines = append(lines, "log: "+truncateDisplay(filepath.Base(m.logPath), width))
	}
	return lines
}

func sidebarFooterLines(m model, width int) []string {
	lines := []string{"workspace"}
	cwd := cwdDisplayName(m.cwd)
	if cwd == "" {
		cwd = "-"
	}
	lines = append(lines, truncateDisplay(cwd, width))
	git := strings.TrimSpace(m.gitStatus)
	if git == "" {
		git = "-"
	}
	m.gitStatus = git
	lines = append(lines, truncateDisplay(gitSyncButtonLabel(m), width))
	lines = append(lines, "⌃S sync")
	lines = append(lines, "⌃K diff")
	mouseState := "off"
	if m.mouseEnabled {
		mouseState = "on"
	}
	lines = append(lines, "⌃O mouse "+mouseState)
	lines = append(lines, "⌃Y copy")
	lines = append(lines, "⌃C quit")
	return lines
}

func gitSyncButtonLabel(m model) string {
	git := strings.TrimSpace(m.gitStatus)
	if git == "" {
		git = "-"
	}
	git = styledGitSyncStatus(m, git)
	if m.gitSyncFeedback.Loading {
		return git + " " + gitLoadingStyle.Render("[syncing "+gitSyncSpinnerFrame(m.gitSyncFeedback.Frame)+"]")
	}
	if strings.TrimSpace(m.gitSyncFeedback.Result) != "" {
		style := gitResultStyle
		if strings.EqualFold(m.gitSyncFeedback.Result, "failed") {
			style = gitFailedStyle
		}
		return git + " " + style.Render("["+m.gitSyncFeedback.Result+"]")
	}
	if m.gitSync.Actionable() {
		if action, _ := gitSyncAction(m.gitSync); action != "" {
			return git + " " + gitActionStyle.Render("["+action+"]")
		}
	}
	return git
}

func styledGitSyncStatus(m model, fallback string) string {
	if !m.gitSync.Valid {
		return fallback
	}
	branch := strings.TrimSpace(m.gitSync.Branch)
	if branch == "" {
		branch = "no git"
	}
	var parts []string
	if m.gitSync.Ahead > 0 {
		parts = append(parts, gitPushStyle.Render(fmt.Sprintf("↑%d", m.gitSync.Ahead)))
	}
	if m.gitSync.Behind > 0 {
		parts = append(parts, gitPullStyle.Render(fmt.Sprintf("↓%d", m.gitSync.Behind)))
	}
	if len(parts) == 0 {
		parts = append(parts, gitOKStyle.Render("✓"))
	}
	if m.gitSync.Dirty > 0 {
		parts = append(parts, " "+gitDirtyStyle.Render(fmt.Sprintf("*%d", m.gitSync.Dirty)))
	}
	return branch + " " + strings.Join(parts, "")
}

func gitSyncSpinnerFrame(frame int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	if frame < 0 {
		frame = 0
	}
	return frames[frame%len(frames)]
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
	out = appendLocalSuggestions(value, out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func appendLocalSuggestions(value string, suggestions []slashSuggestion) []slashSuggestion {
	prefix := strings.TrimPrefix(strings.TrimSpace(value), "/")
	local := []slashSuggestion{
		{Name: "tree", Description: "打开项目目录树", Kind: "tui"},
		{Name: "diff", Description: "查看当前 Git 变更", Kind: "tui"},
		{Name: "commit", Description: "生成并提交当前变更", Kind: "tui"},
	}
	seen := map[string]bool{}
	for _, item := range suggestions {
		seen[item.Name] = true
	}
	for _, item := range local {
		if seen[item.Name] || !strings.HasPrefix(item.Name, prefix) {
			continue
		}
		suggestions = append(suggestions, item)
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		if suggestions[i].Kind == suggestions[j].Kind {
			return suggestions[i].Name < suggestions[j].Name
		}
		return suggestions[i].Kind == "tui"
	})
	return suggestions
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

func renderPendingAskPanel(question string, options []string, selected int, width int) string {
	question = strings.TrimSpace(question)
	if question == "" && len(options) == 0 {
		return ""
	}
	if selected < 0 || selected >= len(options) {
		selected = 0
	}
	textWidth := max(20, width-4)
	var lines []string
	if question != "" {
		for _, line := range strings.Split(wrapText(question, textWidth), "\n") {
			lines = append(lines, titleStyle.Render(fitDisplay(line, width)))
		}
		lines = append(lines, "")
	}
	for i, opt := range options {
		title, desc := splitPendingOption(opt)
		prefix := "  "
		if i == selected {
			prefix = "› "
		}
		line := fmt.Sprintf("%s%d. %s", prefix, i+1, title)
		style := hintStyle
		if i == selected {
			style = userStyle
		}
		lines = append(lines, style.Render(fitDisplay(line, width)))
		if desc != "" {
			descPrefix := strings.Repeat(" ", lipgloss.Width(prefix)+3)
			for _, descLine := range strings.Split(wrapText(desc, max(8, width-lipgloss.Width(descPrefix))), "\n") {
				lines = append(lines, hintStyle.Render(fitDisplay(descPrefix+descLine, width)))
			}
		}
	}
	if len(options) == 0 && len(lines) > 0 && strings.TrimSpace(question) != "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func splitPendingOption(option string) (string, string) {
	option = strings.TrimSpace(option)
	for _, sep := range []string{"::", " - ", "："} {
		if idx := strings.Index(option, sep); idx > 0 {
			title := strings.TrimSpace(option[:idx])
			desc := strings.TrimSpace(option[idx+len(sep):])
			if title != "" && desc != "" {
				return title, desc
			}
		}
	}
	return option, ""
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
		return pendingOptionValue(m.pendingOptions[n-1]), true
	}
	for _, opt := range m.pendingOptions {
		title := pendingOptionValue(opt)
		if strings.EqualFold(text, opt) || strings.EqualFold(text, title) {
			return title, true
		}
	}
	return "", false
}

func pendingOptionValue(option string) string {
	title, _ := splitPendingOption(option)
	return title
}

func (m *model) recordInputHistory(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	m.inputHistory = append(m.inputHistory, text)
	m.historyIndex = 0
	m.historyDraft = ""
}

func (m *model) clearInputDraft() {
	m.input.SetValue("")
	m.historyIndex = 0
	m.historyDraft = ""
	m.shellHistIndex = 0
	m.shellHistDraft = ""
}

func (m *model) navigateInputHistory(direction int) bool {
	if isShellInput(m.input.Value()) {
		return m.navigateShellHistory(direction)
	}
	if len(m.inputHistory) == 0 || direction == 0 || m.busy || m.hasPendingOptions() {
		return false
	}
	if m.historyIndex == 0 {
		m.historyDraft = m.input.Value()
	}
	next := m.historyIndex
	if direction < 0 {
		next++
	} else {
		next--
	}
	if next < 0 {
		return false
	}
	if next == 0 {
		m.historyIndex = 0
		m.input.SetValue(m.historyDraft)
		m.input.CursorEnd()
		return true
	}
	if next > len(m.inputHistory) {
		return false
	}
	m.historyIndex = next
	m.input.SetValue(m.inputHistory[len(m.inputHistory)-next])
	m.input.CursorEnd()
	return true
}

func (m model) openExplorerCommand(text string) *tuiExplorer {
	cmd := strings.TrimSpace(text)
	switch cmd {
	case "/tree":
		return newTreeExplorer(m.cwd, m.width, m.height)
	case "/diff":
		return newDiffExplorer(m.cwd, m.width, m.height)
	default:
		return nil
	}
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

func startChat(ctx context.Context, agent *agentruntime.Agent, out chan<- tea.Msg, sessionID, text string) tea.Cmd {
	return func() tea.Msg {
		go func() {
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

func cwdDisplayName(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	base := filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) {
		return clean
	}
	return base
}

func gitSyncSummary(cwd string) string {
	return gitSyncStateForCWD(cwd).Format()
}

func gitSyncStateForCWD(cwd string) gitSyncState {
	out, err := runGit(cwd, "status", "--porcelain=v1", "--branch")
	if err != nil {
		return gitSyncState{Branch: "no git"}
	}
	return parseGitSyncState(out)
}

func parseGitSyncSummary(out string) string {
	return parseGitSyncState(out).Format()
}

func parseGitSyncState(out string) gitSyncState {
	lines := strings.Split(out, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "## ") {
		return gitSyncState{Branch: "no upstream"}
	}
	head := strings.TrimSpace(strings.TrimPrefix(lines[0], "## "))
	branch := head
	if idx := strings.Index(branch, "..."); idx >= 0 {
		branch = branch[:idx]
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "detached"
	}
	ahead, behind := parseAheadBehind(head)
	dirty := 0
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" {
			dirty++
		}
	}
	return gitSyncState{Branch: branch, Ahead: ahead, Behind: behind, Dirty: dirty, Valid: true}
}

func (s gitSyncState) Format() string {
	branch := strings.TrimSpace(s.Branch)
	if branch == "" {
		branch = "no git"
	}
	if !s.Valid {
		return branch
	}
	var sync string
	switch {
	case s.Ahead > 0 && s.Behind > 0:
		sync = fmt.Sprintf("↑%d↓%d", s.Ahead, s.Behind)
	case s.Ahead > 0:
		sync = fmt.Sprintf("↑%d", s.Ahead)
	case s.Behind > 0:
		sync = fmt.Sprintf("↓%d", s.Behind)
	default:
		sync = "✓"
	}
	if s.Dirty > 0 {
		sync += fmt.Sprintf(" *%d", s.Dirty)
	}
	return branch + " " + sync
}

func parseAheadBehind(head string) (ahead, behind int) {
	if start := strings.Index(head, "["); start >= 0 {
		if end := strings.Index(head[start:], "]"); end >= 0 {
			body := head[start+1 : start+end]
			for _, part := range strings.Split(body, ",") {
				part = strings.TrimSpace(part)
				switch {
				case strings.HasPrefix(part, "ahead "):
					n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(part, "ahead ")))
					ahead = n
				case strings.HasPrefix(part, "behind "):
					n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(part, "behind ")))
					behind = n
				}
			}
		}
	}
	return ahead, behind
}

func (m *model) refreshGitSync() {
	m.gitSync = gitSyncStateForCWD(m.cwd)
	m.gitStatus = m.gitSync.Format()
}

func gitSyncAction(state gitSyncState) (string, []string) {
	switch {
	case state.Ahead > 0 && state.Behind > 0:
		return "pull", []string{"pull", "--rebase"}
	case state.Behind > 0:
		return "pull", []string{"pull", "--ff-only"}
	case state.Ahead > 0:
		return "push", []string{"push"}
	default:
		return "", nil
	}
}

func startGitSync(cwd string, state gitSyncState) tea.Cmd {
	return func() tea.Msg {
		action, args := gitSyncAction(state)
		if action == "" || len(args) == 0 {
			return gitSyncDoneMsg{action: "sync", err: fmt.Errorf("no git sync action available")}
		}
		out, err := runGit(cwd, args...)
		return gitSyncDoneMsg{action: strings.Join(args, " "), output: out, err: err}
	}
}

func (m *model) handleGitSyncClick(msg tea.MouseMsg) bool {
	if msg.Type != tea.MouseLeft || msg.Action != tea.MouseActionPress || m.busy || !m.gitSync.Actionable() {
		return false
	}
	mainW, sideW, bodyH, _ := layoutSizes(m.width, m.height)
	if sideW <= 0 {
		return false
	}
	sideStart := mainW + boxStyle.GetHorizontalFrameSize() + 1
	if msg.X < sideStart || msg.Y < max(0, bodyH-3) || msg.Y > bodyH+1 {
		return false
	}
	return m.startGitSyncFeedback()
}

func (m *model) startGitSyncFeedback() bool {
	if m == nil || m.busy || !m.gitSync.Actionable() {
		return false
	}
	action, _ := gitSyncAction(m.gitSync)
	if action == "" {
		return false
	}
	m.busy = true
	m.gitSyncFeedback = gitSyncFeedback{Loading: true, Action: action}
	m.syncViewport(true)
	return true
}

func tickGitSyncSpinner() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return gitSyncTickMsg{}
	})
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
