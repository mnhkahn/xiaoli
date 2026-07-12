package builtin

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashApprovalOptions(t *testing.T) {
	options := strings.Join(bashApprovalOptions("git diff --cached --stat", filepath.Join(t.TempDir(), "policy.json")), "\n")
	for _, want := range []string{
		"允许一次",
		"本会话允许此命令",
		"始终允许此命令",
		"始终允许子命令 :: git diff *",
		"拒绝",
	} {
		if !strings.Contains(options, want) {
			t.Fatalf("options = %q, want %q", options, want)
		}
	}
	if strings.Contains(options, "始终允许主命令 :: git *") {
		t.Fatalf("options = %q, should not offer risky git main command", options)
	}
}

func TestBashSessionApprovalAllowsExactCommand(t *testing.T) {
	convID := "ses_policy_session"
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if bashCommandAllowed(convID, "git status", policyPath) {
		t.Fatal("command allowed before session approval")
	}
	if err := applyBashApprovalChoice(convID, "git status", bashApprovalAllowSessionExact, policyPath); err != nil {
		t.Fatal(err)
	}
	if !bashCommandAllowed(convID, "git status", policyPath) {
		t.Fatal("exact command not allowed after session approval")
	}
	if bashCommandAllowed(convID, "git diff", policyPath) {
		t.Fatal("different command allowed by exact session approval")
	}
}

func TestBashPersistentSubcommandAllowsMatchingCommands(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := applyBashApprovalChoice("ses_policy_persist", "git diff --cached --stat", bashApprovalAllowAlwaysSub, policyPath); err != nil {
		t.Fatal(err)
	}
	if !bashCommandAllowed("other_session", "git diff README.md", policyPath) {
		t.Fatal("git diff subcommand not allowed by persistent rule")
	}
	if bashCommandAllowed("other_session", "git status", policyPath) {
		t.Fatal("git status allowed by git diff persistent rule")
	}
}

func TestShellToolStoresPendingToolUseConfirm(t *testing.T) {
	ctx, askHolder := NewAskDataHolder(context.Background())
	ctx, confirmHolder := NewToolUseConfirmHolder(ctx)
	ctx = context.WithValue(ctx, SubAgentParentKey, "ses_policy_tool")
	tool := NewShellTool(ShellConfig{PolicyPath: filepath.Join(t.TempDir(), "policy.json")})
	if _, err := tool.InvokableRun(ctx, `{"command":"git diff --cached --stat"}`); err == nil {
		t.Fatal("InvokableRun() error = nil, want approval interrupt")
	}
	if ask := askHolder.Get(); ask != nil {
		t.Fatalf("ask data = %#v, want bash approval to avoid AskData", ask)
	}
	confirm := confirmHolder.Get()
	if confirm == nil || confirm.BashHash == "" || confirm.ToolUseID == "" {
		t.Fatalf("confirm = %#v, want bash approval", confirm)
	}
	if confirm.BashCommand != "git diff --cached --stat" {
		t.Fatalf("BashCommand = %q, want original command", confirm.BashCommand)
	}
	if !strings.HasPrefix(confirm.ToolUseID, "toolu_bash_") {
		t.Fatalf("ToolUseID = %q, want toolu_bash_ prefix", confirm.ToolUseID)
	}
	if got, ok := PendingBashToolUseID("ses_policy_tool", confirm.BashHash); !ok || got != confirm.ToolUseID {
		t.Fatalf("PendingBashToolUseID() = %q, %v; want %q, true", got, ok, confirm.ToolUseID)
	}
	joined := strings.Join(confirm.Options, "\n")
	if !strings.Contains(joined, "本会话允许此命令") || !strings.Contains(joined, "始终允许子命令") {
		t.Fatalf("options = %#v, want rich approval options", confirm.Options)
	}
}

func TestShellToolStripsLeadingCdToCurrentDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	ctx, confirmHolder := NewToolUseConfirmHolder(context.Background())
	ctx = context.WithValue(ctx, SubAgentParentKey, "ses_policy_cd_current")
	tool := NewShellTool(ShellConfig{PolicyPath: filepath.Join(t.TempDir(), "policy.json")})

	if _, err := tool.InvokableRun(ctx, `{"command":"cd `+cwd+` && git status --short"}`); err == nil {
		t.Fatal("InvokableRun() error = nil, want approval interrupt")
	}

	confirm := confirmHolder.Get()
	if confirm == nil {
		t.Fatal("missing pending bash approval")
	}
	if confirm.BashCommand != "git status --short" {
		t.Fatalf("BashCommand = %q, want cd prefix stripped", confirm.BashCommand)
	}
}

func TestShellToolRejectsLeadingCdToOtherAbsoluteDirectory(t *testing.T) {
	current := t.TempDir()
	other := t.TempDir()
	t.Chdir(current)
	ctx, confirmHolder := NewToolUseConfirmHolder(context.Background())
	ctx = context.WithValue(ctx, SubAgentParentKey, "ses_policy_cd_other")
	tool := NewShellTool(ShellConfig{PolicyPath: filepath.Join(t.TempDir(), "policy.json")})

	got, err := tool.InvokableRun(ctx, `{"command":"cd `+other+` && git status --short"}`)
	if err != nil {
		t.Fatal(err)
	}

	if confirm := confirmHolder.Get(); confirm != nil {
		t.Fatalf("confirm = %#v, want command rejected before approval", confirm)
	}
	if !strings.Contains(got, "不要使用 cd") || !strings.Contains(got, "相对路径") {
		t.Fatalf("InvokableRun() = %q, want cd guidance", got)
	}
}

func TestShellToolKeepsMultiplePendingApprovalsForSession(t *testing.T) {
	ctx, confirmHolder := NewToolUseConfirmHolder(context.Background())
	ctx = context.WithValue(ctx, SubAgentParentKey, "ses_policy_multi")
	tool := NewShellTool(ShellConfig{PolicyPath: filepath.Join(t.TempDir(), "policy.json")})

	if _, err := tool.InvokableRun(ctx, `{"command":"git status --short"}`); err == nil {
		t.Fatal("first InvokableRun() error = nil, want approval interrupt")
	}
	if _, err := tool.InvokableRun(ctx, `{"command":"git diff --cached --stat"}`); err == nil {
		t.Fatal("second InvokableRun() error = nil, want approval interrupt")
	}
	confirms := confirmHolder.All()
	if len(confirms) != 2 {
		t.Fatalf("confirmHolder.All() len = %d, want 2: %#v", len(confirms), confirms)
	}
	first := confirms[0]
	second := confirms[1]
	if _, _, _, ok := PendingBashApproval("ses_policy_multi", first.BashHash); !ok {
		t.Fatalf("first pending approval missing: %#v", first)
	}
	if _, _, _, ok := PendingBashApproval("ses_policy_multi", second.BashHash); !ok {
		t.Fatalf("second pending approval missing: %#v", second)
	}
	ClearBashApprovalHash("ses_policy_multi", first.BashHash)
	if _, _, _, ok := PendingBashApproval("ses_policy_multi", first.BashHash); ok {
		t.Fatalf("first pending approval still present after clear")
	}
	if _, _, _, ok := PendingBashApproval("ses_policy_multi", second.BashHash); !ok {
		t.Fatalf("second pending approval was cleared with first")
	}
}
