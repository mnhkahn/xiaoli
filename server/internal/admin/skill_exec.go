package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultSkillExecTimeout        = 8 * time.Second
	defaultSkillExecMaxOutputBytes = 256 * 1024
)

type skillExecConfig struct {
	Timeout        time.Duration
	MaxOutputBytes int64
	GlobalBinDirs  []string
}

type skillToolArgs struct {
	Skill string   `json:"skill"`
	Argv  []string `json:"argv,omitempty"`
	Cmd   string   `json:"cmd,omitempty"`
}

func buildSkillToolDescription(ctx context.Context, skills []einoskill.FrontMatter) string {
	lines := make([]string, 0, len(skills)+16)
	lines = append(lines,
		"skill 工具用于加载和执行本地技能（Skills）。技能提供针对特定任务的专用指令和工作流。",
		"",
		"## 调用方式",
		"",
		"  1. 加载技能说明：{\"skill\":\"<技能名称>\"}",
		"     返回该技能的 SKILL.md 内容和关联文件列表。可用技能见下方列表。",
		"",
		"  2. 执行技能命令：{\"skill\":\"<技能名称>\",\"argv\":[\"可执行文件\",\"参数...\"]}",
		"     或 {\"skill\":\"<技能名称>\",\"cmd\":\"可执行文件 参数...\"}",
		"     argv 优先于 cmd。命令不使用 shell 解析，shell 操作符（| & ; > < $ `）被拒绝。",
		"     不确定命令格式时先调方式 1 查看说明。",
		"",
		"## 可用技能列表",
		"<available_skills>",
	)
	for _, sk := range skills {
		lines = append(lines, fmt.Sprintf("  <skill name=\"%s\">%s</skill>", sk.Name, sk.Description))
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

func buildSkillToolParams(ctx context.Context, defaults map[string]*schema.ParameterInfo) (map[string]*schema.ParameterInfo, error) {
	params := make(map[string]*schema.ParameterInfo, len(defaults)+2)
	for key, value := range defaults {
		params[key] = value
	}
	params["argv"] = &schema.ParameterInfo{
		Type: schema.Array,
		Desc: "Optional command argv to execute for the loaded skill. Prefer this over cmd. Example: [\"cyeam\",\"tv\",\"today\",\"--json\"].",
		ElemInfo: &schema.ParameterInfo{
			Type: schema.String,
		},
	}
	params["cmd"] = &schema.ParameterInfo{
		Type: schema.String,
		Desc: "Optional command line to execute for the loaded skill. It is parsed without a shell; shell operators are rejected.",
	}
	return params, nil
}

func newSkillContentBuilder(cfg skillExecConfig) func(context.Context, einoskill.Skill, string) (string, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultSkillExecTimeout
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = defaultSkillExecMaxOutputBytes
	}
	cfg.GlobalBinDirs = cleanSkillGlobalBinDirs(cfg.GlobalBinDirs)
	return func(ctx context.Context, skill einoskill.Skill, rawArgs string) (string, error) {
		var args skillToolArgs
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return "", fmt.Errorf("parse skill tool arguments: %w", err)
		}
		argv, err := skillExecArgv(args)
		if err != nil {
			return "", err
		}
		if len(argv) == 0 {
			return defaultSkillToolContent(skill), nil
		}
		return runSkillCommand(ctx, skill, argv, cfg)
	}
}

func skillExecArgv(args skillToolArgs) ([]string, error) {
	if len(args.Argv) > 0 {
		argv := make([]string, 0, len(args.Argv))
		for _, item := range args.Argv {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, fmt.Errorf("skill command argv contains empty item")
			}
			argv = append(argv, item)
		}
		return argv, nil
	}
	cmd := strings.TrimSpace(args.Cmd)
	if cmd == "" {
		return nil, nil
	}
	return splitSkillCommandLine(cmd)
}

