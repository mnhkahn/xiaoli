package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/mnhkahn/gogogo/logger"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/schema"
)

const (
	DefaultExecTimeout        = 120 * time.Second
	MaxExecTimeout            = 600 * time.Second
	DefaultExecMaxOutputBytes = 256 * 1024
)

type ExecConfig struct {
	Timeout        time.Duration
	MaxOutputBytes int64
	GlobalBinDirs  []string
}

type ToolArgs struct {
	Skill string   `json:"skill"`
	Argv  []string `json:"argv,omitempty"`
	Cmd   string   `json:"cmd,omitempty"`
}

func BuildToolDescription(ctx context.Context, skills []einoskill.FrontMatter) string {
	lines := make([]string, 0, len(skills)+16)
	lines = append(lines,
		"Load a specialized skill for a specific task. Skills provide domain-specific instructions and workflows.",
		"",
		"Two calling modes:",
		"  1. {\"skill\":\"<name>\"} — load skill instructions only",
		`  2. {\"skill\":\"<name>\",\"argv\":[\"<binary>\",\"<arg1>\",\"...\"]} — execute the skill's CLI binary`,
		"",
		"IMPORTANT: argv MUST start with the actual binary name (e.g., cyeam or lark-cli),",
		"NOT a subcommand. Example: [\"cyeam\",\"tv\",\"list\"] is correct; [\"tv\",\"list\"] is wrong.",
		"When unsure, use mode 1 first to read the skill's instructions for the correct command format.",
		"",
		"Available skills:",
		"<available_skills>",
	)
	for _, sk := range skills {
		lines = append(lines, fmt.Sprintf("  <skill name=\"%s\">%s</skill>", sk.Name, sk.Description))
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

func BuildToolParams(ctx context.Context, defaults map[string]*schema.ParameterInfo) (map[string]*schema.ParameterInfo, error) {
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

func NewContentBuilder(cfg ExecConfig) func(context.Context, einoskill.Skill, string) (string, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultExecTimeout
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = DefaultExecMaxOutputBytes
	}
	cfg.GlobalBinDirs = cleanSkillGlobalBinDirs(cfg.GlobalBinDirs)
	return func(ctx context.Context, skill einoskill.Skill, rawArgs string) (string, error) {
		var args ToolArgs
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return formatSkillCommandResult(nil, "", "", fmt.Errorf("parse skill tool arguments: %w", err)), nil
		}
		argv, err := skillExecArgv(args)
		if err != nil {
			return formatSkillCommandResult(nil, "", "", err), nil
		}
		if len(argv) == 0 {
			logger.Infof("[skill] load instructions skill=%s", skill.Name)
			return defaultSkillToolContent(skill), nil
		}
		logger.Infof("[skill] exec start skill=%s argv=%v", skill.Name, argv)
		start := time.Now()
		result, err := runSkillCommand(ctx, skill, argv, cfg)
		elapsed := time.Since(start)
		logger.Infof("[skill] exec done skill=%s argv=%v elapsed=%v err=%v", skill.Name, argv, elapsed, err)
		return result, err
	}
}

func skillExecArgv(args ToolArgs) ([]string, error) {
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

func runSkillCommand(ctx context.Context, skill einoskill.Skill, argv []string, cfg ExecConfig) (string, error) {
	bin, err := resolveSkillExecutable(skill.BaseDirectory, argv[0], cfg.GlobalBinDirs)
	if err != nil {
		return formatSkillCommandResult(argv, "", "", fmt.Errorf("resolve skill executable: %w", err)), nil
	}

	// 交互式命令（如 cyeam login）会先打印关键信息（登录链接/验证码）再长时间阻塞等待，
	// 用早返回模式：拿到首屏输出后即返回给调用方，进程在后台继续完成。
	if isEarlyReturnCommand(argv) {
		return runSkillCommandEarlyReturn(ctx, skill, bin, argv, cfg), nil
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
		return formatSkillCommandResult(argv, stdout.String(), stderr.String(), fmt.Errorf("skill command timed out after %s", cfg.Timeout)), nil
	}
	if runCtx.Err() != nil {
		return "", runCtx.Err()
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("exit code %d", exitErr.ExitCode())
		}
		return formatSkillCommandResult(argv, stdout.String(), stderr.String(), err), nil
	}
	return formatSkillCommandResult(argv, stdout.String(), stderr.String(), nil), nil
}

// isEarlyReturnCommand 判断命令是否需要早返回（先输出后长时间阻塞）。
// 目前仅 cyeam login：打印登录链接+验证码后会轮询等待用户授权，
// 阻塞读取会导致链接迟迟返不回调用方，验证码已过期。
func isEarlyReturnCommand(argv []string) bool {
	for i, a := range argv {
		if a == "login" && i > 0 {
			return true
		}
	}
	return false
}

// runSkillCommandEarlyReturn 启动命令并在拿到首屏 stdout 后尽快返回，
// 进程在后台继续运行（如 login 的轮询取 token），由独立超时兜底。
func runSkillCommandEarlyReturn(ctx context.Context, skill einoskill.Skill, bin string, argv []string, cfg ExecConfig) string {
	// 后台进程脱离调用方 ctx，用自身超时（不超过 MaxExecTimeout），避免被调用方取消而中断轮询。
	bgTimeout := cfg.Timeout
	if bgTimeout <= 0 || bgTimeout > MaxExecTimeout {
		bgTimeout = MaxExecTimeout
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bgTimeout)

	cmd := exec.CommandContext(runCtx, bin, argv[1:]...)
	cmd.Dir = skill.BaseDirectory

	stdout := &syncBuffer{limit: cfg.MaxOutputBytes}
	stderr := &syncBuffer{limit: cfg.MaxOutputBytes / 4}
	if stderr.limit <= 0 {
		stderr.limit = 1024
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return formatSkillCommandResult(argv, "", "", fmt.Errorf("start skill command: %w", err))
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		cancel()
		close(done)
	}()

	// 等待：命令很快结束（取已有输出），或首屏输出出现后给 1.5s 宽限期收集完整链接，再返回。
	const grace = 1500 * time.Millisecond
	deadline := time.NewTimer(8 * time.Second) // 最多等 8s 仍无输出则带提示返回
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var sawOutput bool
	var graceTimer <-chan time.Time
	for {
		select {
		case <-done:
			return formatSkillCommandResult(argv, stdout.String(), stderr.String(), nil)
		case <-graceTimer:
			// 首屏输出已稳定，命令仍在后台运行（轮询中），返回当前输出
			return earlyReturnResult(argv, stdout.String())
		case <-deadline.C:
			return earlyReturnResult(argv, stdout.String())
		case <-ticker.C:
			if !sawOutput && stdout.Len() > 0 {
				sawOutput = true
				t := time.NewTimer(grace)
				graceTimer = t.C
			}
		}
	}
}

func earlyReturnResult(argv []string, stdout string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Skill command %s started (running in background)\n", strings.Join(argv, " "))
	if stdout != "" {
		b.WriteString("\nstdout:\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteByte('\n')
		}
	}
	b.WriteString("\n请按上面的链接和验证码完成授权，授权后稍等片刻即可生效。\n")
	return b.String()
}

func defaultSkillToolContent(skill einoskill.Skill) string {
	return fmt.Sprintf("<skill_content name=\"%s\">\n# Skill: %s\n\n技能目录：%s\n\n%s\n\n此目录下的文件（如 scripts/、reference/ 等）中的相对路径均相对于此技能目录。\n</skill_content>", skill.Name, skill.Name, skill.BaseDirectory, skill.Content)
}

func formatSkillCommandResult(argv []string, stdout, stderr string, err error) string {
	var b strings.Builder
	command := strings.Join(argv, " ")
	if command == "" {
		command = "<invalid>"
	}
	if err != nil {
		fmt.Fprintf(&b, "Skill command %s failed: %v\n", command, err)
	} else {
		fmt.Fprintf(&b, "Skill command %s completed\n", command)
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

// syncBuffer 是并发安全的 limitedBuffer，供早返回模式下后台进程写、主流程读。
type syncBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int64
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := int(b.limit) - b.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}
