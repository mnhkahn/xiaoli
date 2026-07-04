package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"

	agentchannel "github.com/mnhkahn/xiaoli/internal/agent/channel"
	"github.com/mnhkahn/xiaoli/internal/agent/localapp"
	"github.com/mnhkahn/xiaoli/internal/agent/localconfig"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	"github.com/mnhkahn/xiaoli/internal/agent/slash"
	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
	agentevent "github.com/mnhkahn/xiaoli/internal/event"
)

const (
	channelName = "tui"
	channelUser = "local"
)

var version = "dev"

const (
	latestReleaseURL    = "https://api.github.com/repos/mnhkahn/xiaoli/releases/latest"
	updateCacheFileName = "version.json"
)

type transcriptItem struct {
	role  string
	text  string
	frame int
}

type slashSuggestion = slash.Suggestion

type chatDoneMsg struct {
	reply string
	err   error
}

const defaultChatTimeout = 10 * time.Minute

type chatDeltaMsg struct {
	delta string
}

type eventMsg struct {
	event agentevent.Event
}

type runPulseTickMsg struct{}

type gitSyncTickMsg struct{}

type gitSyncDoneMsg struct {
	action string
	output string
	err    error
}

type selectionCopyDoneMsg struct {
	text string
	err  error
}

type selectionPoint struct {
	x int
	y int
}

type transcriptSelection struct {
	active   bool
	dragging bool
	anchor   selectionPoint
	focus    selectionPoint
	text     string
}

