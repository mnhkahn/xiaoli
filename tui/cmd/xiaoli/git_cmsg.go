package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	"github.com/mnhkahn/xiaoli/internal/agent/slash"
)

type gitCmsgPending struct {
	Active   bool
	Args     string
	Message  string
	Stat     string
	Shortcut bool
}

type gitCmsgPrepareMsg struct {
	args    string
	stat    string
	files   string
	diff    string
	message string
	err     error
}

type gitCmsgProgressMsg struct {
	source <-chan gitCmsgProgressMsg
	text   string
}

type gitCmsgProgressClosedMsg struct{}

type gitCmsgCommitMsg struct {
	message string
	output  string
	summary string
	push    bool
	err     error
}

// gitCmsgPreviewMsg is intentionally separate from the /commit preparation
// flow: Cmd+K must never stage, commit, or push changes.
type gitCmsgPreviewMsg struct {
	text string
	err  error
}

const gitCommitMessageSystemPrompt = "你是 Git 提交信息助手。只输出中文 Conventional Commits 提交信息，不要解释，不要 Markdown。必须使用以下结构：第一行是 type(scope): 简短中文描述；随后空一行；再用 `- ` 开头的列表逐条说明主要变更。即使变更较小，也至少给出一条列表项。"

const (
	gitCmsgPrepareTimeout = 15 * time.Second
	gitCmsgPrepareRetries = 2
	gitCmsgRetryBackoff   = time.Second
	gitCmsgDiffTotalLimit = 12 * 1024
	gitCmsgFileDiffLimit  = 24 * 1024
	gitCmsgFileLineLimit  = 1000
	gitCmsgPreviewLimit   = 4 * 1024
	gitCmsgPreviewLines   = 80
)

func (m *model) startGitCmsgSlash(text string) tea.Cmd {
	cmd, ok := slash.Parse(text)
	if !ok || cmd.Name != "commit" {
		return nil
	}
	m.pendingGitCmsg = gitCmsgPending{}
	ctx, cancel := context.WithCancel(context.Background())
	m.activeCancel = cancel
	m.chatCanceled = false
	return m.startGitCmsgPrepare(ctx, cmd.Args)
}

func (m *model) startGitCmsgPrepare(ctx context.Context, args string) tea.Cmd {
	progress := make(chan gitCmsgProgressMsg, gitCmsgPrepareRetries)
	m.gitCmsgProgress = progress
	return tea.Batch(
		startGitCmsgPrepare(ctx, m.app.Agent, m.cwd, args, progress),
		waitForGitCmsgProgress(progress),
	)
}