func runSkillCommand(ctx context.Context, skill einoskill.Skill, argv []string, cfg skillExecConfig) (string, error) {
	bin, err := resolveSkillExecutable(skill.BaseDirectory, argv[0], cfg.GlobalBinDirs)
	if err != nil {
		return "", fmt.Errorf("resolve skill executable: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, argv[1:]...)
	cmd.Dir = skill.BaseDirectory

	var stdout, stderr limitedBuffer
	stdout.limit = cfg.MaxOutputBytes
	stderr.limit = cfg.MaxOutputBytes / 4
	if stderr.limit <= 0 {
		stderr.limit = 1024
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("skill command timed out after %s", cfg.Timeout)
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("exit code %d", exitErr.ExitCode())
		}
		return formatSkillCommandResult(argv, stdout.String(), stderr.String(), err), nil
	}
	return formatSkillCommandResult(argv, stdout.String(), stderr.String(), nil), nil
}

func defaultSkillToolContent(skill einoskill.Skill) string {
	return fmt.Sprintf("<skill_content name=\"%s\">\n# Skill: %s\n\n技能目录：%s\n\n%s\n\n此目录下的文件（如 scripts/、reference/ 等）中的相对路径均相对于此技能目录。\n</skill_content>", skill.Name, skill.Name, skill.BaseDirectory, skill.Content)
}

func formatSkillCommandResult(argv []string, stdout, stderr string, err error) string {
	var b strings.Builder
	if err != nil {
		fmt.Fprintf(&b, "Skill command %s failed: %v\n", strings.Join(argv, " "), err)
	} else {
		fmt.Fprintf(&b, "Skill command %s completed\n", strings.Join(argv, " "))
	}
	if stdout != "" {
		b.WriteString("\nstdout:\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteByte('\n')
		}
	}
	if stderr != "" {
		b.WriteString("\nstderr:\n")
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func resolveSkillExecutable(skillDir, name string, globalBinDirs []string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("skill command executable is empty")
	}
	if filepath.IsAbs(name) || strings.ContainsRune(name, filepath.Separator) {
		path, err := filepath.Abs(name)
		if err != nil {
			return "", fmt.Errorf("resolve skill command %q: %w", name, err)
		}
		if isPathUnder(path, skillDir) || isPathUnderAny(path, globalBinDirs) {
			if isExecutable(path) {
				return path, nil
			}
		}
		return "", fmt.Errorf("skill command %q is not an allowed executable", name)
	}

	for _, candidate := range []string{
		filepath.Join(skillDir, "bin", name),
		filepath.Join(skillDir, name),
		filepath.Join(skillDir, "scripts", name),
	} {
		if isExecutable(candidate) {
			return candidate, nil
		}
	}
	for _, dir := range globalBinDirs {
		candidate := filepath.Join(dir, name)
		if isExecutable(candidate) {
			return candidate, nil
		}
	}

	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("skill command %q not found", name)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve skill command %q: %w", name, err)
	}
	if !isPathUnderAny(abs, globalBinDirs) {
		return "", fmt.Errorf("skill command %q resolves to %s outside allowed global bin dirs", name, abs)
	}
	return abs, nil
}

func cleanSkillGlobalBinDirs(dirs []string) []string {
	if len(dirs) == 0 {
		dirs = []string{"/usr/local/bin"}
	}
	out := make([]string, 0, len(dirs))
	seen := map[string]bool{}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if !seen[abs] {
			out = append(out, abs)
			seen[abs] = true
		}
	}
	return out
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func isPathUnderAny(path string, roots []string) bool {
	for _, root := range roots {
		if isPathUnder(path, root) {
			return true
		}
	}
	return false
}

func isPathUnder(path, root string) bool {
	if root == "" {
		return false
	}
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func splitSkillCommandLine(cmd string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false
	haveToken := false

	for _, r := range cmd {
		if escaped {
			current.WriteRune(r)
			haveToken = true
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			haveToken = true
			continue
		}
		if quote == 0 && containsShellRune(r) {
			return nil, fmt.Errorf("skill command contains unsupported shell operator %q", r)
		}
		if quote == 0 && (r == '\'' || r == '"') {
			quote = r
			haveToken = true
			continue
		}
		if quote != 0 && r == quote {
			quote = 0
			continue
		}
		if quote == 0 && (r == ' ' || r == '\t' || r == '\n' || r == '\r') {
			if haveToken {
				args = append(args, current.String())
				current.Reset()
				haveToken = false
			}
			continue
		}
		current.WriteRune(r)
		haveToken = true
	}
	if escaped {
		return nil, fmt.Errorf("skill command has trailing escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("skill command has unterminated quote")
	}
	if haveToken {
		args = append(args, current.String())
	}
	return args, nil
}

func containsShellRune(r rune) bool {
	switch r {
	case '|', '&', ';', '>', '<', '$', '`', '(', ')':
		return true
	default:
		return false
	}
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