type updateCheckDoneMsg struct {
	info updateInfo
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

type updateInfo struct {
	Current    string    `json:"current"`
	Latest     string    `json:"latest"`
	Command    string    `json:"command"`
	ReleaseURL string    `json:"release_url,omitempty"`
	Notes      []string  `json:"notes,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

func (i updateInfo) Available() bool {
	return !isDevVersion(i.Current) && i.Latest != "" && compareVersions(i.Current, i.Latest) < 0
}

type model struct {
	app                *localapp.App
	events             chan agentevent.Event
	chatMsgs           chan tea.Msg
	activeCancel       context.CancelFunc
	chatCanceled       bool
	input              textinput.Model
	inputHistory       []string
	historyIndex       int
	historyDraft       string
	shellHistory       []string
	shellHistIndex     int
	shellHistDraft     string
	items              []transcriptItem
	sessionID          string
	cwd                string
	workspaceStatePath string
	workspacePicker    *workspacePicker
	lastTabAt          time.Time
	gitStatus          string
	gitSync            gitSyncState
	gitSyncFeedback    gitSyncFeedback
	logPath            string
	updateInfo         updateInfo
	contextUsage       *agentruntime.ContextUsage
	runPulseFrame      int
	runPulseActive     bool
	scroll             int
	viewport           viewport.Model
	streamingIndex     int
	hadChatInput       bool
	width              int
	height             int
	busy               bool
	status             string
	lastError          string
	pendingBashHash    string
	pendingQuestion    string
	pendingOptions     []string
	pendingChoice      int
	pendingGitCmsg     gitCmsgPending
	explorer           *tuiExplorer
	mouseEnabled       bool
	selection          transcriptSelection
	quitting           bool
}

var (
	titleStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	userStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	agentStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("114"))
	shellStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	eventStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	hintStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	boxStyle           = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	sideStyle          = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
	gitOKStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	gitPushStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true)
	gitPullStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	gitDirtyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	gitActionStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true)
	gitLoadingStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	gitResultStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	gitFailedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	ansiEscapeRE       = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	mouseSGRFragmentRE = regexp.MustCompile(`(?:\x1b)?\[<\d+;\d+;\d+[Mm]`)
)

const (
	selectionStartSeq = "\x1b[7m"
	selectionEndSeq   = "\x1b[27m"
)

func main() {
	configPath := flag.String("config", "", "path to local xiaoli settings.json")
	initConfig := flag.Bool("init", false, "create default local settings and secrets files")
	prompt := flag.String("prompt", "", "extra system prompt appended after AGENT.md/SOUL.md")
	resumeSession := flag.String("s", "", "session id to resume")
	showVersion := flag.Bool("version", false, "print TUI version and exit")
	renderSession := flag.String("render-session", "", "render a session frame and exit")
	renderWidth := flag.Int("width", 160, "render-session terminal width")
	renderHeight := flag.Int("height", 40, "render-session terminal height")
	flag.Parse()

	if *showVersion {
		fmt.Println(versionInfo())
		return
	}

	if *initConfig {
		cfg, err := localconfig.EnsureDefaults(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "xiaoli init: %v\n", err)
			os.Exit(1)
		}
		if localconfig.NeedsModelWizard(cfg) {
			fmt.Println("No local model configured yet. Let's set one up.")
			cfg, err = localconfig.RunModelWizard(*configPath, os.Stdin, os.Stdout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "xiaoli init model: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "xiaoli: %v\n\n", err)
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

	p := tea.NewProgram(newModel(app, *resumeSession, logPath), tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "xiaoli: %v\n", err)
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

	sessionID := strings.TrimSpace(resumeSessionID)
	workspacePath := ""
	if app != nil && strings.TrimSpace(app.Config.DataDir) != "" {
		workspacePath = workspaceStatePath(app.Config.DataDir)
	}
	if sessionID == "" && workspacePath != "" {
		if item, ok := findWorkspace(workspacePath, cwd); ok && item.SessionID != "" {
			sessionID = item.SessionID
		}
	}
	items := []transcriptItem{{role: "banner"}}
	if sessionID != "" {
		items = restoreTranscript(app.Agent, sessionID)
		items = append(items, transcriptItem{role: "system", text: "Resumed session " + sessionID})
	}
	gitSync := gitSyncStateForCWD(cwd)
	if workspacePath != "" {
		_ = upsertWorkspace(workspacePath, workspaceItem{
			CWD:        cwd,
			SessionID:  sessionID,
			Title:      workspaceTitle(app, sessionID, cwd),
			Model:      currentModelName(app),
			LastOpened: time.Now(),
		})
	}

	return model{
		app:                app,
		events:             events,
		chatMsgs:           chatMsgs,
		input:              input,
		sessionID:          sessionID,
		cwd:                cwd,
		workspaceStatePath: workspacePath,
		gitStatus:          gitSync.Format(),
		gitSync:            gitSync,
		logPath:            logPath,
		viewport:           vp,
		status:             "idle",
		streamingIndex:     -1,
		items:              items,
		mouseEnabled:       true,
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, waitForEvent(m.events)}
	if m.app != nil && strings.TrimSpace(m.app.Config.DataDir) != "" {
		cmds = append(cmds, checkForUpdateCmd(m.app.Config.DataDir, buildVersion()))
	}
	return tea.Batch(cmds...)
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
		fmt.Println("  Continue  xiaoli")
	}
}

func exitLogo() string {
	return renderFigletLogo()
}

func renderFigletLogo() string {
	lines := figletLogoLines()
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

func figletLogoLines() []string {
	return []string{
		"  ___ _   _  ___  __ _ _ __ ___      ___ ___  _ __ ___",
		" / __| | | |/ _ \\/ _` | '_ ` _ \\    / __/ _ \\| '_ ` _ \\",
		"| (__| |_| |  __/ (_| | | | | | |  | (_| (_) | | | | | |",
		" \\___|\\__, |\\___|\\__,_|_| |_| |_| (_)___\\___/|_| |_| |_|",
		"      |___/",
	}
}

func continueCommand(sessionID string, configPath string) string {
	parts := []string{"xiaoli"}
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
		mainW, _, _, _, _ := layoutSizes(m.width, m.height)
		m.input.Width = max(20, mainW-2)
		m.refreshContextUsage()
		m.syncViewport(false)
		return m, nil
	case tea.MouseMsg:
		if !m.mouseEnabled {
			return m, nil
		}
		if m.explorer != nil {
			m.explorer.handleMouse(msg)
			return m, nil
		}
		if handled, cmd := m.handleTranscriptSelectionMouse(msg); handled {
			return m, cmd
		}
		if m.handleGitSyncClick(msg) {
			return m, tea.Batch(startGitSync(m.cwd, m.gitSync), tickGitSyncSpinner())
		}
		if !isMouseWheel(msg) || !m.mouseInMainViewport(msg) {
			return m, nil
		}
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		return m, vpCmd
	case tea.KeyMsg:
		if m.workspacePicker != nil {
			item, selected, closePicker := m.workspacePicker.handleKey(msg)
			if selected {
				m.switchWorkspace(item)
			}
			if closePicker {
				m.workspacePicker = nil
			}
			return m, nil
		}
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
			if m.selection.active || m.selection.dragging {
				m.selection = transcriptSelection{}
				m.status = "idle"
				return m, nil
			}
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
			return m, nil
		case "ctrl+k":
			if m.busy || m.hasPendingOptions() {
				return m, nil
			}
			m.clearInputDraft()
			m.explorer = newDiffExplorer(m.cwd, m.width, m.height)
			return m, nil
		case "ctrl+t":
			if m.busy || m.hasPendingOptions() {
				return m, nil
			}
			m.clearInputDraft()
			m.explorer = newTreeExplorer(m.cwd, m.width, m.height)
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
			if m.movePendingChoice(-1) {
				return m, nil
			}
			if m.navigateInputHistory(-1) {
				return m, nil
			}
		case "down":
			if m.movePendingChoice(1) {
				return m, nil
			}
			if m.navigateInputHistory(1) {
				return m, nil
			}
		case "left", "shift+tab":
			if m.movePendingChoice(-1) {
				return m, nil
			}
		case "right":
			if m.movePendingChoice(1) {
				return m, nil
			}
		case "tab":
			if m.movePendingChoice(1) {
				return m, nil
			}
			if m.consumeDoubleTab() {
				m.openWorkspacePicker()
				return m, nil
			}
			m.markTab()
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
			if m.handleLocalCommand(text) {
				m.input.SetValue("")
				m.status = "idle"
				m.refreshContextUsage()
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
				if isApprove(text) || isBashApprovalChoice(text) {
					sessionID := m.activeSessionID()
					command, ok := agentbuiltin.PendingBashCommand(sessionID, m.pendingBashHash)
					if ok {
						if err := agentbuiltin.StoreBashApprovalChoice(sessionID, m.pendingBashHash, normalizeBashApprovalChoice(text)); err != nil {
							agentbuiltin.ClearBashApproval(sessionID)
							m.pendingBashHash = ""
							m.pendingQuestion = ""
							m.pendingOptions = nil
							m.pendingChoice = 0
							m.input.SetValue("")
							m.items = append(m.items, transcriptItem{role: "error", text: "保存 Bash 权限失败：" + err.Error()})
							m.syncViewport(true)
							return m, nil
						}
					}
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
				m.appendRunActiveEvent("Preparing commit")
				m.syncViewport(true)
				return m, tea.Batch(cmd, tickRunPulse())
			}
			if cmd := m.handleSlash(text); cmd.handled {
				m.input.SetValue("")
				if cmd.sessionID != "" && cmd.sessionID != m.sessionID {
					m.sessionID = cmd.sessionID
					m.items = restoreTranscript(m.app.Agent, m.sessionID)
					m.items = append(m.items, transcriptItem{role: "system", text: cmd.reply})
					m.recordCurrentWorkspace()
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
			if m.sessionID == "" {
				m.sessionID = m.createProjectSession()
			}
			m.recordCurrentWorkspace()
			m.busy = true
			m.status = "running"
			m.streamingIndex = -1
			m.scroll = 0
			m.syncViewport(true)
			chatCtx, cancel := context.WithTimeout(context.Background(), defaultChatTimeout)
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
		m.recordCurrentWorkspace()
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
		m.recordCurrentWorkspace()
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
		chatCtx, cancel := context.WithTimeout(context.Background(), defaultChatTimeout)
		m.activeCancel = cancel
		m.chatCanceled = false
		return m, tea.Batch(startChat(chatCtx, m.app.Agent, m.chatMsgs, msg.sessionID, prompt), waitForChat(m.chatMsgs), waitForEvent(m.events))
	case gitCmsgPrepareMsg:
		m.busy = false
		m.status = "idle"
		m.runPulseActive = false
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
			m.items = append(m.items, transcriptItem{role: "run-failed", text: "Commit blocked", frame: m.runPulseFrame})
			m.items = append(m.items, transcriptItem{role: "error", text: msg.err.Error()})
			m.syncViewport(true)
			return m, nil
		}
		m.pendingGitCmsg = gitCmsgPending{Active: true, Args: msg.args, Message: msg.message}
		m.pendingQuestion = formatGitCmsgQuestion(msg)
		m.pendingOptions = []string{"提交并推送", "确认提交", "重新生成", "取消操作"}
		m.pendingChoice = 0
		m.items = append(m.items, transcriptItem{role: "run-done", text: "Commit plan ready", frame: m.runPulseFrame})
		m.items = append(m.items, transcriptItem{role: "assistant", text: m.pendingQuestion})
		m.syncViewport(true)
		return m, nil
	case gitCmsgCommitMsg:
		m.busy = false
		m.status = "idle"
		m.runPulseActive = false
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
			m.items = append(m.items, transcriptItem{role: "run-failed", text: "Commit blocked", frame: m.runPulseFrame})
			m.items = append(m.items, transcriptItem{role: "error", text: formatGitCmsgCommitError(msg.err, msg.output)})
		} else {
			doneText := "提交完成。"
			if msg.push {
				doneText = "提交并推送完成。"
			}
			m.items = append(m.items, transcriptItem{role: "run-done", text: "Commit delivered", frame: m.runPulseFrame})
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
	case selectionCopyDoneMsg:
		if msg.err != nil {
			m.status = "copy failed"
			m.lastError = msg.err.Error()
			return m, nil
		}
		if strings.TrimSpace(msg.text) != "" {
			m.status = fmt.Sprintf("copied %d chars", len([]rune(msg.text)))
		}
		return m, nil
	case updateCheckDoneMsg:
		m.updateInfo = msg.info
		m.syncViewport(false)
		return m, nil
	case runPulseTickMsg:
		if !m.runPulseActive {
			return m, nil
		}
		m.runPulseFrame++
		m.syncViewport(false)
		return m, tickRunPulse()
	case eventMsg:
		item := eventTranscriptItem(msg.event)
		item.frame = m.runPulseFrame
		switch item.role {
		case "run-active":
			m.runPulseActive = true
			m.runPulseFrame = 0
			item.frame = 0
			m.items = append(m.items, item)
			m.refreshContextUsage()
			m.syncViewport(true)
			return m, tea.Batch(waitForEvent(m.events), tickRunPulse())
		case "run-done", "run-failed":
			m.runPulseActive = false
		}
		m.items = append(m.items, item)
		m.refreshContextUsage()
		m.syncViewport(true)
		return m, waitForEvent(m.events)
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if cleaned := stripMouseSGRFragments(m.input.Value()); cleaned != m.input.Value() {
		m.input.SetValue(cleaned)
	}
	return m, tea.Batch(vpCmd, cmd)
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "Loading..."
	}
	if m.workspacePicker != nil {
		m.workspacePicker.resize(m.width, m.height)
		return m.workspacePicker.View()
	}
	if m.explorer != nil {
		m.explorer.resize(m.width, m.height)
		return m.explorer.View()
	}
	mainW, _, bodyH, promptW, statusH := layoutSizes(m.width, m.height)

	m.syncViewport(false)
	transcript := m.viewport.View()
	if m.selection.active || m.selection.dragging {
		transcript = renderTranscriptSelectionOverlay(strings.Split(transcript, "\n"), m.selection, mainW)
	}
	top := boxStyle.Width(mainW).Height(bodyH).Render(transcript)
	status := renderStatusBar(m, max(20, promptW+boxStyle.GetHorizontalFrameSize()))

	promptParts := []string{}
	if suggestions := m.shellSuggestions(8); len(suggestions) > 0 {
		promptParts = append(promptParts, renderShellSuggestions(suggestions, promptW-2))
	} else if suggestions := m.slashSuggestions(8); len(suggestions) > 0 {
		promptParts = append(promptParts, renderSlashSuggestions(suggestions, promptW-2))
	}
	promptParts = append(promptParts, m.input.View())
	prompt := boxStyle.Width(promptW).Render(strings.Join(promptParts, "\n"))
	parts := []string{top, prompt}
	if statusH > 0 {
		parts = append(parts, status)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func layoutSizes(width, height int) (mainW, sideW, bodyH, promptW, statusH int) {
	sideW = 0
	statusH = 2
	promptW = max(20, width-boxStyle.GetHorizontalFrameSize())
	mainW = promptW
	bodyH = max(8, height-5-statusH)
	return mainW, sideW, bodyH, promptW, statusH
}

func layoutMainWidth(width, height int) int {
	mainW, _, _, _, _ := layoutSizes(width, height)
	return mainW
}

func (m *model) syncViewport(gotoBottom bool) {
	if m == nil || m.width <= 0 || m.height <= 0 {
		return
	}
	mainW, _, bodyH, _, _ := layoutSizes(m.width, m.height)
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
	content := m.renderTranscriptItems(width)
	if m.hasPendingOptions() || strings.TrimSpace(m.pendingQuestion) != "" {
		panel := renderPendingAskPanel(m.pendingQuestion, m.pendingOptions, m.pendingChoice, width)
		if strings.TrimSpace(content) == "" {
			return panel
		}
		return content + "\n\n" + panel
	}
	return content
}

func (m model) renderTranscriptItems(width int) string {
	if len(m.items) == 1 && m.items[0].role == "banner" {
		return renderWelcomeBanner(m, width)
	}
	return renderTranscriptContentWithFrame(m.items, width, m.runPulseFrame)
}

func renderTranscriptContent(items []transcriptItem, width int) string {
	return renderTranscriptContentWithFrame(items, width, 0)
}

func renderTranscriptContentWithFrame(items []transcriptItem, width int, frame int) string {
	lines := make([]string, 0, len(items)*2)
	textWidth := max(20, width-2)
	latestActive := latestActiveEventIndex(items)
	for i, item := range items {
		var plain string
		style := eventStyle
		custom := false
		itemFrame := item.frame
		if i == latestActive {
			itemFrame = frame
		}
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
		case "run-active":
			label := item.text
			if strings.TrimSpace(label) == "" {
				label = activeRunStatus(itemFrame)
			}
			plain = renderRunEventLine(label, width, itemFrame, runEventActive)
			custom = true
		case "run-done":
			label := item.text
			if strings.TrimSpace(label) == "" {
				label = "Delivered"
			}
			plain = renderRunEventLine(label, width, itemFrame, runEventDone)
			custom = true
		case "run-failed":
			label := item.text
			if strings.TrimSpace(label) == "" {
				label = "Blocked"
			}
			plain = renderRunEventLine(label, width, itemFrame, runEventFailed)
			custom = true
		case "tool-active":
			plain = renderRunEventLine(item.text, width, itemFrame, runEventToolActive)
			custom = true
		case "tool-done":
			plain = renderRunEventLine(item.text, width, itemFrame, runEventToolDone)
			custom = true
		case "tool-failed":
			plain = renderRunEventLine(item.text, width, itemFrame, runEventToolFailed)
			custom = true
		case "error":
			plain = wrapWithPrefix("error: ", item.text, textWidth)
			style = errStyle
		default:
			plain = wrapText(item.text, textWidth)
			style = eventStyle
		}
		for _, line := range strings.Split(plain, "\n") {
			if custom {
				lines = append(lines, fitDisplay(line, width))
			} else {
				lines = append(lines, style.Render(fitDisplay(line, width)))
			}
		}
		lines = append(lines, "")
	}
	if len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func latestActiveEventIndex(items []transcriptItem) int {
	for i := len(items) - 1; i >= 0; i-- {
		switch items[i].role {
		case "run-active", "tool-active":
			return i
		}
	}
	return -1
}

type runEventState int

const (
	runEventActive runEventState = iota
	runEventDone
	runEventFailed
	runEventToolActive
	runEventToolDone
	runEventToolFailed
)

var runLoadingFrames = []string{"[li  ]", "[l i ]", "[l  i]", "[l i ]"}

var runStatusPhrases = []string{
	"Aligning",
	"Syncing context",
	"Scoping",
	"Mapping",
	"Structuring",
	"Reasoning",
	"Synthesizing",
	"Prioritizing",
	"Calibrating",
	"Reconciling",
	"Consolidating",
	"Distilling",
	"Driving alignment",
	"Closing the loop",
	"Moving the needle",
	"Sharpening scope",
	"Managing context",
	"Unblocking path",
	"De-risking delivery",
	"Pressure testing",
	"Deep diving",
	"Landing decisions",
	"Operationalizing",
	"RCA tracing",
	"SLA checking",
	"PRD parsing",
	"RFC shaping",
	"WIP reducing",
}

func activeRunStatus(frame int) string {
	if len(runStatusPhrases) == 0 {
		return "Working"
	}
	return runStatusPhrases[positiveMod(frame/32, len(runStatusPhrases))]
}

func renderRunEventLine(label string, width int, frame int, state runEventState) string {
	prefix := "[li  ]"
	colors := []lipgloss.Color{
		lipgloss.Color("#d77757"),
		lipgloss.Color("#eb9f7f"),
		lipgloss.Color("#ffc107"),
		lipgloss.Color("#eb9f7f"),
	}
	switch state {
	case runEventActive:
		if len(runLoadingFrames) > 0 {
			prefix = runLoadingFrames[positiveMod(frame/4, len(runLoadingFrames))]
		}
	case runEventDone:
		prefix = "[ ok ]"
		colors = []lipgloss.Color{
			lipgloss.Color("#4eba65"),
			lipgloss.Color("#7fdc94"),
			lipgloss.Color("#4eba65"),
		}
	case runEventFailed:
		prefix = "[ x  ]"
		colors = []lipgloss.Color{
			lipgloss.Color("#ff6b80"),
			lipgloss.Color("#ff9aa8"),
			lipgloss.Color("#d77757"),
		}
	case runEventToolActive:
		prefix = "[run ]"
		colors = []lipgloss.Color{
			lipgloss.Color("#b1b9f9"),
			lipgloss.Color("#cfd7ff"),
			lipgloss.Color("#93a5ff"),
			lipgloss.Color("#cfd7ff"),
		}
	case runEventToolDone:
		prefix = "[ ok ]"
		colors = []lipgloss.Color{
			lipgloss.Color("#4eba65"),
			lipgloss.Color("#7fdc94"),
			lipgloss.Color("#b1b9f9"),
		}
	case runEventToolFailed:
		prefix = "[ x  ]"
		colors = []lipgloss.Color{
			lipgloss.Color("#ff6b80"),
			lipgloss.Color("#ff9aa8"),
			lipgloss.Color("#ffc107"),
		}
	default:
		prefix = runLoadingFrames[positiveMod(frame/4, len(runLoadingFrames))]
	}
	return shimmerText(fitDisplay(prefix+" "+label, width), colors, frame)
}

func positiveMod(value, base int) int {
	if base <= 0 {
		return 0
	}
	value %= base
	if value < 0 {
		value += base
	}
	return value
}

func shimmerText(text string, colors []lipgloss.Color, offset int) string {
	if len(colors) == 0 {
		return text
	}
	var b strings.Builder
	runes := []rune(text)
	base := colors[0]
	shimmer := colors[len(colors)-1]
	if len(colors) > 1 {
		shimmer = colors[1]
	}
	lead := positiveMod(offset/2, len(runes)+4)
	for i, r := range runes {
		if r == ' ' {
			b.WriteRune(r)
			continue
		}
		distance := absInt(i - lead)
		color := base
		bold := false
		if distance <= 1 {
			color = shimmer
			bold = true
		}
		style := lipgloss.NewStyle().Foreground(color)
		if bold {
			style = style.Bold(true)
		}
		b.WriteString(style.Render(string(r)))
	}
	return b.String()
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
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
		case "run-active":
			label := item.text
			if strings.TrimSpace(label) == "" {
				label = activeRunStatus(item.frame)
			}
			lines = append(lines, renderRunEventLine(label, width, item.frame, runEventActive))
		case "run-done":
			label := item.text
			if strings.TrimSpace(label) == "" {
				label = "Delivered"
			}
			lines = append(lines, renderRunEventLine(label, width, item.frame, runEventDone))
		case "run-failed":
			label := item.text
			if strings.TrimSpace(label) == "" {
				label = "Blocked"
			}
			lines = append(lines, renderRunEventLine(label, width, item.frame, runEventFailed))
		case "tool-active":
			lines = append(lines, renderRunEventLine(item.text, width, item.frame, runEventToolActive))
		case "tool-done":
			lines = append(lines, renderRunEventLine(item.text, width, item.frame, runEventToolDone))
		case "tool-failed":
			lines = append(lines, renderRunEventLine(item.text, width, item.frame, runEventToolFailed))
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

func renderStatusBar(m model, width int) string {
	bodyWidth := max(8, width-2)
	cwd := compactPath(m.cwd, max(20, bodyWidth/3))
	if cwd == "" {
		cwd = "-"
	}
	git := strings.TrimSpace(gitSyncButtonLabel(m))
	if git == "" {
		git = "-"
	}
	status := strings.TrimSpace(m.status)
	if status == "" {
		status = "idle"
	}
	stateParts := []string{
		status,
	}
	if m.app != nil && m.app.Agent != nil {
		if modelName := strings.TrimSpace(m.app.Agent.CurrentLLMModel()); modelName != "" {
			stateParts = append(stateParts, modelName)
		}
	}
	stateParts = append(stateParts, cwd, git)
	if ctxUsage := m.contextUsage; ctxUsage != nil && ctxUsage.ContextLength > 0 {
		stateParts = append(stateParts, fmt.Sprintf("ctx %d%%", min(100, ctxUsage.EstimatedInput*100/ctxUsage.ContextLength)))
	}
	if m.updateInfo.Available() {
		stateParts = append(stateParts, "update "+m.updateInfo.Latest)
	}
	actionParts := []string{"wheel scroll", "drag select copies", "Esc clear", "⌃S sync", "⌃T tree", "⌃K diff", "Tab Tab projects", "/cd", "/upgrade", "⌃C quit"}
	lines := []string{
		hintStyle.Render(fitDisplay(strings.Join(stateParts, " · "), bodyWidth)),
		hintStyle.Render(fitDisplay(strings.Join(actionParts, " · "), bodyWidth)),
	}
	return strings.Join(lines, "\n")
}

func renderWelcomeBanner(m model, width int) string {
	bodyWidth := max(20, width-2)
	modelName := "-"
	if m.app != nil && m.app.Agent != nil {
		modelName = m.app.Agent.CurrentLLMModel()
	}
	leftW, rightW := welcomeColumnWidths(bodyWidth)
	cwd := compactPath(m.cwd, max(20, leftW))
	if cwd == "" {
		cwd = "-"
	}
	session := "new session"
	if strings.TrimSpace(m.sessionID) != "" {
		session = "resumed " + shortID(m.sessionID)
	}
	header := []string{
		titleStyle.Render("Xiaoli TUI " + buildVersion()),
		"",
	}
	header = append(header, compactLogoLines(bodyWidth)...)
	header = append(header, "")
	left := []string{
		"",
		truncateDisplay(modelName, leftW),
		truncateDisplay(cwd, leftW),
		truncateDisplay(session, leftW),
	}
	if m.updateInfo.Available() {
		left = append(left, "update  "+m.updateInfo.Current+" -> "+m.updateInfo.Latest)
	}
	right := []string{
		titleStyle.Render("Getting started"),
	}
	right = append(right, strings.Split(renderWelcomeCommands(rightW), "\n")...)
	if notes := welcomeReleaseNotes(m.updateInfo, rightW); len(notes) > 0 {
		right = append(right, "")
		right = append(right, titleStyle.Render("What's new"))
		right = append(right, notes...)
	}
	if bodyWidth < 86 {
		var lines []string
		lines = append(lines, header...)
		lines = append(lines, left...)
		lines = append(lines, "")
		lines = append(lines, right...)
		return fitLines(lines, width)
	}
	right = compactWelcomeRight(right)
	height := max(len(left), len(right))
	lines := make([]string, 0, height)
	for i := 0; i < height; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines = append(lines, fitDisplay(l, leftW)+" │ "+fitDisplay(r, rightW))
	}
	lines = append(header, lines...)
	return fitLines(lines, width)
}

func welcomeColumnWidths(bodyWidth int) (leftW, rightW int) {
	leftW = max(24, bodyWidth*30/100)
	rightW = max(20, bodyWidth-leftW-3)
	return leftW, rightW
}

func compactLogoLines(width int) []string {
	source := figletLogoLines()
	lines := make([]string, 0, len(source))
	colors := []lipgloss.Color{"81", "45", "51", "49", "86"}
	for i, line := range source {
		if lipgloss.Width(line) > width {
			line = compactLogoLine(line, width)
		}
		style := lipgloss.NewStyle().Bold(true).Foreground(colors[i%len(colors)])
		lines = append(lines, style.Render(fitDisplay(line, width)))
	}
	return lines
}

func compactLogoLine(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return truncateDisplay(line, width)
	}
	left := strings.TrimRight(line, " ")
	if lipgloss.Width(left) <= width {
		return left
	}
	return truncateDisplay(left, width)
}

func renderWelcomeCommands(width int) string {
	type welcomeCommand struct {
		Key  string
		Text string
	}
	commands := []welcomeCommand{
		{Key: "/cd <path>", Text: "switch workspace"},
		{Key: "/tree", Text: "browse project"},
		{Key: "/diff", Text: "review changes"},
		{Key: "/commit", Text: "generate commit"},
		{Key: "/upgrade", Text: "show upgrade command"},
		{Key: "Ctrl+S", Text: "git sync"},
		{Key: "Ctrl+T", Text: "open tree"},
		{Key: "Ctrl+K", Text: "open diff"},
	}
	if width <= 0 {
		width = 40
	}
	cols := 1
	switch {
	case width >= 78:
		cols = 3
	case width >= 48:
		cols = 2
	}
	gap := 2
	cellW := max(16, (width-(cols-1)*gap)/cols)
	rows := (len(commands) + cols - 1) / cols
	lines := make([]string, 0, rows)
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	for row := 0; row < rows; row++ {
		cells := make([]string, 0, cols)
		for col := 0; col < cols; col++ {
			idx := row*cols + col
			if idx >= len(commands) {
				break
			}
			item := commands[idx]
			cell := keyStyle.Render(item.Key) + "  " + item.Text
			cells = append(cells, fitDisplay(cell, cellW))
		}
		lines = append(lines, strings.Join(cells, strings.Repeat(" ", gap)))
	}
	return strings.Join(lines, "\n")
}

func welcomeReleaseNotes(info updateInfo, width int) []string {
	limit := 4
	notes := make([]string, 0, min(limit, len(info.Notes)))
	for _, note := range info.Notes {
		note = strings.TrimSpace(note)
		if note == "" {
			continue
		}
		notes = append(notes, fitDisplay("- "+note, width))
		if len(notes) >= limit {
			break
		}
	}
	return notes
}

func compactWelcomeRight(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "Ctrl+S        git sync" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func fitLines(lines []string, width int) string {
	for i := range lines {
		lines[i] = fitDisplay(lines[i], width)
	}
	return strings.Join(lines, "\n")
}

func versionInfo() string {
	return "Xiaoli TUI " + buildVersion()
}

func newUpdateInfo(current, latest string, checkedAt time.Time) updateInfo {
	info := updateInfo{
		Current:   strings.TrimSpace(current),
		Latest:    strings.TrimSpace(latest),
		CheckedAt: checkedAt,
	}
	if info.Current == "" {
		info.Current = "dev"
	}
	if info.Available() {
		info.Command = upgradeCommand(info.Latest)
	}
	return info
}

func upgradeMessage(info updateInfo) string {
	if info.Available() {
		lines := []string{fmt.Sprintf("Update available: %s -> %s", info.Current, info.Latest)}
		if strings.TrimSpace(info.ReleaseURL) != "" {
			lines = append(lines, "Release notes: "+strings.TrimSpace(info.ReleaseURL))
		}
		lines = append(lines, "Run: "+info.Command)
		return strings.Join(lines, "\n")
	}
	current := strings.TrimSpace(info.Current)
	if current == "" {
		current = buildVersion()
	}
	if isDevVersion(current) {
		return "Development build.\nRun: " + upgradeCommand("latest")
	}
	if strings.TrimSpace(info.Latest) != "" {
		return fmt.Sprintf("Xiaoli TUI %s is up to date.", current)
	}
	return "No update information yet.\nRun: " + upgradeCommand("latest")
}

func upgradeCommand(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		target = "latest"
	}
	return "go install github.com/mnhkahn/xiaoli/tui/cmd/xiaoli@" + target
}

func buildVersion() string {
	if v := strings.TrimSpace(version); v != "" && v != "dev" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" {
			return v
		}
	}
	if strings.TrimSpace(version) == "" {
		return "dev"
	}
	return version
}

func checkForUpdateCmd(dataDir, current string) tea.Cmd {
	return func() tea.Msg {
		info := checkForUpdate(dataDir, current, time.Now(), &http.Client{Timeout: 3 * time.Second}, latestReleaseURL)
		return updateCheckDoneMsg{info: info}
	}
}

func checkForUpdate(dataDir, current string, now time.Time, client *http.Client, url string) updateInfo {
	current = strings.TrimSpace(current)
	if current == "" {
		current = "dev"
	}
	if isDevVersion(current) {
		return newUpdateInfo(current, "", now)
	}
	cachePath := updateCachePath(dataDir)
	if cached, ok := readUpdateCache(cachePath, current, now); ok {
		return cached
	}
	release, err := fetchLatestRelease(context.Background(), client, url)
	if err != nil {
		return newUpdateInfo(current, "", now)
	}
	info := newUpdateInfo(current, release.Tag, now)
	info.ReleaseURL = release.URL
	info.Notes = release.Notes
	writeUpdateCache(cachePath, info)
	return info
}

func updateCachePath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = localconfig.DefaultDataDir()
	}
	return filepath.Join(dataDir, "state", updateCacheFileName)
}

func readUpdateCache(path, current string, now time.Time) (updateInfo, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return updateInfo{}, false
	}
	var info updateInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return updateInfo{}, false
	}
	if info.Current != current || info.CheckedAt.IsZero() || now.Sub(info.CheckedAt) > 24*time.Hour {
		return updateInfo{}, false
	}
	if info.Command == "" && info.Available() {
		info.Command = upgradeCommand(info.Latest)
	}
	return info, true
}

func writeUpdateCache(path string, info updateInfo) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(data, '\n'), 0o644)
}

type releaseInfo struct {
	Tag   string
	URL   string
	Notes []string
}

func fetchLatestRelease(ctx context.Context, client *http.Client, url string) (releaseInfo, error) {
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return releaseInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "xiaoli-tui")
	resp, err := client.Do(req)
	if err != nil {
		return releaseInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return releaseInfo{}, fmt.Errorf("version check failed: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return releaseInfo{}, err
	}
	return latestReleaseFromJSON(data)
}

func latestReleaseFromJSON(data []byte) (releaseInfo, error) {
	var release struct {
		TagName string `json:"tag_name"`
		URL     string `json:"html_url"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return releaseInfo{}, err
	}
	tag := strings.TrimSpace(release.TagName)
	if !isSemver(tag) {
		return releaseInfo{}, fmt.Errorf("release tag is not semver: %q", release.TagName)
	}
	return releaseInfo{
		Tag:   tag,
		URL:   strings.TrimSpace(release.URL),
		Notes: extractReleaseNotes(release.Body, 5),
	}, nil
}

func extractReleaseNotes(body string, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	var notes []string
	for _, line := range strings.Split(body, "\n") {
		note := cleanReleaseNoteLine(line)
		if note == "" {
			continue
		}
		notes = append(notes, note)
		if len(notes) >= limit {
			break
		}
	}
	return notes
}

func cleanReleaseNoteLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "full changelog") {
		return ""
	}
	for _, prefix := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(line, prefix) {
			return cleanMarkdownInline(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
		}
	}
	dot := strings.Index(line, ". ")
	if dot > 0 {
		allDigits := true
		for _, r := range line[:dot] {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return cleanMarkdownInline(strings.TrimSpace(line[dot+2:]))
		}
	}
	return ""
}

