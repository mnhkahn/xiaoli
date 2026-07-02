package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func startApprovedBashCommand(ctx context.Context, cwd, command, sessionID string) tea.Cmd {
	return func() tea.Msg {
		output, err := runShellCommandContext(ctx, cwd, command)
		return bashApprovalDoneMsg{command: command, output: output, err: err, sessionID: sessionID}
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

func formatApprovedBashFollowup(command, output string, err error) string {
	var b strings.Builder
	b.WriteString("用户已批准并执行了你刚才请求的 bash 命令。\n\n")
	fmt.Fprintf(&b, "命令：\n```bash\n%s\n```\n\n", strings.TrimSpace(command))
	if strings.TrimSpace(output) != "" {
		fmt.Fprintf(&b, "输出：\n```text\n%s\n```\n\n", strings.TrimRight(output, "\n"))
	} else {
		b.WriteString("输出为空。\n\n")
	}
	if err != nil {
		fmt.Fprintf(&b, "执行错误：%v\n\n", err)
	}
	b.WriteString("请基于这个结果继续完成上一项任务。不要重复请求执行同一条命令；如果需要新的高风险命令，再明确说明。")
	return b.String()
}
