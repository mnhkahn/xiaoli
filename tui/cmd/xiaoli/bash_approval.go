package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func startApprovedBashCommand(ctx context.Context, cwd, command, sessionID, toolUseID string, reviewFix bool) tea.Cmd {
	return func() tea.Msg {
		output, err := runShellCommandContext(ctx, cwd, command)
		return bashApprovalDoneMsg{command: command, output: output, err: err, sessionID: sessionID, toolUseID: toolUseID, reviewFix: reviewFix}
	}
}

func bashApprovalTranscriptItem(msg bashApprovalDoneMsg) transcriptItem {
	text := "$ " + strings.TrimSpace(msg.command)
	output := strings.TrimRight(msg.output, "\n")
	if output != "" {
		text += "\n" + output
	}
	if msg.err != nil {
		text += "\n" + msg.err.Error()
	}
	return transcriptItem{role: "shell", text: text}
}

func formatApprovedBashFollowup(toolUseID, command, output string, err error) string {
	err = normalizeApprovedBashError(output, err)
	var b strings.Builder
	writeBashToolResultHeader(&b, toolUseID)
	if err != nil {
		b.WriteString("status=error\n")
	} else {
		b.WriteString("status=success\n")
	}
	fmt.Fprintf(&b, "命令：\n```bash\n%s\n```\n\n", strings.TrimSpace(command))
	if strings.TrimSpace(output) != "" {
		fmt.Fprintf(&b, "输出：\n```text\n%s\n```\n\n", strings.TrimRight(output, "\n"))
	} else {
		b.WriteString("输出为空。\n\n")
	}
	if err != nil {
		fmt.Fprintf(&b, "执行错误：%v\n\n", err)
	}
	b.WriteString("[/TOOL_RESULT]\n\n")
	b.WriteString("上面是你刚才请求的 bash 工具调用结果，tool_use_id 与原工具请求绑定。请基于这个结果继续完成上一项任务；如果还需要工作，直接调用下一个工具，不要只回复“开始处理”。不要重复请求执行同一条命令；如果需要新的高风险命令，再明确说明。")
	return b.String()
}

func normalizeApprovedBashError(output string, err error) error {
	if err != nil {
		return err
	}
	if shellOutputLooksLikeError(output) {
		return fmt.Errorf("shell reported an error in output")
	}
	return nil
}

func shellOutputLooksLikeError(output string) bool {
	text := strings.TrimSpace(output)
	if text == "" {
		return false
	}
	errorMarkers := []string{
		"zsh: no matches found:",
		"zsh:1: no matches found:",
		"bash: no match:",
		"no matches found:",
		"command not found:",
		"permission denied:",
		"syntax error",
	}
	lower := strings.ToLower(text)
	for _, marker := range errorMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func formatExpiredBashFollowup(toolUseID, command string) string {
	var b strings.Builder
	writeBashToolResultHeader(&b, toolUseID)
	b.WriteString("status=expired\n")
	if strings.TrimSpace(command) != "" {
		fmt.Fprintf(&b, "命令：\n```bash\n%s\n```\n\n", strings.TrimSpace(command))
	}
	b.WriteString("执行错误：用户没有在有效期内确认命令，审批已失效。\n\n")
	b.WriteString("[/TOOL_RESULT]\n\n")
	b.WriteString("上面的 bash 工具调用已经过期，不能继续等待用户确认。请基于当前任务重新判断下一步；如果仍需要执行命令，请重新发起新的 bash 工具调用并等待新的审批。")
	return b.String()
}

func writeBashToolResultHeader(b *strings.Builder, toolUseID string) {
	b.WriteString("[TOOL_RESULT]\n")
	fmt.Fprintf(b, "tool=bash\n")
	if strings.TrimSpace(toolUseID) != "" {
		fmt.Fprintf(b, "tool_use_id=%s\n", strings.TrimSpace(toolUseID))
	}
}