func cleanMarkdownInline(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "`")
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "__", "")
	return strings.TrimSpace(text)
}

func isDevVersion(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || v == "dev" || v == "(devel)"
}

func isSemver(v string) bool {
	_, ok := parseSemver(v)
	return ok
}

func compareVersions(a, b string) int {
	av, aok := parseSemver(a)
	bv, bok := parseSemver(b)
	if aok && bok {
		for i := 0; i < len(av); i++ {
			if av[i] < bv[i] {
				return -1
			}
			if av[i] > bv[i] {
				return 1
			}
		}
		return 0
	}
	return strings.Compare(strings.TrimSpace(a), strings.TrimSpace(b))
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if idx := strings.IndexAny(v, "+-"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		if part == "" {
			return out, false
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
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
	lines = append(lines, "wheel scroll")
	lines = append(lines, "drag select copies")
	lines = append(lines, "Esc clear")
	lines = append(lines, "⌃S sync")
	lines = append(lines, "⌃T tree")
	lines = append(lines, "⌃K diff")
	lines = append(lines, "⌃Y copy")
	lines = append(lines, "⌃C quit")
	return lines
}

func isMouseWheel(msg tea.MouseMsg) bool {
	return msg.Type == tea.MouseWheelUp || msg.Type == tea.MouseWheelDown
}

func (m model) mouseInMainViewport(msg tea.MouseMsg) bool {
	mainW, _, bodyH, _, _ := layoutSizes(m.width, m.height)
	return msg.X >= 0 && msg.X < mainW && msg.Y >= 0 && msg.Y < bodyH
}

func (m *model) handleTranscriptSelectionMouse(msg tea.MouseMsg) (bool, tea.Cmd) {
	if isMouseWheel(msg) {
		return false, nil
	}
	point, ok := m.transcriptMousePoint(msg)
	if msg.Action == tea.MouseActionRelease || msg.Type == tea.MouseRelease {
		if !m.selection.dragging {
			return false, nil
		}
		if ok {
			m.selection.focus = point
		}
		m.selection.dragging = false
		lines := plainTranscriptLines(m.viewport.View())
		text := selectedTranscriptText(lines, m.selection)
		m.selection.text = text
		if strings.TrimSpace(text) == "" {
			m.selection = transcriptSelection{}
			m.status = "idle"
			return true, nil
		}
		m.selection.active = true
		m.status = "selected"
		return true, copySelectionCmd(text)
	}
	if !ok {
		if m.selection.dragging && msg.Action == tea.MouseActionMotion {
			m.selection.focus = point
			m.selection.active = true
			return true, nil
		}
		return false, nil
	}
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft && msg.Type != tea.MouseLeft {
			return false, nil
		}
		m.selection = transcriptSelection{dragging: true, anchor: point, focus: point}
		m.status = "selecting"
		return true, nil
	case tea.MouseActionMotion:
		if !m.selection.dragging {
			return false, nil
		}
		m.selection.focus = point
		if point != m.selection.anchor {
			m.selection.active = true
		}
		return true, nil
	default:
		return false, nil
	}
}

func (m model) transcriptMousePoint(msg tea.MouseMsg) (selectionPoint, bool) {
	mainW, _, bodyH, _, _ := layoutSizes(m.width, m.height)
	x := msg.X - 2
	y := msg.Y - 1
	point := selectionPoint{x: x, y: y}
	if x < 0 {
		point.x = 0
	}
	if y < 0 {
		point.y = 0
	}
	maxX := max(0, mainW-3)
	maxY := max(0, bodyH-2)
	if point.x > maxX {
		point.x = maxX
	}
	if point.y > maxY {
		point.y = maxY
	}
	return point, msg.X >= 2 && msg.X < mainW-1 && msg.Y >= 1 && msg.Y < bodyH-1
}

func copySelectionCmd(text string) tea.Cmd {
	return func() tea.Msg {
		return selectionCopyDoneMsg{text: text, err: copyTextToClipboard(text)}
	}
}

func plainTranscriptLines(rendered string) []string {
	if rendered == "" {
		return nil
	}
	raw := strings.Split(rendered, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, ansiEscapeRE.ReplaceAllString(line, ""))
	}
	return lines
}

