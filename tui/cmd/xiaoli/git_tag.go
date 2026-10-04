package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mnhkahn/xiaoli/internal/agent/slash"
)

const gitTagUsage = "命令：/tag s 小版本 · /tag m 中版本 · /tag l 大版本"

var stableGitTag = regexp.MustCompile(`^([vV]?)(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type gitTagVersion struct {
	numbers [3]uint64
	prefix  string
}

func (v gitTagVersion) String() string {
	return fmt.Sprintf("%s%d.%d.%d", v.prefix, v.numbers[0], v.numbers[1], v.numbers[2])
}
func highestGitTag(refs string) (gitTagVersion, error) {
	// New repositories retain the default v prefix. Existing tags keep their spelling.
	best := gitTagVersion{prefix: "v"}
	found := false
	for _, line := range strings.Fields(refs) {
		match := stableGitTag.FindStringSubmatch(strings.TrimPrefix(line, "refs/tags/"))
		if match == nil {
			continue
		}
		v := gitTagVersion{prefix: match[1]}
		for i := range v.numbers {
			n, err := strconv.ParseUint(match[i+2], 10, 64)
			if err != nil {
				return best, fmt.Errorf("版本号超出范围：%s", line)
			}
			v.numbers[i] = n
		}
		greater := false
		for i := range v.numbers {
			if v.numbers[i] != best.numbers[i] {
				greater = v.numbers[i] > best.numbers[i]
				break
			}
		}
		// Equal versions prefer v, then V, then no prefix, regardless of ref order.
		if !found || greater || (v.numbers == best.numbers && v.prefix > best.prefix) {
			best = v
		}
		found = true
	}
	return best, nil
}
func nextGitTag(v gitTagVersion, size string) (string, error) {
	index := 2
	switch size {
	case "s":
	case "m":
		index = 1
	case "l":
		index = 0
	default:
		return "", fmt.Errorf("%s", gitTagUsage)
	}
	if v.numbers[index] == ^uint64(0) {
		return "", fmt.Errorf("版本号超出范围")
	}
	v.numbers[index]++
	for i := index + 1; i < len(v.numbers); i++ {
		v.numbers[i] = 0
	}
	return v.String(), nil
}

type gitTagPlan struct {
	cwd, commit, branch, summary, remote, pushURL, target, size string
	previous                                                    gitTagVersion
}
type gitTagPending struct {
	stage string
	plan  gitTagPlan
}
type gitTagPreparedMsg struct {
	plan gitTagPlan
	err  error
}
type gitTagDoneMsg struct {
	plan          gitTagPlan
	created, push bool
	err           error
}

func tagGit(ctx context.Context, cwd string, args ...string) (string, error) {
	out, err := runGitCombinedContext(ctx, cwd, args...)
	if err != nil {
		return "", fmt.Errorf("git %s：%w\n%s", strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}
func requireCleanTagWorktree(ctx context.Context, cwd string) error {
	out, err := tagGit(ctx, cwd, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if out != "" {
		return fmt.Errorf("工作区有未提交变更，请先提交后再运行 /tag")
	}
	return nil
}
func prepareGitTag(ctx context.Context, cwd, size string) (p gitTagPlan, err error) {
	p.size = size
	p.cwd, err = tagGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	if err = requireCleanTagWorktree(ctx, p.cwd); err != nil {
		return
	}
	p.commit, err = tagGit(ctx, p.cwd, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return
	}
	p.branch, err = tagGit(ctx, p.cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return
	}
	p.summary, err = tagGit(ctx, p.cwd, "show", "-s", "--format=%h %s", p.commit)
	if err != nil {
		return
	}
	err = readGitTagVersion(ctx, &p)
	if err == nil && size != "" {
		p.target, err = nextGitTag(p.previous, size)
	}
	return
}

// Share the version source with input previews, without requiring a clean worktree.
func readGitTagVersion(ctx context.Context, p *gitTagPlan) error {
	refs, err := tagGit(ctx, p.cwd, "tag", "--list")
	if err != nil {
		return err
	}
	remotes, err := tagGit(ctx, p.cwd, "remote")
	if err != nil {
		return err
	}
	names := strings.Fields(remotes)
	for _, name := range names {
		if name == "origin" {
			p.remote = name
		}
	}
	if p.remote == "" && len(names) == 1 {
		p.remote = names[0]
	}
	if p.remote == "" && len(names) > 1 {
		return fmt.Errorf("存在多个远端且没有 origin，无法确定标签推送目标")
	}
	if p.remote != "" {
		// Read the push destination, which can differ from the fetch URL.
		urls, e := tagGit(ctx, p.cwd, "remote", "get-url", "--push", "--all", p.remote)
		if e != nil {
			return e
		}
		if len(strings.Split(urls, "\n")) != 1 {
			return fmt.Errorf("远端有多个推送地址，无法确定标签推送目标")
		}
		p.pushURL = urls
		remoteRefs, e := tagGit(ctx, p.cwd, "ls-remote", "--tags", "--refs", urls)
		if e != nil {
			return fmt.Errorf("远端版本信息未核实，请检查连接后重试：%w", e)
		}
		refs += "\n" + remoteRefs
	}
	p.previous, err = highestGitTag(refs)
	return err
}

func executeGitTag(ctx context.Context, p gitTagPlan, push, retry bool) gitTagDoneMsg {
	result := gitTagDoneMsg{plan: p, push: push, created: retry}
	if !retry {
		if result.err = requireCleanTagWorktree(ctx, p.cwd); result.err != nil {
			return result
		}
		head, err := tagGit(ctx, p.cwd, "rev-parse", "HEAD")
		if err != nil {
			result.err = err
			return result
		}
		if head != p.commit {
			result.err = fmt.Errorf("HEAD 已变化，请重新运行 /tag 预览版本")
			return result
		}
		_, result.err = tagGit(ctx, p.cwd, "tag", "-a", p.target, p.commit, "-m", "Release "+p.target)
		if result.err != nil {
			return result
		}
		result.created = true
	}
	if push {
		urls, err := tagGit(ctx, p.cwd, "remote", "get-url", "--push", "--all", p.remote)
		if err != nil {
			result.err = err
			return result
		}
		if urls != p.pushURL {
			result.err = fmt.Errorf("远端推送地址已变化，停止推送")
			return result
		}
		commit, err := tagGit(ctx, p.cwd, "rev-parse", "refs/tags/"+p.target+"^{commit}")
		if err != nil {
			result.err = err
			return result
		}
		if commit != p.commit {
			result.err = fmt.Errorf("本地标签已变化，停止推送")
			return result
		}
		_, result.err = tagGit(ctx, p.cwd, "push", p.remote, "refs/tags/"+p.target+":refs/tags/"+p.target)
	}
	return result
}

func (m *model) clearGitTagChoice() {
	m.pendingGitTag = gitTagPending{}
	m.pendingQuestion = ""
	m.pendingOptions = nil
	m.pendingChoice = 0
	m.input.SetValue("")
}
func (m *model) showGitTagPlan(p gitTagPlan) {
	m.pendingGitTag = gitTagPending{stage: "confirm", plan: p}
	m.pendingQuestion = fmt.Sprintf("创建版本标签\n\n分支：%s\n目标提交：%s\n版本：%s → %s", p.branch, p.summary, p.previous, p.target)
	m.pendingOptions = []string{"仅创建本地标签", "取消操作"}
	if p.remote != "" {
		m.pendingQuestion += "\n推送目标：" + p.remote + "\n推送标签可能触发仓库的 Release 工作流。"
		m.pendingOptions = []string{"创建并推送", "仅创建本地标签", "取消操作"}
	}
	m.pendingChoice = 0
}
func (m *model) handleGitTagInput(text string) (bool, tea.Cmd) {
	cmd, isSlash := slash.Parse(text)
	isTag := isSlash && cmd.Name == "tag"
	if !isTag && m.pendingGitTag.stage == "" {
		return false, nil
	}
	// A fresh /tag command replaces a previous tag selection or retry.
	if isTag {
		size := strings.TrimSpace(cmd.Args)
		m.clearGitTagChoice()
		if size == "" {
			m.items = append(m.items, transcriptItem{role: "system", text: gitTagUsage})
			m.syncViewport(true)
		}
		if size != "" && size != "s" && size != "m" && size != "l" {
			m.items = append(m.items, transcriptItem{role: "system", text: gitTagUsage})
			m.syncViewport(true)
			return true, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		m.activeCancel = cancel
		m.chatCanceled = false
		m.busy = true
		m.status = "tag"
		cwd := m.cwd
		return true, func() tea.Msg { p, err := prepareGitTag(ctx, cwd, size); return gitTagPreparedMsg{p, err} }
	}
	p := m.pendingGitTag.plan
	choice := strings.TrimSpace(text)
	m.input.SetValue("")
	if choice == "取消操作" || isReject(choice) {
		m.clearGitTagChoice()
		m.items = append(m.items, transcriptItem{role: "system", text: "已结束标签操作。"})
	} else if m.pendingGitTag.stage == "select" {
		size := strings.TrimPrefix(choice, "/tag ")
		target, err := nextGitTag(p.previous, size)
		if err != nil {
			m.items = append(m.items, transcriptItem{role: "system", text: gitTagUsage})
		} else {
			p.size, p.target = size, target
			m.showGitTagPlan(p)
		}
	} else {
		retry := m.pendingGitTag.stage == "retry"
		push := choice == "创建并推送" || choice == "重试推送"
		if (retry && choice == "重试推送") || (!retry && (choice == "仅创建本地标签" || choice == "创建并推送")) {
			if push && p.remote == "" {
				return true, nil
			}
			m.clearGitTagChoice()
			m.busy = true
			m.status = "tag"
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			m.activeCancel = cancel
			m.chatCanceled = false
			return true, func() tea.Msg { return executeGitTag(ctx, p, push, retry) }
		}
	}
	m.syncViewport(true)
	return true, nil
}
func (m *model) finishGitTagRun() {
	if m.activeCancel != nil {
		m.activeCancel()
		m.activeCancel = nil
	}
	m.busy = false
	m.status = "idle"
	m.chatCanceled = false
}
func (m *model) handleGitTagPrepared(msg gitTagPreparedMsg) {
	m.finishGitTagRun()
	if msg.err != nil {
		m.items = append(m.items, transcriptItem{role: "error", text: msg.err.Error()})
	} else if msg.plan.size != "" {
		m.showGitTagPlan(msg.plan)
	} else {
		m.pendingGitTag = gitTagPending{stage: "select", plan: msg.plan}
		m.pendingQuestion = "请选择升级版本\n\n" + gitTagUsage + "\n\n当前版本：" + msg.plan.previous.String()
		m.pendingOptions = nil
		for i, size := range []string{"s", "m", "l"} {
			target, err := nextGitTag(msg.plan.previous, size)
			if err == nil {
				m.pendingOptions = append(m.pendingOptions, fmt.Sprintf("%s :: %s版本 %s → %s（/tag %s）", size, []string{"小", "中", "大"}[i], msg.plan.previous, target, size))
			}
		}
		m.pendingOptions = append(m.pendingOptions, "取消操作")
		m.pendingChoice = 0
	}
	m.syncViewport(true)
}
func (m *model) handleGitTagDone(msg gitTagDoneMsg) {
	m.finishGitTagRun()
	text := "已创建本地标签 " + msg.plan.target
	if msg.err != nil {
		text = msg.err.Error()
		if msg.created && msg.push {
			text = "本地标签 " + msg.plan.target + " 已保留，推送未完成。\n" + text
			m.pendingGitTag = gitTagPending{stage: "retry", plan: msg.plan}
			m.pendingQuestion = text
			m.pendingOptions = []string{"重试推送", "取消操作"}
			m.pendingChoice = 0
		}
		m.items = append(m.items, transcriptItem{role: "error", text: text})
	} else {
		if msg.push {
			text += "，已推送到 " + msg.plan.remote
		}
		m.items = append(m.items, transcriptItem{role: "system", text: text})
	}
	m.syncViewport(true)
}

func (m model) gitTagSuggestions(value string) []slashSuggestion {
	if !strings.HasPrefix(value, "/tag ") {
		return nil
	}
	var out []slashSuggestion
	for i, size := range []string{"s", "m", "l"} {
		name := "tag " + size
		if strings.HasPrefix(name, strings.TrimPrefix(value, "/")) {
			description := "正在读取版本…"
			preview := m.gitTagPreview
			if preview.active && preview.cwd == m.cwd && !preview.loading {
				if preview.err != nil {
					description = "版本读取失败：" + strings.Join(strings.Fields(preview.err.Error()), " ")
				} else if target, err := nextGitTag(preview.version, size); err != nil {
					description = err.Error()
				} else {
					description = fmt.Sprintf("%s → %s", preview.version, target)
				}
			}
			out = append(out, slashSuggestion{Name: name, Description: []string{"小", "中", "大"}[i] + "版本：" + description, Kind: "tui"})
		}
	}
	return out
}

// Each input session gets its own request ID so late results cannot overwrite a
// newer preview (including after /cd or leaving and re-entering /tag).
type gitTagPreview struct {
	cwd             string
	id              uint64
	active, loading bool
	version         gitTagVersion
	err             error
	cancel          context.CancelFunc
}

type gitTagPreviewMsg struct {
	cwd     string
	id      uint64
	version gitTagVersion
	err     error
}

func (m *model) syncGitTagPreview() tea.Cmd {
	value := strings.TrimLeft(m.input.Value(), " ")
	wanted := !m.busy && !m.quitting && m.pendingGitTag.stage == "" &&
		(value == "/tag" || strings.HasPrefix(value, "/tag "))
	p := &m.gitTagPreview
	if p.active && (!wanted || p.cwd != m.cwd) {
		if p.cancel != nil {
			p.cancel()
		}
		*p = gitTagPreview{id: p.id + 1}
	}
	if !wanted || p.active {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	*p = gitTagPreview{cwd: m.cwd, id: p.id + 1, active: true, loading: true, cancel: cancel}
	cwd, id := p.cwd, p.id
	return func() tea.Msg {
		defer cancel()
		plan := gitTagPlan{cwd: cwd}
		err := readGitTagVersion(ctx, &plan)
		return gitTagPreviewMsg{cwd: cwd, id: id, version: plan.previous, err: err}
	}
}

func (m *model) handleGitTagPreview(msg gitTagPreviewMsg) {
	p := &m.gitTagPreview
	if !p.active || p.id != msg.id || p.cwd != msg.cwd || m.cwd != msg.cwd {
		return
	}
	p.loading, p.version, p.err, p.cancel = false, msg.version, msg.err, nil
}
