package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	"github.com/mnhkahn/xiaoli/internal/agent/slash"
)

type codexReviewDoneMsg struct {
	args   []string
	output string
	err    error
}

type codexReviewLoop struct {
	Active    bool
	Round     int
	MaxRounds int
	Args      []string
	CWD       string
}

const defaultCodexReviewMaxRounds = 3

const maxCodexReviewFixPromptChars = 20000

const maxCodexReviewDisplayChars = 40000

const codexReviewChinesePrompt = "请用中文输出审查结论。若发现问题，请按严重程度列出问题、文件路径和修复建议；若没有问题，请明确说明未发现问题。"

func (m *model) startCodexReviewSlash(text string) tea.Cmd {
	cmd, ok := slash.Parse(text)
	if !ok || cmd.Name != "review" {
		return nil
	}
	args, err := codexReviewArgs(cmd.Args)
	if err != nil {
		return func() tea.Msg {
			return codexReviewDoneMsg{args: []string{"review"}, err: err}
		}
	}
	m.reviewLoop = codexReviewLoop{
		Active:    true,
		Round:     1,
		MaxRounds: defaultCodexReviewMaxRounds,
		Args:      append([]string(nil), args...),
		CWD:       m.cwd,
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultChatTimeout)
	m.activeCancel = cancel
	m.chatCanceled = false
	return startCodexReview(ctx, m.cwd, args)
}

func startCodexReview(ctx context.Context, cwd string, args []string) tea.Cmd {
	return func() tea.Msg {
		output, err := runCodexReview(ctx, cwd, args)
		return codexReviewDoneMsg{args: args, output: output, err: err}
	}
}

func runCodexReview(ctx context.Context, cwd string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = cwd
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return strings.TrimRight(buf.String(), "\n"), err
}

func (l codexReviewLoop) reviewLabel() string {
	if l.MaxRounds <= 0 {
		return "Codex review"
	}
	round := l.Round
	if round <= 0 {
		round = 1
	}
	return fmt.Sprintf("Codex review %d/%d", round, l.MaxRounds)
}

func (l codexReviewLoop) fixLabel() string {
	if l.MaxRounds <= 0 {
		return "Applying review fixes"
	}
	round := l.Round
	if round <= 0 {
		round = 1
	}
	return fmt.Sprintf("Applying review fixes %d/%d", round, l.MaxRounds)
}