func selectedTranscriptText(lines []string, sel transcriptSelection) string {
	if !sel.active && !sel.dragging {
		return ""
	}
	start, end := normalizedSelection(sel)
	if start.y >= len(lines) {
		return ""
	}
	if end.y >= len(lines) {
		end.y = len(lines) - 1
	}
	var out []string
	for y := start.y; y <= end.y; y++ {
		runes := []rune(lines[y])
		if len(runes) == 0 {
			out = append(out, "")
			continue
		}
		from, to := 0, len(runes)-1
		if y == start.y {
			from = clampInt(start.x, 0, len(runes)-1)
		}
		if y == end.y {
			to = clampInt(end.x, 0, len(runes)-1)
		}
		if from > to {
			out = append(out, "")
			continue
		}
		out = append(out, strings.TrimRight(string(runes[from:to+1]), " "))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

func renderTranscriptSelectionOverlay(lines []string, sel transcriptSelection, width int) string {
	start, end := normalizedSelection(sel)
	rendered := make([]string, 0, len(lines))
	for y, line := range lines {
		rendered = append(rendered, renderSelectionOverlayLine(line, y, start, end, width))
	}
	return strings.Join(rendered, "\n")
}

func renderSelectionOverlayLine(line string, y int, start, end selectionPoint, width int) string {
	if y < start.y || y > end.y || line == "" {
		return line
	}
	lastContentCol, ok := lastNonSpaceVisibleCol(line)
	if !ok {
		return line
	}
	maxSelectedCol := lastContentCol
	if width > 1 && maxSelectedCol >= width-1 {
		maxSelectedCol = width - 2
	}
	if maxSelectedCol < 0 {
		return line
	}
	var b strings.Builder
	col := 0
	inSelection := false
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			if loc := ansiEscapeRE.FindStringIndex(line[i:]); loc != nil && loc[0] == 0 {
				seq := line[i : i+loc[1]]
				b.WriteString(seq)
				i += loc[1]
				if inSelection {
					b.WriteString(selectionStartSeq)
				}
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		cellWidth := max(1, lipgloss.Width(string(r)))
		cellEnd := col + cellWidth - 1
		selected := cellEnd <= maxSelectedCol && selectionContains(start, end, selectionPoint{x: col, y: y})
		if selected && !inSelection {
			b.WriteString(selectionStartSeq)
			inSelection = true
		}
		if !selected && inSelection {
			b.WriteString(selectionEndSeq)
			inSelection = false
		}
		b.WriteString(line[i : i+size])
		col += cellWidth
		i += size
	}
	if inSelection {
		b.WriteString(selectionEndSeq)
	}
	return b.String()
}

func lastNonSpaceVisibleCol(line string) (int, bool) {
	last := -1
	col := 0
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			if loc := ansiEscapeRE.FindStringIndex(line[i:]); loc != nil && loc[0] == 0 {
				i += loc[1]
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		w := max(1, lipgloss.Width(string(r)))
		if !unicode.IsSpace(r) {
			last = col + w - 1
		}
		col += w
		i += size
	}
	return last, last >= 0
}

func normalizedSelection(sel transcriptSelection) (selectionPoint, selectionPoint) {
	start, end := sel.anchor, sel.focus
	if end.y < start.y || (end.y == start.y && end.x < start.x) {
		start, end = end, start
	}
	if start.y < 0 {
		start.y = 0
	}
	if start.x < 0 {
		start.x = 0
	}
	if end.y < 0 {
		end.y = 0
	}
	if end.x < 0 {
		end.x = 0
	}
	return start, end
}

func selectionContains(start, end, p selectionPoint) bool {
	if p.y < start.y || p.y > end.y {
		return false
	}
	if start.y == end.y {
		return p.x >= start.x && p.x <= end.x
	}
	if p.y == start.y {
		return p.x >= start.x
	}
	if p.y == end.y {
		return p.x <= end.x
	}
	return true
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func stripMouseSGRFragments(input string) string {
	if input == "" {
		return input
	}
	return mouseSGRFragmentRE.ReplaceAllString(input, "")
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
		section = append(section, limitLines(wrapText(pendingQuestionDisplay(m.pendingQuestion), width), 2)...)
		if len(m.pendingOptions) > 0 {
			section = append(section, limitLines(wrapText("choose: "+pendingOptionsDisplay(m.pendingOptions), width), 2)...)
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
		{Name: "cd", Description: "切换当前工作目录", Kind: "tui"},
		{Name: "tree", Description: "打开项目目录树", Kind: "tui"},
		{Name: "diff", Description: "查看当前 Git 变更", Kind: "tui"},
		{Name: "commit", Description: "生成并提交当前变更", Kind: "tui"},
		{Name: "version", Description: "查看 TUI 版本", Kind: "tui"},
		{Name: "upgrade", Description: "查看升级命令", Kind: "tui"},
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
	var lines []string
	if question != "" {
		lines = append(lines, titleStyle.Render(fitDisplay(pendingQuestionDisplay(question), width)))
		lines = append(lines, "")
	}
	for i, opt := range options {
		title, desc := splitPendingOptionDisplay(opt)
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
			lines = append(lines, hintStyle.Render(fitDisplay(descPrefix+desc, width)))
		}
	}
	if len(options) == 0 && len(lines) > 0 && strings.TrimSpace(question) != "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func pendingQuestionDisplay(question string) string {
	question = strings.TrimSpace(question)
	const bashPrefix = "是否允许执行命令："
	if strings.HasPrefix(question, bashPrefix) {
		command := strings.TrimSpace(strings.TrimPrefix(question, bashPrefix))
		if command != "" {
			return bashPrefix + summarizeCommandForDisplay(command)
		}
	}
	return summarizeLongText(question)
}

func pendingOptionsDisplay(options []string) string {
	parts := make([]string, 0, len(options))
	for _, opt := range options {
		title, desc := splitPendingOptionDisplay(opt)
		if desc != "" {
			parts = append(parts, title+" :: "+desc)
		} else {
			parts = append(parts, title)
		}
	}
	return strings.Join(parts, " / ")
}

func splitPendingOptionDisplay(option string) (string, string) {
	title, desc := splitPendingOption(option)
	if desc != "" {
		desc = summarizeCommandForDisplay(desc)
	}
	return title, desc
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

func summarizeCommandForDisplay(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	lines := nonEmptyLines(command)
	lower := strings.ToLower(command)
	first := strings.TrimSpace(lines[0])
	if isPythonCommand(first) {
		if script := pythonScriptPath(first); script != "" {
			return "python 执行脚本 " + script
		}
		if strings.Contains(first, " -c ") || strings.Contains(first, " -c=") || strings.HasSuffix(first, " -c") {
			return pythonInlineSummary(command)
		}
		if len(lines) > 1 || strings.Contains(lower, "<<") {
			return pythonInlineSummary(command)
		}
		return summarizeLongText(first)
	}
	if len(lines) > 1 {
		return fmt.Sprintf("%s（共 %d 行）", summarizeLongText(first), len(lines))
	}
	return summarizeLongText(command)
}

func nonEmptyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return []string{strings.TrimSpace(text)}
	}
	return out
}

func isPythonCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	name := fields[0]
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		name = parts[len(parts)-1]
	}
	return name == "python" || name == "python3" || strings.HasPrefix(name, "python3.")
}

