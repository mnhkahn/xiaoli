package builtin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	bashApprovalAllowOnce         = "允许一次"
	bashApprovalAllowSessionExact = "本会话允许此命令"
	bashApprovalAllowAlwaysExact  = "始终允许此命令"
	bashApprovalAllowAlwaysSub    = "始终允许子命令"
	bashApprovalAllowAlwaysMain   = "始终允许主命令"
	bashApprovalReject            = "拒绝"
	bashPolicyMatchExact          = "exact"
	bashPolicyMatchSubcommand     = "subcommand"
	bashPolicyMatchMain           = "main"
	bashPolicyDecisionAllow       = "allow"
	bashPolicyDecisionDeny        = "deny"
)

var (
	bashSessionPolicyMu sync.Mutex
	bashSessionAllow    = map[string]map[string]bool{}
)

type bashPolicyRule struct {
	Match     string `json:"match"`
	Pattern   string `json:"pattern"`
	Decision  string `json:"decision"`
	CreatedAt string `json:"created_at,omitempty"`
}

type bashPolicyFile struct {
	Version int              `json:"version"`
	Allow   []bashPolicyRule `json:"allow"`
	Deny    []bashPolicyRule `json:"deny"`
}

type bashCommandPattern struct {
	Exact      string
	Subcommand string
	Main       string
	Compound   bool
}

func bashApprovalOptions(command, policyPath string) []string {
	pattern := bashCommandPatterns(command)
	options := []string{
		bashApprovalAllowOnce + " :: " + pattern.Exact,
		bashApprovalAllowSessionExact + " :: " + pattern.Exact,
	}
	if strings.TrimSpace(policyPath) != "" {
		options = append(options, bashApprovalAllowAlwaysExact+" :: "+pattern.Exact)
		if !pattern.Compound && pattern.Subcommand != "" && pattern.Subcommand != pattern.Exact && !bashRiskySubcommand(pattern.Subcommand) {
			options = append(options, bashApprovalAllowAlwaysSub+" :: "+pattern.Subcommand+" *")
		}
		if !pattern.Compound && pattern.Main != "" && pattern.Main != pattern.Subcommand && !bashRiskyMainCommand(pattern.Main) {
			options = append(options, bashApprovalAllowAlwaysMain+" :: "+pattern.Main+" *")
		}
	}
	options = append(options, bashApprovalReject)
	return options
}

func bashCommandAllowed(convID, command, policyPath string) bool {
	pattern := bashCommandPatterns(command)
	if bashPersistentDecision(policyPath, pattern, bashPolicyDecisionDeny) {
		return false
	}
	bashSessionPolicyMu.Lock()
	sessionAllowed := bashSessionAllow[convID][pattern.Exact]
	bashSessionPolicyMu.Unlock()
	if sessionAllowed {
		return true
	}
	return bashPersistentDecision(policyPath, pattern, bashPolicyDecisionAllow)
}

func applyBashApprovalChoice(convID, command, choice, policyPath string) error {
	choice = normalizeBashApprovalChoice(choice)
	pattern := bashCommandPatterns(command)
	switch choice {
	case bashApprovalAllowSessionExact:
		bashSessionPolicyMu.Lock()
		if bashSessionAllow[convID] == nil {
			bashSessionAllow[convID] = map[string]bool{}
		}
		bashSessionAllow[convID][pattern.Exact] = true
		bashSessionPolicyMu.Unlock()
	case bashApprovalAllowAlwaysExact:
		return appendBashPolicyRule(policyPath, bashPolicyMatchExact, pattern.Exact, bashPolicyDecisionAllow)
	case bashApprovalAllowAlwaysSub:
		return appendBashPolicyRule(policyPath, bashPolicyMatchSubcommand, pattern.Subcommand, bashPolicyDecisionAllow)
	case bashApprovalAllowAlwaysMain:
		return appendBashPolicyRule(policyPath, bashPolicyMatchMain, pattern.Main, bashPolicyDecisionAllow)
	}
	return nil
}

