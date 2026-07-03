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

func TestShellToolStoresRichApprovalOptions(t *testing.T) {
	ctx, holder := NewAskDataHolder(context.Background())
	ctx = context.WithValue(ctx, SubAgentParentKey, "ses_policy_tool")
	tool := NewShellTool(ShellConfig{PolicyPath: filepath.Join(t.TempDir(), "policy.json")})
	if _, err := tool.InvokableRun(ctx, `{"command":"git diff --cached --stat"}`); err != nil {
		t.Fatal(err)
	}
	ask := holder.Get()
	if ask == nil || ask.BashHash == "" {
		t.Fatalf("ask data = %#v, want bash approval", ask)
	}
	joined := strings.Join(ask.Options, "\n")
	if !strings.Contains(joined, "本会话允许此命令") || !strings.Contains(joined, "始终允许子命令") {
		t.Fatalf("options = %#v, want rich approval options", ask.Options)
	}
}