func pythonScriptPath(command string) string {
	fields := strings.Fields(command)
	for i := 1; i < len(fields); i++ {
		field := strings.Trim(fields[i], `"'`)
		if field == "" || strings.HasPrefix(field, "-") {
			if field == "-m" {
				i++
			}
			continue
		}
		if strings.HasSuffix(field, ".py") {
			return field
		}
		break
	}
	return ""
}

func pythonInlineSummary(command string) string {
	body := extractPythonInlineBody(command)
	actions := pythonInlineActions(body)
	if len(actions) == 0 {
		return "python 执行内联脚本"
	}
	parts := []string{"python " + strings.Join(actions, "、")}
	if details := pythonInlineDetails(body); len(details) > 0 {
		parts = append(parts, details...)
	}
	return summarizeLongText(strings.Join(parts, " · "))
}

func extractPythonInlineBody(command string) string {
	command = strings.TrimSpace(command)
	lines := strings.Split(command, "\n")
	if len(lines) > 1 {
		body := strings.Join(lines[1:], "\n")
		bodyLines := strings.Split(body, "\n")
		if len(bodyLines) > 0 {
			last := strings.TrimSpace(bodyLines[len(bodyLines)-1])
			if isShellHereDocTerminator(last) {
				bodyLines = bodyLines[:len(bodyLines)-1]
			}
		}
		return strings.Join(bodyLines, "\n")
	}
	if idx := strings.Index(command, " -c "); idx >= 0 {
		return strings.Trim(strings.TrimSpace(command[idx+4:]), `"'`)
	}
	if idx := strings.Index(command, " -c="); idx >= 0 {
		return strings.Trim(strings.TrimSpace(command[idx+4:]), `"'`)
	}
	return ""
}

