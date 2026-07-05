package builtin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
)

type ShellConfig struct {
	Enabled        bool
	Timeout        time.Duration
	MaxOutputBytes int64
	PolicyPath     string
}

type ShellTool struct {
	config ShellConfig
}

// Global bash pending/approval stores — shared across all ShellTool instances.
// bashPending: convID → command hash → pendingInfo — commands waiting for user approval
// bashApproved: convID → approvedHash — commands the user has approved
var (
	bashPendingMu  sync.Mutex
	bashPending    = map[string]map[string]pendingInfo{}
	bashApprovedMu sync.Mutex
	bashApproved   = map[string]approvedInfo{}
)

const bashApprovalTTL = 30 * time.Minute

type pendingInfo struct {
	Command    string
	Hash       string
	ToolUseID  string
	PolicyPath string
	ExpiresAt  time.Time
}

type approvedInfo struct {
	Hash      string
	ExpiresAt time.Time
}

// StoreBashApproval records that a user approved a command for a given conversation.
// Called by the Lark card action handler when the user clicks "允许".
// hash is the command hash to verify against the pending approval.
// If caller doesn't know the hash (non-bash card), hash is "" and the call is silently ignored.
func StoreBashApproval(convID, hash string) {
	_ = StoreBashApprovalChoice(convID, hash, bashApprovalAllowOnce)
}

func StoreBashApprovalChoice(convID, hash, choice string) error {
	choice = normalizeBashApprovalChoice(choice)
	if choice == bashApprovalReject {
		return nil
	}
	bashPendingMu.Lock()
	pendingByHash := bashPending[convID]
	p, ok := pendingByHash[hash]
	bashPendingMu.Unlock()
	if !ok || time.Now().After(p.ExpiresAt) || p.Hash != hash {
		return nil
	}
	if err := applyBashApprovalChoice(convID, p.Command, choice, p.PolicyPath); err != nil {
		return err
	}
	bashApprovedMu.Lock()
	bashApproved[convID] = approvedInfo{Hash: p.Hash, ExpiresAt: time.Now().Add(bashApprovalTTL)}
	bashApprovedMu.Unlock()
	return nil
}

// PendingBashApproval returns the current pending approval even if it has
// expired, so callers can send a precise tool result back to the model.
func PendingBashApproval(convID, hash string) (command, toolUseID string, expired, ok bool) {
	bashPendingMu.Lock()
	defer bashPendingMu.Unlock()
	p, ok := bashPending[convID][hash]
	if !ok || p.Hash != hash {
		return "", "", false, false
	}
	return p.Command, p.ToolUseID, time.Now().After(p.ExpiresAt), true
}

// PendingBashCommand returns the exact command currently waiting for approval.
func PendingBashCommand(convID, hash string) (string, bool) {
	bashPendingMu.Lock()
	defer bashPendingMu.Unlock()
	p, ok := bashPending[convID][hash]
	if !ok || time.Now().After(p.ExpiresAt) || p.Hash != hash {
		return "", false
	}
	return p.Command, true
}

// PendingBashToolUseID returns the tool call id for the pending approval.
func PendingBashToolUseID(convID, hash string) (string, bool) {
	bashPendingMu.Lock()
	defer bashPendingMu.Unlock()
	p, ok := bashPending[convID][hash]
	if !ok || time.Now().After(p.ExpiresAt) || p.Hash != hash {
		return "", false
	}
	return p.ToolUseID, true
}

// ClearBashApproval removes any pending/approved state for a conversation
func ClearBashApproval(convID string) {
	bashPendingMu.Lock()
	delete(bashPending, convID)
	bashPendingMu.Unlock()
	bashApprovedMu.Lock()
	delete(bashApproved, convID)
	bashApprovedMu.Unlock()
}

func ClearBashApprovalHash(convID, hash string) {
	bashPendingMu.Lock()
	if pendingByHash := bashPending[convID]; pendingByHash != nil {
		delete(pendingByHash, hash)
		if len(pendingByHash) == 0 {
			delete(bashPending, convID)
		}
	}
	bashPendingMu.Unlock()
	bashApprovedMu.Lock()
	if approved, ok := bashApproved[convID]; ok && approved.Hash == hash {
		delete(bashApproved, convID)
	}
	bashApprovedMu.Unlock()
}

func NewShellTool(cfg ShellConfig) *ShellTool {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 512 * 1024
	}
	return &ShellTool{config: cfg}
}

func (t *ShellTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "bash",
		Desc: "执行 shell 命令。所有命令需要用户确认后方可执行。支持管道和重定向。不支持交互式命令。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"command": {
				Type:     schema.String,
				Desc:     "要执行的 shell 命令",
				Required: true,
			},
		}),
	}, nil
}