func (m *model) handleGitCmsgChoice(text string) tea.Cmd {
	choice := strings.TrimSpace(text)
	switch {
	case choice == "提交并推送" || strings.EqualFold(choice, "push") || strings.EqualFold(choice, "commit and push"):
		msg := m.pendingGitCmsg.Message
		summary := m.pendingGitCmsg.summary()
		m.pendingGitCmsg = gitCmsgPending{}
		m.pendingQuestion = ""
		m.pendingOptions = nil
		m.pendingChoice = 0
		m.input.SetValue("")
		m.busy = true
		m.status = "git commit && push"
		m.appendRunActiveEvent("Commit and push")
		m.syncViewport(true)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		m.activeCancel = cancel
		m.chatCanceled = false
		return tea.Batch(startGitCmsgCommit(ctx, m.cwd, msg, true, summary), tickRunPulse(), terminalTitleCmd(*m))
	case choice == "确认提交" || strings.EqualFold(choice, "commit") || isApprove(choice):
		msg := m.pendingGitCmsg.Message
		summary := m.pendingGitCmsg.summary()
		m.pendingGitCmsg = gitCmsgPending{}
		m.pendingQuestion = ""
		m.pendingOptions = nil
		m.pendingChoice = 0
		m.input.SetValue("")
		m.busy = true
		m.status = "git commit"
		m.appendRunActiveEvent("Committing changes")
		m.syncViewport(true)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		m.activeCancel = cancel
		m.chatCanceled = false
		return tea.Batch(startGitCmsgCommit(ctx, m.cwd, msg, false, summary), tickRunPulse(), terminalTitleCmd(*m))
	case choice == "重新生成" || strings.EqualFold(choice, "regenerate"):
		args := m.pendingGitCmsg.Args
		m.pendingGitCmsg = gitCmsgPending{}
		m.pendingQuestion = ""
		m.pendingOptions = nil
		m.pendingChoice = 0
		m.input.SetValue("")
		m.busy = true
		m.status = "commit"
		m.appendRunActiveEvent("Refreshing commit plan")
		m.syncViewport(true)
		ctx, cancel := context.WithCancel(context.Background())
		m.activeCancel = cancel
		m.chatCanceled = false
		return tea.Batch(m.startGitCmsgPrepare(ctx, args), tickRunPulse(), terminalTitleCmd(*m))
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

func startGitCmsgPrepare(ctx context.Context, agent *agentruntime.Agent, cwd, args string, progress chan gitCmsgProgressMsg) tea.Cmd {
	return func() tea.Msg {
		defer close(progress)
		for attempt := 0; attempt <= gitCmsgPrepareRetries; attempt++ {
			attemptCtx, cancel := context.WithTimeout(ctx, gitCmsgPrepareTimeout)
			stat, files, diff, err := prepareGitCmsgDiff(attemptCtx, cwd, args)
			if err == nil {
				var msg string
				msg, err = generateGitCommitMessage(attemptCtx, agent, stat, files, diff)
				if err == nil {
					cancel()
					return gitCmsgPrepareMsg{args: args, stat: stat, files: files, diff: diff, message: msg}
				}
			}
			timedOut := errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
			if timedOut {
				err = fmt.Errorf("模型请求超时（单次上限 %s）：%w", gitCmsgPrepareTimeout, context.DeadlineExceeded)
			}
			retryable := timedOut || isRetryableGitCmsgError(err)
			cancel()
			if !retryable || attempt == gitCmsgPrepareRetries {
				if timedOut {
					err = fmt.Errorf("提交信息生成超时，已重试 %d 次仍未完成：%w", gitCmsgPrepareRetries, err)
				}
				return gitCmsgPrepareMsg{args: args, stat: stat, files: files, diff: diff, err: err}
			}
			progressText := fmt.Sprintf("提交信息生成失败，正在第 %d/%d 次重试（第 %d/%d 次尝试）", attempt+1, gitCmsgPrepareRetries, attempt+2, gitCmsgPrepareRetries+1)
			if timedOut {
				progressText = fmt.Sprintf("提交信息生成超时，正在第 %d/%d 次重试（第 %d/%d 次尝试）", attempt+1, gitCmsgPrepareRetries, attempt+2, gitCmsgPrepareRetries+1)
			}
			progress <- gitCmsgProgressMsg{
				source: progress,
				text:   progressText,
			}
			delay := gitCmsgRetryBackoff * (1 << attempt)
			select {
			case <-ctx.Done():
				return gitCmsgPrepareMsg{args: args, stat: stat, files: files, diff: diff, err: ctx.Err()}
			case <-time.After(delay):
			}
		}
		return gitCmsgPrepareMsg{args: args, err: context.DeadlineExceeded}
	}
}

func waitForGitCmsgProgress(ch <-chan gitCmsgProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return gitCmsgProgressClosedMsg{}
		}
		return msg
	}
}

func isRetryableGitCmsgError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "received empty choices") ||
		strings.Contains(msg, "empty choices from openai api")
}

func startGitCmsgPreview(ctx context.Context, agent *agentruntime.Agent, cwd string) tea.Cmd {
	return func() tea.Msg {
		stat, files, diff, err := prepareGitPreviewDiff(ctx, cwd)
		if err != nil {
			return gitCmsgPreviewMsg{err: err}
		}
		message, err := generateGitCommitMessage(ctx, agent, stat, files, diff)
		if err != nil {
			return gitCmsgPreviewMsg{err: err}
		}
		return gitCmsgPreviewMsg{text: formatGitCommitPreview(message, stat)}
	}
}

func (pending gitCmsgPending) summary() string {
	if !pending.Shortcut {
		return ""
	}
	return formatGitCommitPreview(pending.Message, pending.Stat)
}