func isShellHereDocTerminator(line string) bool {
	if line == "" || strings.ContainsAny(line, " \t") {
		return false
	}
	for _, r := range line {
		if !(r == '_' || r == '-' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func pythonInlineActions(body string) []string {
	lower := strings.ToLower(body)
	var actions []string
	add := func(action string) {
		for _, existing := range actions {
			if existing == action {
				return
			}
		}
		actions = append(actions, action)
	}
	if strings.Contains(lower, "subprocess.") || strings.Contains(lower, "os.system(") || strings.Contains(lower, "exec(") {
		add("调用子进程")
	}
	if strings.Contains(lower, "requests.") || strings.Contains(lower, "urllib.") || strings.Contains(lower, "httpx.") {
		add("请求网络")
	}
	if strings.Contains(lower, ".write_text(") || strings.Contains(lower, ".write_bytes(") || strings.Contains(lower, "open(") && hasAny(lower, `"w"`, `'w'`, `"a"`, `'a'`) {
		add("写入文件")
	}
	if strings.Contains(lower, ".read_text(") || strings.Contains(lower, ".read_bytes(") || strings.Contains(lower, "open(") && hasAny(lower, `"r"`, `'r'`) {
		add("读取文件")
	}
	if strings.Contains(lower, "json.") {
		add("处理 JSON")
	}
	if strings.Contains(lower, "csv.") {
		add("处理 CSV")
	}
	if strings.Contains(lower, "sqlite3") {
		add("操作 SQLite")
	}
	if strings.Contains(lower, "print(") {
		add("输出结果")
	}
	if len(actions) == 0 {
		if imports := pythonImportedModules(body); len(imports) > 0 {
			add("使用 " + strings.Join(imports, "/"))
		}
	}
	if len(actions) > 3 {
		actions = actions[:3]
	}
	return actions
}

func pythonInlineDetails(body string) []string {
	var details []string
	if files := pythonFileLiterals(body); len(files) > 0 {
		details = append(details, "文件 "+strings.Join(limitStrings(files, 3), "、"))
	}
	if urls := pythonURLLiterals(body); len(urls) > 0 {
		details = append(details, "URL "+strings.Join(limitStrings(urls, 2), "、"))
	}
	if cmds := pythonCommandLiterals(body); len(cmds) > 0 {
		details = append(details, "命令 "+strings.Join(limitStrings(cmds, 2), "、"))
	}
	if len(details) > 2 {
		return details[:2]
	}
	return details
}

func pythonFileLiterals(body string) []string {
	var out []string
	for _, value := range quotedStringLiterals(body) {
		if looksLikeFileLiteral(value) {
			out = appendUniqueString(out, value)
		}
	}
	return out
}

func pythonURLLiterals(body string) []string {
	var out []string
	for _, value := range quotedStringLiterals(body) {
		lower := strings.ToLower(value)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			out = appendUniqueString(out, value)
		}
	}
	return out
}

func pythonCommandLiterals(body string) []string {
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "subprocess.") && !strings.Contains(lower, "os.system(") {
		return nil
	}
	var out []string
	for _, value := range quotedStringLiterals(body) {
		if value == "" || looksLikeFileLiteral(value) || strings.HasPrefix(strings.ToLower(value), "http") {
			continue
		}
		if strings.ContainsAny(value, " \t") || strings.Contains(value, "/") {
			out = appendUniqueString(out, summarizeLongText(value))
		}
	}
	return out
}

func quotedStringLiterals(body string) []string {
	var out []string
	for i := 0; i < len(body); i++ {
		quote := body[i]
		if quote != '\'' && quote != '"' {
			continue
		}
		start := i + 1
		escaped := false
		for j := start; j < len(body); j++ {
			if escaped {
				escaped = false
				continue
			}
			if body[j] == '\\' {
				escaped = true
				continue
			}
			if body[j] == quote {
				value := strings.TrimSpace(body[start:j])
				if value != "" {
					out = append(out, value)
				}
				i = j
				break
			}
		}
	}
	return out
}

func looksLikeFileLiteral(value string) bool {
	if value == "" || strings.ContainsAny(value, "\n\r\t") || strings.Contains(value, " ") {
		return false
	}
	lower := strings.ToLower(value)
	for _, suffix := range []string{".go", ".py", ".js", ".ts", ".json", ".yaml", ".yml", ".toml", ".md", ".txt", ".csv", ".html", ".css", ".sql", ".sh", ".log"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return strings.Contains(value, "/") || strings.HasPrefix(value, ".")
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func limitStrings(values []string, maxItems int) []string {
	if len(values) <= maxItems {
		return values
	}
	out := append([]string(nil), values[:maxItems]...)
	out = append(out, fmt.Sprintf("+%d", len(values)-maxItems))
	return out
}

func hasAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func pythonImportedModules(body string) []string {
	seen := map[string]bool{}
	var modules []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		var name string
		if strings.HasPrefix(line, "import ") {
			name = strings.Fields(strings.TrimPrefix(line, "import "))[0]
		} else if strings.HasPrefix(line, "from ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				name = fields[1]
			}
		}
		name = strings.Trim(strings.Split(name, ".")[0], ",")
		if name != "" && !seen[name] {
			seen[name] = true
			modules = append(modules, name)
		}
		if len(modules) >= 3 {
			break
		}
	}
	return modules
}

func summarizeLongText(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	const maxRunes = 96
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes-1]) + "…"
}