func codexReviewPassed(output string) bool {
	text := strings.TrimSpace(output)
	if text == "" {
		return true
	}
	lower := strings.ToLower(text)
	passPhrases := []string{
		"lgtm",
		"no issues",
		"no findings",
		"looks good",
		"codex review completed",
	}
	for _, phrase := range passPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func codexReviewFixPrompt(output string, loop codexReviewLoop) string {
	findings := truncateRunes(strings.TrimSpace(output), maxCodexReviewFixPromptChars)
	return fmt.Sprintf("Fix the following Codex review findings from round %d/%d.\n\nReview findings:\n%s\n\nRequirements:\n- Apply the necessary code changes in the current repository.\n- Keep changes focused on the review findings.\n- Do not create commits.\n- After fixing, stop and summarize what changed.", loop.Round, loop.MaxRounds, findings)
}

func (m model) handleCodexReviewDone(msg codexReviewDoneMsg) (tea.Model, tea.Cmd) {
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
	loop := m.reviewLoop
	if msg.err != nil {
		m.reviewLoop = codexReviewLoop{}
		m.lastError = msg.err.Error()
		m.items = append(m.items, transcriptItem{role: "run-failed", text: "Review blocked", frame: m.runPulseFrame})
		text := strings.TrimSpace(msg.output)
		if text != "" {
			text += "\n"
		}
		text += msg.err.Error()
		m.items = append(m.items, transcriptItem{role: "error", text: text})
		m.syncViewport(true)
		return m, nil
	}
	output := strings.TrimSpace(msg.output)
	if !loop.Active || codexReviewPassed(output) {
		m.reviewLoop = codexReviewLoop{}
		m.items = append(m.items, transcriptItem{role: "run-done", text: fmt.Sprintf("第 %d 轮审查通过", max(1, loop.Round)), frame: m.runPulseFrame})
		if output == "" {
			output = "Codex review completed."
		}
		m.items = append(m.items, transcriptItem{role: "assistant", text: output})
		m.syncViewport(true)
		return m, nil
	}
	if loop.Round >= loop.MaxRounds {
		m.reviewLoop = codexReviewLoop{}
		m.items = append(m.items, transcriptItem{role: "run-failed", text: fmt.Sprintf("第 %d 轮审查仍有问题，已停止", loop.MaxRounds), frame: m.runPulseFrame})
		m.items = append(m.items, transcriptItem{role: "assistant", text: output})
		m.syncViewport(true)
		return m, nil
	}
	m.items = append(m.items, transcriptItem{role: "assistant", text: formatCodexReviewFindings(output, loop)})
	m.busy = true
	m.status = "review fix"
	m.runPulseActive = true
	m.items = append(m.items, transcriptItem{role: "run-active", text: loop.fixLabel(), frame: m.runPulseFrame})
	m.syncViewport(true)
	ctx, cancel := context.WithTimeout(context.Background(), defaultChatTimeout)
	m.activeCancel = cancel
	m.chatCanceled = false
	m.chatRunID++
	return m, tea.Batch(startReviewFixChatCmd(ctx, m.appAgent(), m.chatMsgs, m.sessionID, loop.CWD, codexReviewFixPrompt(output, loop), nil), waitForChat(m.chatMsgs), chatTimeoutCmd(m.chatRunID, defaultChatTimeout))
}

func (m model) handleCodexReviewFixDone(msg chatDoneMsg) (tea.Model, tea.Cmd) {
	loop := m.reviewLoop
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
	if msg.err != nil {
		m.busy = false
		m.status = "idle"
		m.runPulseActive = false
		m.reviewLoop = codexReviewLoop{}
		m.lastError = msg.err.Error()
		m.items = append(m.items, transcriptItem{role: "run-failed", text: "Review fix blocked", frame: m.runPulseFrame})
		m.items = append(m.items, transcriptItem{role: "error", text: msg.err.Error()})
		m.syncViewport(true)
		return m, nil
	}
	if strings.TrimSpace(msg.reply) != "" {
		m.items = append(m.items, transcriptItem{role: "assistant", text: msg.reply})
	}
	loop.Round++
	m.reviewLoop = loop
	m.busy = true
	m.status = "review"
	m.runPulseActive = true
	m.items = append(m.items, transcriptItem{role: "run-active", text: loop.reviewLabel(), frame: m.runPulseFrame})
	m.syncViewport(true)
	ctx, cancel := context.WithTimeout(context.Background(), defaultChatTimeout)
	m.activeCancel = cancel
	m.chatCanceled = false
	return m, startCodexReview(ctx, loop.CWD, loop.Args)
}

func formatCodexReviewFindings(output string, loop codexReviewLoop) string {
	return fmt.Sprintf("第 %d 轮审查发现问题：\n\n%s", max(1, loop.Round), truncateRunes(strings.TrimSpace(output), maxCodexReviewDisplayChars))
}

func truncateRunes(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars]) + fmt.Sprintf("\n\n[已截断：原始内容 %d 字符，仅保留前 %d 字符。]", len(runes), maxChars)
}

func (m model) appAgent() *agentruntime.Agent {
	if m.app == nil {
		return nil
	}
	return m.app.Agent
}

func codexReviewArgs(raw string) ([]string, error) {
	fields, err := splitCodexReviewArgs(raw)
	if err != nil {
		return nil, err
	}
	targetArgs, prompt, err := codexReviewTargetAndPrompt(fields)
	if err != nil {
		return nil, err
	}
	args := []string{"review"}
	if len(targetArgs) == 0 && strings.TrimSpace(prompt) == "" {
		args = append(args, "--uncommitted")
	}
	args = append(args, targetArgs...)
	if strings.TrimSpace(prompt) != "" {
		args = append(args, codexReviewPrompt(prompt))
	}
	return args, nil
}

func codexReviewPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return codexReviewChinesePrompt
	}
	return prompt + "\n\n" + codexReviewChinesePrompt
}

func codexReviewTargetAndPrompt(fields []string) ([]string, string, error) {
	var args []string
	var prompt []string
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		switch field {
		case "--uncommitted":
			args = append(args, field)
		case "--base", "--commit":
			if i+1 >= len(fields) {
				return nil, "", fmt.Errorf("%s 需要一个参数", field)
			}
			args = append(args, field, fields[i+1])
			i++
		default:
			prompt = append(prompt, field)
		}
	}
	return args, strings.Join(prompt, " "), nil
}

func splitCodexReviewArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var fields []string
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range raw {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if b.Len() > 0 {
				fields = append(fields, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("review 参数引号未闭合")
	}
	if b.Len() > 0 {
		fields = append(fields, b.String())
	}
	return fields, nil
}