func (t *ShellTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return "用法：bash(command=\"要执行的命令\")\n支持管道和重定向，不支持交互式命令。", nil
	}

	sessionID, _ := ctx.Value(SubAgentParentKey).(string)
	if sessionID != "" && bashCommandAllowed(sessionID, cmd, t.config.PolicyPath) {
		return t.execute(ctx, cmd)
	}

	// 1. Check if user has explicitly approved this command
	if sessionID != "" {
		bashApprovedMu.Lock()
		a, hasApproved := bashApproved[sessionID]
		bashApprovedMu.Unlock()
		if hasApproved && time.Now().Before(a.ExpiresAt) && hashCommand(cmd) == a.Hash {
			bashApprovedMu.Lock()
			delete(bashApproved, sessionID)
			bashApprovedMu.Unlock()
			return t.execute(ctx, cmd)
		}
	}

	// 2. No approval found — store pending and ask user
	cmdHash := hashCommand(cmd)
	toolUseID := newBashToolUseID(cmdHash)
	if sessionID != "" {
		bashPendingMu.Lock()
		if bashPending[sessionID] == nil {
			bashPending[sessionID] = map[string]pendingInfo{}
		}
		bashPending[sessionID][cmdHash] = pendingInfo{
			Command:    cmd,
			Hash:       cmdHash,
			ToolUseID:  toolUseID,
			PolicyPath: t.config.PolicyPath,
			ExpiresAt:  time.Now().Add(bashApprovalTTL),
		}
		bashPendingMu.Unlock()
	}

	if holder, ok := ctx.Value(ToolUseConfirmKey).(*ToolUseConfirmHolder); ok {
		channelName, _ := ctx.Value(SubAgentChannelKey).(string)
		deviceID, _ := ctx.Value(SubAgentDeviceIDKey).(string)
		logger.Infof("bash approval confirm created: session=%s channel=%s device=%s tool_use_id=%s command_len=%d", sessionID, channelName, deviceID, toolUseID, len([]rune(cmd)))
		holder.Append(&PendingToolUseConfirm{
			RequestID:   toolUseID,
			SessionID:   sessionID,
			ChannelName: channelName,
			DeviceID:    deviceID,
			ToolName:    "bash",
			ToolUseID:   toolUseID,
			Question:    fmt.Sprintf("是否允许执行命令：%s", cmd),
			Options:     bashApprovalOptions(cmd, t.config.PolicyPath),
			BashHash:    cmdHash,
			BashCommand: cmd,
		})
	} else {
		logger.Infof("bash approval confirm missing holder: session=%s tool_use_id=%s command_len=%d", sessionID, toolUseID, len([]rune(cmd)))
	}

	return fmt.Sprintf("命令「%s」需要您的确认，已发送审批请求，等待用户回复", cmd), nil
}

func (t *ShellTool) execute(ctx context.Context, cmd string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, t.config.Timeout)
	defer cancel()

	shCmd := exec.CommandContext(runCtx, "sh", "-c", cmd)

	var stdout, stderr limitedBuffer
	stdout.limit = t.config.MaxOutputBytes
	stderr.limit = t.config.MaxOutputBytes / 4
	if stderr.limit <= 0 {
		stderr.limit = 1024
	}
	shCmd.Stdout = &stdout
	shCmd.Stderr = &stderr

	err := shCmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return t.formatResult(cmd, stdout.String(), stderr.String(), fmt.Errorf("命令执行超时（%s）", t.config.Timeout)), nil
	}
	if runCtx.Err() != nil {
		return "", runCtx.Err()
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("exit code %d", exitErr.ExitCode())
		}
		return t.formatResult(cmd, stdout.String(), stderr.String(), err), nil
	}
	return t.formatResult(cmd, stdout.String(), stderr.String(), nil), nil
}

func (t *ShellTool) formatResult(cmd, stdout, stderr string, err error) string {
	var b strings.Builder
	if err != nil {
		fmt.Fprintf(&b, "命令执行失败：%s\n", cmd)
		fmt.Fprintf(&b, "错误：%v\n", err)
	} else {
		fmt.Fprintf(&b, "命令执行完成：%s\n", cmd)
	}
	if stdout != "" {
		b.WriteString("\n输出：\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteByte('\n')
		}
	}
	if stderr != "" {
		b.WriteString("\n错误输出：\n")
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

type limitedBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := int(b.limit) - b.Buffer.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.Buffer.Write(p[:remaining])
		return len(p), nil
	}
	_, _ = b.Buffer.Write(p)
	return len(p), nil
}

func hashCommand(cmd string) string {
	h := sha256.Sum256([]byte(cmd))
	return fmt.Sprintf("%x", h[:8])
}

func newBashToolUseID(cmdHash string) string {
	if len(cmdHash) > 12 {
		cmdHash = cmdHash[:12]
	}
	return fmt.Sprintf("toolu_bash_%s_%d", cmdHash, time.Now().UnixNano())
}