func (m model) hasPendingOptions() bool {
	return len(m.pendingOptions) > 0
}

func (m *model) movePendingChoice(delta int) bool {
	if m == nil || !m.hasPendingOptions() || delta == 0 {
		return false
	}
	m.pendingChoice = (m.pendingChoice + delta + len(m.pendingOptions)) % len(m.pendingOptions)
	return true
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

func (m *model) handleLocalCommand(text string) bool {
	args := strings.Fields(strings.TrimSpace(text))
	if len(args) == 0 {
		return false
	}
	switch strings.ToLower(args[0]) {
	case "/version":
		m.items = append(m.items, transcriptItem{role: "system", text: versionInfo()})
		return true
	case "/upgrade":
		m.items = append(m.items, transcriptItem{role: "system", text: upgradeMessage(m.updateInfo)})
		return true
	case "/cd":
		if len(args) < 2 {
			m.items = append(m.items, transcriptItem{role: "error", text: "用法：/cd <path>"})
			return true
		}
		target, err := resolveCDPath(m.cwd, strings.Join(args[1:], " "))
		if err != nil {
			m.items = append(m.items, transcriptItem{role: "error", text: err.Error()})
			return true
		}
		m.cwd = target
		m.gitSync = gitSyncStateForCWD(m.cwd)
		m.gitStatus = m.gitSync.Format()
		m.gitSyncFeedback = gitSyncFeedback{}
		m.recordCurrentWorkspace()
		m.items = append(m.items, transcriptItem{role: "system", text: "cwd: " + target})
		return true
	default:
		return false
	}
}

func resolveCDPath(cwd, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("用法：/cd <path>")
	}
	if strings.HasPrefix(raw, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("读取 home 目录失败：%w", err)
		}
		switch {
		case raw == "~":
			raw = home
		case strings.HasPrefix(raw, "~/"):
			raw = filepath.Join(home, strings.TrimPrefix(raw, "~/"))
		}
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(cwd, raw)
	}
	target, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("解析目录失败：%w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("目录不可用：%s", target)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("不是目录：%s", target)
	}
	return target, nil
}

