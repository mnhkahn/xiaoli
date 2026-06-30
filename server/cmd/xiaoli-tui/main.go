package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"xiaoli/server/internal/agent/localapp"
	"xiaoli/server/internal/agent/localconfig"
	agentruntime "xiaoli/server/internal/agent/runtime"
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

type chatDoneMsg struct {
	reply string
	err   error
}

type eventMsg struct {
	event agentevent.Event
}

type model struct {
	app       *localapp.App
	events    chan agentevent.Event
	input     textinput.Model
	items     []transcriptItem
	width     int
	height    int
	busy      bool
	status    string
	lastError string
	quitting  bool
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	userStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	agentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("114"))
	eventStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	boxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	sideStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
)

func main() {
	configPath := flag.String("config", "", "path to local xiaoli settings.json")
	initConfig := flag.Bool("init", false, "create default local settings and secrets files")
	prompt := flag.String("prompt", "你是小李，一个本地运行的中文 Agent。回答要清楚、直接、适合终端阅读。", "system prompt")
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

	app, err := localapp.New(localapp.Options{ConfigPath: *configPath, Prompt: *prompt, Ensure: *initConfig})
	if err != nil {
		fmt.Fprintf(os.Stderr, "xiaoli-tui: %v\n\n", err)
		fmt.Fprintln(os.Stderr, "Run with -init to create local settings, then configure models.default and API key.")
		os.Exit(1)
	}
	defer app.Close()

	p := tea.NewProgram(newModel(app), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "xiaoli-tui: %v\n", err)
		os.Exit(1)
	}
}

func newModel(app *localapp.App) model {
	input := textinput.New()
	input.Placeholder = "Ask Xiaoli..."
	input.Focus()
	input.CharLimit = 4096
	input.Width = 80

	events := make(chan agentevent.Event, 64)
	app.Bus.SubscribeAll(func(_ context.Context, e agentevent.Event) error {
		select {
		case events <- e:
		default:
		}
		return nil
	})

	return model{
		app:    app,
		events: events,
		input:  input,
		status: "idle",
		items: []transcriptItem{{
			role: "system",
			text: "Xiaoli TUI ready. Press Ctrl+C to quit.",
		}},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, waitForEvent(m.events))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = max(20, msg.Width-4)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" || m.busy {
				return m, nil
			}
			if strings.EqualFold(text, "/quit") || strings.EqualFold(text, "/exit") {
				m.quitting = true
				return m, tea.Quit
			}
			m.input.SetValue("")
			m.busy = true
			m.status = "running"
			m.lastError = ""
			m.items = append(m.items, transcriptItem{role: "user", text: text})
			return m, tea.Batch(runChat(m.app.Agent, text), waitForEvent(m.events))
		}
	case chatDoneMsg:
		m.busy = false
		m.status = "idle"
		if msg.err != nil {
			m.lastError = msg.err.Error()
			m.items = append(m.items, transcriptItem{role: "error", text: msg.err.Error()})
		} else {
			m.items = append(m.items, transcriptItem{role: "assistant", text: msg.reply})
		}
		return m, waitForEvent(m.events)
	case eventMsg:
		m.items = append(m.items, transcriptItem{role: "event", text: eventSummary(msg.event)})
		return m, waitForEvent(m.events)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "Loading..."
	}
	sideW := 30
	mainW := max(40, m.width-sideW-3)
	bodyH := max(8, m.height-5)

	transcript := renderTranscript(m.items, mainW, bodyH)
	sidebar := renderSidebar(m, sideW, bodyH)
	top := lipgloss.JoinHorizontal(lipgloss.Top,
		boxStyle.Width(mainW).Height(bodyH).Render(transcript),
		sideStyle.Width(sideW).Height(bodyH).Render(sidebar),
	)

	prompt := boxStyle.Width(m.width - 2).Render(m.input.View())
	return lipgloss.JoinVertical(lipgloss.Left, top, prompt)
}

func renderTranscript(items []transcriptItem, width, height int) string {
	lines := make([]string, 0, len(items)*2)
	for _, item := range items {
		label := item.role
		switch item.role {
		case "user":
			label = userStyle.Render("You")
		case "assistant":
			label = agentStyle.Render("Xiaoli")
		case "event":
			label = eventStyle.Render("event")
		case "error":
			label = errStyle.Render("error")
		default:
			label = eventStyle.Render(item.role)
		}
		wrapped := lipgloss.NewStyle().Width(max(20, width-4)).Render(item.text)
		lines = append(lines, label+"\n"+wrapped)
	}
	content := strings.Join(lines, "\n\n")
	renderedLines := strings.Split(content, "\n")
	if len(renderedLines) > height {
		renderedLines = renderedLines[len(renderedLines)-height:]
	}
	return strings.Join(renderedLines, "\n")
}

func renderSidebar(m model, width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Xiaoli"))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "status: %s\n", m.status)
	fmt.Fprintf(&b, "model: %s\n", m.app.Agent.CurrentLLMModel())
	fmt.Fprintf(&b, "storage: %s\n", m.app.Runtime.StorageBackend)
	fmt.Fprintf(&b, "runs: %s\n", m.app.RunLogDir)
	fmt.Fprintf(&b, "bash: %v\n", m.app.Runtime.BashConfig.Enabled)
	if m.lastError != "" {
		b.WriteString("\n")
		b.WriteString(errStyle.Render("last error"))
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Width(width - 4).Render(m.lastError))
	}
	if tasks := m.app.Agent.TaskStatusList(); len(tasks) > 0 {
		b.WriteString("\n\ntasks\n")
		for _, task := range tasks {
			fmt.Fprintf(&b, "- %s %s\n", task.ID, task.Status)
		}
	}
	statuses := m.app.Agent.MCPStatus()
	if len(statuses) > 0 {
		b.WriteString("\nMCP\n")
		for _, s := range statuses {
			state := "down"
			if s.Connected {
				state = "up"
			}
			fmt.Fprintf(&b, "- %s %s\n", state, s.URL)
		}
	}
	b.WriteString("\nkeys\n")
	b.WriteString("enter send\n")
	b.WriteString("esc quit\n")

	lines := strings.Split(b.String(), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func runChat(agent *agentruntime.Agent, text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		reply, err := agent.ChatWithContextOptions(ctx, channelUser, channelUser, text, agentruntime.ChatOptions{Channel: channelName})
		return chatDoneMsg{reply: reply, err: err}
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