func startGitCmsgCommit(ctx context.Context, cwd, message string, push bool, summary string) tea.Cmd {
	return func() tea.Msg {
		out, err := runGitCombinedContext(ctx, cwd, gitCommitMessageArgs(message)...)
		if err != nil || !push {
			return gitCmsgCommitMsg{message: message, output: out, summary: summary, push: push, err: err}
		}
		pushOut, pushErr := runGitCombinedContext(ctx, cwd, "push")
		combined := strings.TrimRight(out, "\n")
		if strings.TrimSpace(pushOut) != "" {
			if combined != "" {
				combined += "\n"
			}
			combined += strings.TrimRight(pushOut, "\n")
		}
		return gitCmsgCommitMsg{message: message, output: combined, summary: summary, push: push, err: pushErr}
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
	stat, err := runGitCombinedContext(ctx, cwd, "diff", "--cached", "--numstat")
	if err != nil {
		return "", "", "", fmt.Errorf("读取暂存统计失败：%v\n%s", err, strings.TrimSpace(stat))
	}
	diff, err := runGitCombinedContext(ctx, cwd, "diff", "--cached")
	if err != nil {
		return "", "", "", fmt.Errorf("读取暂存 diff 失败：%v\n%s", err, strings.TrimSpace(diff))
	}
	return stat, files, diff, nil
}

// prepareGitPreviewDiff reads the complete working-tree snapshot without
// changing the index. git diff HEAD covers staged and unstaged tracked files;
// untracked text files are appended so a newly created test/file is reflected
// in Cmd+K's preview too.
func prepareGitPreviewDiff(ctx context.Context, cwd string) (string, string, string, error) {
	files, err := runGitCombinedContext(ctx, cwd, "diff", "HEAD", "--name-only")
	if err != nil {
		return "", "", "", fmt.Errorf("读取工作区文件失败：%v\n%s", err, strings.TrimSpace(files))
	}
	stat, err := runGitCombinedContext(ctx, cwd, "diff", "HEAD", "--numstat")
	if err != nil {
		return "", "", "", fmt.Errorf("读取工作区统计失败：%v\n%s", err, strings.TrimSpace(stat))
	}
	diff, err := runGitCombinedContext(ctx, cwd, "diff", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("读取工作区 diff 失败：%v\n%s", err, strings.TrimSpace(diff))
	}

	status, err := runGitCombinedContext(ctx, cwd, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", "", "", fmt.Errorf("读取未跟踪文件失败：%v\n%s", err, strings.TrimSpace(status))
	}
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		if !strings.HasPrefix(line, "?? ") {
			continue
		}
		path := strings.TrimPrefix(line, "?? ")
		if path == "" {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(cwd, path))
		if readErr != nil {
			return "", "", "", fmt.Errorf("读取未跟踪文件 %s 失败：%w", path, readErr)
		}
		additions := strings.Count(string(content), "\n")
		if len(content) > 0 && content[len(content)-1] != '\n' {
			additions++
		}
		if strings.TrimSpace(stat) != "" {
			stat += "\n"
		}
		stat += fmt.Sprintf("%d\t0\t%s", additions, path)
		if strings.TrimSpace(files) != "" {
			files += "\n"
		}
		files += path
		// Keep the generated title aware of new files, without overwhelming the
		// model when a large generated file is present.
		if len(content) <= 30000 {
			diff += fmt.Sprintf("\n\n[untracked file: %s]\n%s", path, content)
		}
	}
	if strings.TrimSpace(files) == "" {
		return "", "", "", fmt.Errorf("工作区没有变更，无法生成提交摘要")
	}
	return stat, files, diff, nil
}

func generateGitCommitMessage(ctx context.Context, agent *agentruntime.Agent, stat, files, diff string) (string, error) {
	if agent == nil {
		return "", fmt.Errorf("agent 未初始化")
	}
	diff = compactGitCommitDiff(diff)
	system := gitCommitMessageSystemPrompt
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

// compactGitCommitDiff bounds only the context sent to the commit-message
// model. The staged Git content is intentionally left untouched.
func compactGitCommitDiff(diff string) string {
	if len(diff) <= gitCmsgDiffTotalLimit {
		return diff
	}

	sections := strings.Split(diff, "\ndiff --git ")
	var out strings.Builder
	for i, section := range sections {
		if i > 0 {
			section = "diff --git " + section
		}
		if len(section) > gitCmsgFileDiffLimit || strings.Count(section, "\n")+1 > gitCmsgFileLineLimit {
			originalBytes := len(section)
			originalLines := strings.Count(section, "\n") + 1
			section = truncateGitDiffLines(section, gitCmsgPreviewLines)
			section = truncateGitDiffBytes(section, gitCmsgPreviewLimit)
			section += fmt.Sprintf("\n\n[large diff omitted: %d lines, %d bytes]\n", originalLines, originalBytes)
		}

		if out.Len()+len(section) > gitCmsgDiffTotalLimit {
			const omitted = "\n[remaining diff omitted: total context limit]\n"
			remaining := gitCmsgDiffTotalLimit - out.Len() - len(omitted)
			if remaining > 0 {
				out.WriteString(truncateGitDiffBytes(section, remaining))
			}
			out.WriteString(omitted)
			break
		}
		out.WriteString(section)
	}
	return out.String()
}

func truncateGitDiffLines(text string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return text
	}
	return strings.Join(lines[:maxLines], "\n")
}

func truncateGitDiffBytes(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	limit := maxBytes
	for limit > 0 && (text[limit]&0xC0) == 0x80 {
		limit--
	}
	return text[:limit]
}

func sanitizeCommitMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	lines := strings.Split(msg, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			continue
		}
		cleaned = append(cleaned, strings.TrimRight(line, " \t"))
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func splitCommitMessageArgs(message string) []string {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	parts := strings.Split(message, "\n\n")
	args := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			args = append(args, part)
		}
	}
	return args
}

func gitCommitMessageArgs(message string) []string {
	args := []string{"commit"}
	for _, part := range splitCommitMessageArgs(message) {
		args = append(args, "-m", part)
	}
	return args
}

func formatGitCmsgCommitError(err error, output string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	output = strings.TrimSpace(output)
	if output != "" {
		msg += "\n" + output
	}
	return msg
}

func formatGitCmsgQuestion(msg gitCmsgPrepareMsg, width int) string {
	var b strings.Builder
	b.WriteString("生成的提交信息是否符合要求？\n\n")
	fmt.Fprintf(&b, "%s\n\n", msg.message)
	if strings.TrimSpace(msg.stat) != "" {
		b.WriteString("统计：\n")
		b.WriteString(formatGitNumstat(msg.stat, width))
	}
	return b.String()
}

func formatGitCommitPreview(message, stat string) string {
	title := strings.TrimSpace(strings.Split(strings.TrimSpace(message), "\n")[0])
	if title == "" {
		title = "chore: 更新工作区变更"
	}
	type row struct {
		path       string
		additions  int
		deletions  int
		binaryFile bool
	}
	var rows []row
	totalAdditions, totalDeletions := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(stat), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		item := row{path: parts[2]}
		if parts[0] == "-" || parts[1] == "-" {
			item.binaryFile = true
		} else {
			item.additions, _ = strconv.Atoi(parts[0])
			item.deletions, _ = strconv.Atoi(parts[1])
			totalAdditions += item.additions
			totalDeletions += item.deletions
		}
		rows = append(rows, item)
	}
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%d files changed", len(rows))
	if totalAdditions > 0 {
		fmt.Fprintf(&b, ", %d insertions(+)", totalAdditions)
	}
	if totalDeletions > 0 {
		fmt.Fprintf(&b, ", %d deletions(-)", totalDeletions)
	}
	for _, item := range rows {
		b.WriteString("\n ")
		b.WriteString(item.path)
		b.WriteString(" | ")
		if item.binaryFile {
			b.WriteString("Bin")
			continue
		}
		changes := item.additions + item.deletions
		b.WriteString(strconv.Itoa(changes))
		b.WriteByte(' ')
		b.WriteString(strings.Repeat("+", min(changes, 48)))
		if item.deletions > 0 {
			b.WriteString(strings.Repeat("-", min(item.deletions, 24)))
		}
	}
	return b.String()
}

var (
	gitAdditionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	gitDeletionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

func formatGitNumstat(stat string, width int) string {
	type row struct {
		path      string
		additions int
		deletions int
		binary    bool
	}
	var rows []row
	totalAdditions, totalDeletions := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(stat), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		item := row{path: parts[2]}
		if parts[0] == "-" || parts[1] == "-" {
			item.binary = true
		} else {
			item.additions, _ = strconv.Atoi(parts[0])
			item.deletions, _ = strconv.Atoi(parts[1])
			totalAdditions += item.additions
			totalDeletions += item.deletions
		}
		rows = append(rows, item)
	}
	if len(rows) == 0 {
		return strings.TrimSpace(stat)
	}

	pathWidth := max(24, width-18)
	var b strings.Builder
	for i, item := range rows {
		path := truncateDisplay(item.path, pathWidth)
		b.WriteString(path)
		b.WriteString(strings.Repeat(" ", max(2, pathWidth-lipgloss.Width(path)+2)))
		if item.binary {
			b.WriteString("binary")
		} else {
			var deltas []string
			if item.additions > 0 {
				deltas = append(deltas, gitAdditionStyle.Render("+"+strconv.Itoa(item.additions)))
			}
			if item.deletions > 0 {
				deltas = append(deltas, gitDeletionStyle.Render("-"+strconv.Itoa(item.deletions)))
			}
			if len(deltas) == 0 {
				deltas = append(deltas, "0")
			}
			b.WriteString(strings.Join(deltas, "  "))
		}
		if i < len(rows)-1 {
			b.WriteByte('\n')
		}
	}
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%d 个文件", len(rows))
	if totalAdditions > 0 {
		b.WriteString("，")
		b.WriteString(gitAdditionStyle.Render("+" + strconv.Itoa(totalAdditions)))
	}
	if totalDeletions > 0 {
		b.WriteString(" ")
		b.WriteString(gitDeletionStyle.Render("-" + strconv.Itoa(totalDeletions)))
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
	gitArgs := append([]string{"-c", "core.quotepath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
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
