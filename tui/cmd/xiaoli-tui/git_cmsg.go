package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	agentruntime "github.com/mnhkahn/xiaoli-esp32/internal/agent/runtime"
	"github.com/mnhkahn/xiaoli-esp32/internal/agent/slash"
)

type gitCmsgPending struct {
	Active  bool
	Args    string
	Message string
}

type gitCmsgPrepareMsg struct {
	args    string
	stat    string
	files   string
	diff    string
	message string
	err     error
}

type gitCmsgCommitMsg struct {
	message string
	output  string
	push    bool
	err     error
}

func (m *model) startGitCmsgSlash(text string) tea.Cmd {
	cmd, ok := slash.Parse(text)
	if !ok || cmd.Name != "commit" {
		return nil
	}
	m.pendingGitCmsg = gitCmsgPending{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	m.activeCancel = cancel
	m.chatCanceled = false
	return startGitCmsgPrepare(ctx, m.app.Agent, m.cwd, cmd.Args)
}

func (m *model) handleGitCmsgChoice(text string) tea.Cmd {
	choice := strings.TrimSpace(text)
	switch {
	case choice == "提交并推送" || strings.EqualFold(choice, "push") || strings.EqualFold(choice, "commit and push"):
		msg := m.pendingGitCmsg.Message
		m.pendingGitCmsg = gitCmsgPending{}
		m.pendingQuestion = ""
		m.pendingOptions = nil
		m.pendingChoice = 0
		m.input.SetValue("")
		m.busy = true
		m.status = "git commit && push"
		m.items = append(m.items, transcriptItem{role: "event", text: "git commit and push started"})
		m.syncViewport(true)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		m.activeCancel = cancel
		m.chatCanceled = false
		return startGitCmsgCommit(ctx, m.cwd, msg, true)
	case choice == "确认提交" || strings.EqualFold(choice, "commit") || isApprove(choice):
		msg := m.pendingGitCmsg.Message
		m.pendingGitCmsg = gitCmsgPending{}
		m.pendingQuestion = ""
		m.pendingOptions = nil
		m.pendingChoice = 0
		m.input.SetValue("")
		m.busy = true
		m.status = "git commit"
		m.items = append(m.items, transcriptItem{role: "event", text: "git commit started"})
		m.syncViewport(true)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		m.activeCancel = cancel
		m.chatCanceled = false
		return startGitCmsgCommit(ctx, m.cwd, msg, false)
	case choice == "重新生成" || strings.EqualFold(choice, "regenerate"):
		args := m.pendingGitCmsg.Args
		m.pendingGitCmsg = gitCmsgPending{}
		m.pendingQuestion = ""
		m.pendingOptions = nil
		m.pendingChoice = 0
		m.input.SetValue("")
		m.busy = true
		m.status = "commit"
		m.items = append(m.items, transcriptItem{role: "event", text: "commit message regenerating"})
		m.syncViewport(true)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		m.activeCancel = cancel
		m.chatCanceled = false
		return startGitCmsgPrepare(ctx, m.app.Agent, m.cwd, args)
	case choice == "取消操作" || isReject(choice):
		m.pendingGitCmsg = gitCmsgPending{}
		m.pendingQuestion = ""
		m.pendingOptions = nil
		m.pendingChoice = 0
		m.input.SetValue("")
		m.items = append(m.items, transcriptItem{role: "system", text: "已取消提交。"})
		m.syncViewport(true)
		return nil
	default:
		m.items = append(m.items, transcriptItem{role: "system", text: "请选择：提交并推送 / 确认提交 / 重新生成 / 取消操作。"})
		m.syncViewport(true)
		return nil
	}
}

func startGitCmsgPrepare(ctx context.Context, agent *agentruntime.Agent, cwd, args string) tea.Cmd {
	return func() tea.Msg {
		stat, files, diff, err := prepareGitCmsgDiff(ctx, cwd, args)
		if err != nil {
			return gitCmsgPrepareMsg{args: args, err: err}
		}
		msg, err := generateGitCommitMessage(ctx, agent, stat, files, diff)
		if err != nil {
			return gitCmsgPrepareMsg{args: args, stat: stat, files: files, diff: diff, err: err}
		}
		return gitCmsgPrepareMsg{args: args, stat: stat, files: files, diff: diff, message: msg}
	}
}

func startGitCmsgCommit(ctx context.Context, cwd, message string, push bool) tea.Cmd {
	return func() tea.Msg {
		out, err := runGitCombinedContext(ctx, cwd, append([]string{"commit", "-m"}, splitCommitMessageArgs(message)...)...)
		if err != nil || !push {
			return gitCmsgCommitMsg{message: message, output: out, push: push, err: err}
		}
		pushOut, pushErr := runGitCombinedContext(ctx, cwd, "push")
		combined := strings.TrimRight(out, "\n")
		if strings.TrimSpace(pushOut) != "" {
			if combined != "" {
				combined += "\n"
			}
			combined += strings.TrimRight(pushOut, "\n")
		}
		return gitCmsgCommitMsg{message: message, output: combined, push: push, err: pushErr}
	}
}

func prepareGitCmsgDiff(ctx context.Context, cwd, args string) (string, string, string, error) {
	files, err := runGitCombinedContext(ctx, cwd, "diff", "--cached", "--name-only")
	if err != nil {
		return "", "", "", fmt.Errorf("读取暂存文件失败：%v\n%s", err, strings.TrimSpace(files))
	}
	if strings.TrimSpace(files) == "" {
		addArgs := []string{"add"}
		if fields := strings.Fields(args); len(fields) > 0 {
			addArgs = append(addArgs, fields...)
		} else {
			addArgs = append(addArgs, ".")
		}
		if out, err := runGitCombinedContext(ctx, cwd, addArgs...); err != nil {
			return "", "", "", fmt.Errorf("git add 失败：%v\n%s", err, strings.TrimSpace(out))
		}
		files, err = runGitCombinedContext(ctx, cwd, "diff", "--cached", "--name-only")
		if err != nil {
			return "", "", "", fmt.Errorf("读取暂存文件失败：%v\n%s", err, strings.TrimSpace(files))
		}
	}
	if strings.TrimSpace(files) == "" {
		return "", "", "", fmt.Errorf("暂存区没有变更，无法生成提交信息")
	}
	if bad := suspiciousGitFiles(files); len(bad) > 0 {
		return "", "", "", fmt.Errorf("暂存区包含疑似不应提交的文件，请先处理后再运行 /commit：\n%s", strings.Join(bad, "\n"))
	}
	stat, err := runGitCombinedContext(ctx, cwd, "diff", "--cached", "--stat")
	if err != nil {
		return "", "", "", fmt.Errorf("读取暂存统计失败：%v\n%s", err, strings.TrimSpace(stat))
	}
	diff, err := runGitCombinedContext(ctx, cwd, "diff", "--cached")
	if err != nil {
		return "", "", "", fmt.Errorf("读取暂存 diff 失败：%v\n%s", err, strings.TrimSpace(diff))
	}
	return stat, files, diff, nil
}

func generateGitCommitMessage(ctx context.Context, agent *agentruntime.Agent, stat, files, diff string) (string, error) {
	if agent == nil {
		return "", fmt.Errorf("agent 未初始化")
	}
	const maxDiff = 30000
	if len(diff) > maxDiff {
		diff = diff[:maxDiff] + "\n\n[diff truncated]\n"
	}
	system := "你是 Git 提交信息助手。只输出一条中文 Conventional Commits 提交信息，不要解释，不要 Markdown。格式：type(scope): 简短中文描述。"
	user := fmt.Sprintf("根据下面暂存区变更生成提交信息。\n\n文件：\n%s\n\n统计：\n%s\n\nDiff：\n%s", strings.TrimSpace(files), strings.TrimSpace(stat), diff)
	msg, err := agent.Generate(ctx, system, user)
	if err != nil {
		return "", fmt.Errorf("生成提交信息失败：%w", err)
	}
	msg = sanitizeCommitMessage(msg)
	if msg == "" {
		return "", fmt.Errorf("生成提交信息为空")
	}
	return msg, nil
}

func sanitizeCommitMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	msg = strings.Trim(msg, "`")
	lines := strings.Split(msg, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(strings.Trim(line, "`"))
		if line != "" && !strings.HasPrefix(line, "```") {
			return line
		}
	}
	return ""
}