func normalizeBashApprovalChoice(choice string) string {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "允许", "同意", "批准", "yes", "y", "ok", "approve", strings.ToLower(bashApprovalAllowOnce):
		return bashApprovalAllowOnce
	case "拒绝", "不同意", "否", "不", "no", "n", "reject", "deny", strings.ToLower(bashApprovalReject):
		return bashApprovalReject
	default:
		return strings.TrimSpace(choice)
	}
}

func bashCommandPatterns(command string) bashCommandPattern {
	exact := normalizeBashCommand(command)
	fields := strings.Fields(exact)
	out := bashCommandPattern{Exact: exact, Compound: bashHasShellControl(exact)}
	if len(fields) > 0 {
		out.Main = fields[0]
		out.Subcommand = fields[0]
	}
	if len(fields) > 1 {
		out.Subcommand = fields[0] + " " + fields[1]
	}
	return out
}

func normalizeBashCommand(command string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
}

func bashHasShellControl(command string) bool {
	for _, token := range []string{"&&", "||", ";", "|", ">", "<", "$(", "`"} {
		if strings.Contains(command, token) {
			return true
		}
	}
	return false
}

func bashRiskyMainCommand(main string) bool {
	switch strings.ToLower(strings.TrimSpace(main)) {
	case "rm", "mv", "cp", "chmod", "chown", "sudo", "su", "curl", "wget", "ssh", "scp", "rsync", "dd", "mkfs", "docker", "kubectl", "flyctl", "git", "npm", "pnpm", "yarn":
		return true
	default:
		return false
	}
}

func bashRiskySubcommand(sub string) bool {
	switch strings.ToLower(strings.TrimSpace(sub)) {
	case "git push", "git reset", "git clean", "git checkout", "git restore", "npm publish", "pnpm publish", "yarn publish", "docker run", "docker rm", "docker rmi", "kubectl delete", "flyctl deploy":
		return true
	default:
		return false
	}
}

func bashPersistentDecision(policyPath string, pattern bashCommandPattern, decision string) bool {
	policy, err := loadBashPolicy(policyPath)
	if err != nil {
		return false
	}
	var rules []bashPolicyRule
	if decision == bashPolicyDecisionDeny {
		rules = policy.Deny
	} else {
		rules = policy.Allow
	}
	for _, rule := range rules {
		if rule.Decision != "" && rule.Decision != decision {
			continue
		}
		if bashPolicyRuleMatches(rule, pattern) {
			return true
		}
	}
	return false
}

func bashPolicyRuleMatches(rule bashPolicyRule, pattern bashCommandPattern) bool {
	switch rule.Match {
	case bashPolicyMatchExact:
		return rule.Pattern == pattern.Exact
	case bashPolicyMatchSubcommand:
		return rule.Pattern != "" && rule.Pattern == pattern.Subcommand
	case bashPolicyMatchMain:
		return rule.Pattern != "" && rule.Pattern == pattern.Main
	default:
		return false
	}
}

func appendBashPolicyRule(policyPath, match, pattern, decision string) error {
	if strings.TrimSpace(policyPath) == "" || strings.TrimSpace(pattern) == "" {
		return nil
	}
	policy, err := loadBashPolicy(policyPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if policy.Version == 0 {
		policy.Version = 1
	}
	rule := bashPolicyRule{
		Match:     match,
		Pattern:   pattern,
		Decision:  decision,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	target := &policy.Allow
	if decision == bashPolicyDecisionDeny {
		target = &policy.Deny
	}
	for _, existing := range *target {
		if existing.Match == rule.Match && existing.Pattern == rule.Pattern {
			return nil
		}
	}
	*target = append(*target, rule)
	return saveBashPolicy(policyPath, policy)
}

func loadBashPolicy(policyPath string) (bashPolicyFile, error) {
	if strings.TrimSpace(policyPath) == "" {
		return bashPolicyFile{}, os.ErrNotExist
	}
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return bashPolicyFile{}, err
	}
	var policy bashPolicyFile
	if err := json.Unmarshal(data, &policy); err != nil {
		return bashPolicyFile{}, err
	}
	return policy, nil
}

func saveBashPolicy(policyPath string, policy bashPolicyFile) error {
	if err := os.MkdirAll(filepath.Dir(policyPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(policyPath, append(data, '\n'), 0600)
}