func (m *model) markTab() {
	m.lastTabAt = time.Now()
}

func (m *model) consumeDoubleTab() bool {
	if m.lastTabAt.IsZero() || time.Since(m.lastTabAt) > 500*time.Millisecond {
		return false
	}
	m.lastTabAt = time.Time{}
	return true
}

func (m *model) openWorkspacePicker() {
	m.recordCurrentWorkspace()
	items := loadWorkspaces(m.workspaceStatePath)
	if len(items) == 0 && strings.TrimSpace(m.cwd) != "" {
		items = []workspaceItem{m.currentWorkspaceItem()}
	}
	m.workspacePicker = newWorkspacePicker(items, m.cwd, m.width, m.height)
}

func (m *model) switchWorkspace(item workspaceItem) {
	target := strings.TrimSpace(item.CWD)
	if target == "" {
		return
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		if m.workspacePicker != nil {
			m.workspacePicker.err = "Project unavailable: " + target
		}
		return
	}
	m.cwd = target
	m.sessionID = strings.TrimSpace(item.SessionID)
	m.gitSync = gitSyncStateForCWD(m.cwd)
	m.gitStatus = m.gitSync.Format()
	m.gitSyncFeedback = gitSyncFeedback{}
	m.lastError = ""
	m.pendingQuestion = ""
	m.pendingOptions = nil
	m.pendingChoice = 0
	if m.sessionID != "" {
		var agent *agentruntime.Agent
		if m.app != nil {
			agent = m.app.Agent
		}
		m.items = restoreTranscript(agent, m.sessionID)
	} else {
		m.items = nil
	}
	m.items = append(m.items, transcriptItem{role: "system", text: "Switched to " + target})
	m.recordCurrentWorkspace()
	m.refreshContextUsage()
	m.syncViewport(true)
}

func (m *model) currentWorkspaceItem() workspaceItem {
	return workspaceItem{
		CWD:        m.cwd,
		SessionID:  m.sessionID,
		Title:      workspaceTitle(m.app, m.sessionID, m.cwd),
		Model:      currentModelName(m.app),
		LastOpened: time.Now(),
	}
}

func (m *model) recordCurrentWorkspace() {
	if m == nil || strings.TrimSpace(m.workspaceStatePath) == "" || strings.TrimSpace(m.cwd) == "" {
		return
	}
	_ = upsertWorkspace(m.workspaceStatePath, m.currentWorkspaceItem())
}

func (m *model) createProjectSession() string {
	if m == nil || m.app == nil || m.app.Agent == nil {
		return ""
	}
	sessionID, err := m.app.Agent.NewSession(context.Background(), channelName, channelUser)
	if err != nil {
		return ""
	}
	return sessionID
}

func currentModelName(app *localapp.App) string {
	if app == nil || app.Agent == nil {
		return ""
	}
	return app.Agent.CurrentLLMModel()
}

func workspaceTitle(app *localapp.App, sessionID, cwd string) string {
	if app != nil && app.Agent != nil && app.Agent.SessionManager() != nil && strings.TrimSpace(sessionID) != "" {
		if info, err := app.Agent.SessionManager().Get(context.Background(), sessionID); err == nil && strings.TrimSpace(info.Title) != "" {
			return info.Title
		}
	}
	base := cwdDisplayName(cwd)
	if base == "" {
		return strings.TrimSpace(sessionID)
	}
	return base
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

func tickRunPulse() tea.Cmd {
	return tea.Tick(220*time.Millisecond, func(time.Time) tea.Msg {
		return runPulseTickMsg{}
	})
}

func (m *model) appendRunActiveEvent(label string) {
	m.items = append(m.items, transcriptItem{role: "run-active", text: label, frame: m.runPulseFrame})
	m.runPulseActive = true
	m.runPulseFrame = 0
}

func eventTranscriptItem(e agentevent.Event) transcriptItem {
	switch e.Type {
	case agentevent.TypeAgentRunStarted:
		return transcriptItem{role: "run-active"}
	case agentevent.TypeAgentRunCompleted:
		return transcriptItem{role: "run-done", text: "Delivered"}
	case agentevent.TypeAgentRunFailed:
		return transcriptItem{role: "run-failed", text: "Blocked"}
	case agentevent.TypeAgentToolStarted:
		return transcriptItem{role: "tool-active", text: toolEventText(e, "Tracing")}
	case agentevent.TypeAgentToolFinished:
		if toolEventError(e) != "" {
			return transcriptItem{role: "tool-failed", text: toolEventText(e, "Blocked")}
		}
		return transcriptItem{role: "tool-done", text: toolEventText(e, "Validated")}
	default:
		return transcriptItem{role: "event", text: eventSummary(e)}
	}
}

func eventSummary(e agentevent.Event) string {
	switch e.Type {
	case agentevent.TypeAgentRunStarted:
		return "Aligning"
	case agentevent.TypeAgentRunCompleted:
		return "Delivered"
	case agentevent.TypeAgentRunFailed:
		return "Blocked"
	case agentevent.TypeAgentToolStarted:
		return toolEventText(e, "Tracing")
	case agentevent.TypeAgentToolFinished:
		if errText := toolEventError(e); errText != "" {
			return fmt.Sprintf("%s: %s", toolEventText(e, "Blocked"), errText)
		}
		return toolEventText(e, "Validated")
	default:
		return e.Type
	}
}

func toolEventText(e agentevent.Event, verb string) string {
	name := "tool"
	if data, ok := e.Data.(map[string]any); ok {
		if value, ok := data["name"].(string); ok && strings.TrimSpace(value) != "" {
			name = value
		} else if value := fmt.Sprint(data["name"]); strings.TrimSpace(value) != "" && value != "<nil>" {
			name = value
		}
	}
	return fmt.Sprintf("%s %s", verb, name)
}

func toolEventError(e agentevent.Event) string {
	if data, ok := e.Data.(map[string]any); ok {
		if errText, ok := data["error"].(string); ok {
			return strings.TrimSpace(errText)
		}
	}
	return ""
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
	return false
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

func isBashApprovalChoice(text string) bool {
	switch strings.TrimSpace(text) {
	case "允许一次", "本会话允许此命令", "始终允许此命令", "始终允许子命令", "始终允许主命令":
		return true
	default:
		return false
	}
}

func normalizeBashApprovalChoice(text string) string {
	if isApprove(text) {
		return "允许一次"
	}
	return strings.TrimSpace(text)
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