func splitCommitMessageArgs(message string) []string {
	return []string{message}
}

func formatGitCmsgQuestion(msg gitCmsgPrepareMsg) string {
	var b strings.Builder
	b.WriteString("生成的提交信息是否符合要求？\n\n")
	fmt.Fprintf(&b, "%s\n\n", msg.message)
	if strings.TrimSpace(msg.files) != "" {
		b.WriteString("文件：\n")
		b.WriteString(strings.TrimSpace(msg.files))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(msg.stat) != "" {
		b.WriteString("统计：\n")
		b.WriteString(strings.TrimSpace(msg.stat))
	}
	return b.String()
}

func suspiciousGitFiles(files string) []string {
	var bad []string
	for _, line := range strings.Split(files, "\n") {
		file := strings.TrimSpace(line)
		if file == "" {
			continue
		}
		lower := strings.ToLower(file)
		if strings.Contains(lower, ".env") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "secrets") ||
			strings.HasSuffix(lower, ".pem") ||
			strings.HasSuffix(lower, ".key") ||
			strings.Contains(lower, "/logs/") ||
			strings.HasSuffix(lower, ".log") {
			bad = append(bad, file)
		}
	}
	return bad
}

func runGitCombined(cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return runGitCombinedContext(ctx, cwd, args...)
}

func runGitCombinedContext(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return buf.String(), fmt.Errorf("git %s timed out", strings.Join(args, " "))
	}
	if ctx.Err() == context.Canceled {
		return buf.String(), context.Canceled
	}
	return buf.String(), err
}
