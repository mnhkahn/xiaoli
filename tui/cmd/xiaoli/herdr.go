package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// herdr 集成：在 herdr pane 里运行时向 herdr 上报 agent 状态。
// 仅当 HERDR_ENV=1 且 HERDR_PANE_ID / HERDR_BIN_PATH 非空时启用，
// 未启用时所有 hook 都是 nil 接收者的廉价 no-op。

const (
	herdrSource  = "xiaoli"
	herdrAgent   = "xiaoli"
	herdrTimeout = 2 * time.Second
)

type herdrState string

const (
	herdrStateIdle    herdrState = "idle"
	herdrStateWorking herdrState = "working"
	herdrStateBlocked herdrState = "blocked"
)

// herdrSnapshot 是从 model 提取的、与 herdr 上报相关的最小状态。
// 可比较，用于在 Update 前后做 diff。
type herdrSnapshot struct {
	state     herdrState
	message   string
	sessionID string
}

type herdrExecFunc func(ctx context.Context, name string, args ...string) error

type herdrReporter struct {
	binPath string
	paneID  string
	execFn  herdrExecFunc

	wake chan struct{}

	mu      sync.Mutex
	pending *herdrSnapshot
	closed  bool

	sendMu sync.Mutex // 串行化 exec 调用，release 借此等待在途上报完成
	// 仅在持有 sendMu 时访问，记录已上报过恢复命令的会话。
	lastSessionID string
	lastSeq       atomic.Int64
}

// newHerdrReporterFromEnv 在非 herdr 环境返回 nil。
func newHerdrReporterFromEnv() *herdrReporter {
	if os.Getenv("HERDR_ENV") != "1" {
		return nil
	}
	paneID := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	binPath := strings.TrimSpace(os.Getenv("HERDR_BIN_PATH"))
	if paneID == "" || binPath == "" {
		return nil
	}
	r := &herdrReporter{
		binPath: binPath,
		paneID:  paneID,
		execFn:  defaultHerdrExec,
		wake:    make(chan struct{}, 1),
	}
	go r.loop()
	return r
}

func defaultHerdrExec(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

// submit 只保留最新的待上报状态；一条上报在途时旧的 pending 被合并丢弃。
func (r *herdrReporter) submit(snap herdrSnapshot) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.pending = &snap
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *herdrReporter) loop() {
	for range r.wake {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return
		}
		snap := r.pending
		r.pending = nil
		r.mu.Unlock()
		if snap == nil {
			continue
		}
		r.send(*snap)
	}
}

func (r *herdrReporter) send(snap herdrSnapshot) {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return
	}
	args := []string{
		"pane", "report-agent", r.paneID,
		"--source", herdrSource,
		"--agent", herdrAgent,
		"--state", string(snap.state),
	}
	if snap.state == herdrStateBlocked && snap.message != "" {
		args = append(args, "--message", snap.message)
	}
	args = append(args, "--seq", strconv.FormatInt(r.nextSeq(), 10))
	if snap.sessionID != "" && snap.sessionID != r.lastSessionID {
		args = append(args, "--agent-session-id", snap.sessionID, "--", "xiaoli", "-s", snap.sessionID)
		r.lastSessionID = snap.sessionID
	}
	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	_ = r.execFn(ctx, r.binPath, args...)
}

// release 在退出时同步调用，确保 release-agent 在进程结束前发出。
func (r *herdrReporter) release() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.pending = nil
	r.mu.Unlock()
	// 唤醒 worker 使其退出。
	select {
	case r.wake <- struct{}{}:
	default:
	}
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	_ = r.execFn(ctx, r.binPath,
		"pane", "release-agent", r.paneID,
		"--source", herdrSource,
		"--agent", herdrAgent,
		"--seq", strconv.FormatInt(r.nextSeq(), 10),
	)
}

// nextSeq 用 Unix 毫秒时间戳保证跨进程递增，同毫秒内单调 +1。
func (r *herdrReporter) nextSeq() int64 {
	now := time.Now().UnixMilli()
	for {
		last := r.lastSeq.Load()
		next := now
		if next <= last {
			next = last + 1
		}
		if r.lastSeq.CompareAndSwap(last, next) {
			return next
		}
	}
}

// herdrSnapshotOf 从 model 推导上报状态：
// 有待处理的批准/提问弹窗 → blocked；任务在跑 → working；否则 idle。
func herdrSnapshotOf(m model) herdrSnapshot {
	snap := herdrSnapshot{state: herdrStateIdle, sessionID: strings.TrimSpace(m.sessionID)}
	if m.pendingToolConfirm != nil || m.hasPendingOptions() || strings.TrimSpace(m.pendingQuestion) != "" {
		snap.state = herdrStateBlocked
		snap.message = herdrBlockedMessage(m)
		return snap
	}
	if m.busy || m.runPulseActive {
		snap.state = herdrStateWorking
	}
	return snap
}

func herdrBlockedMessage(m model) string {
	question := ""
	if m.pendingToolConfirm != nil {
		question = m.pendingToolConfirm.Question
	}
	if strings.TrimSpace(question) == "" {
		question = m.pendingQuestion
	}
	question = strings.Join(strings.Fields(question), " ")
	const maxRunes = 120
	runes := []rune(question)
	if len(runes) > maxRunes {
		question = string(runes[:maxRunes-1]) + "…"
	}
	return question
}

// herdrSync 在每次 Update 后对比前后快照，只有变化时才提交。
// 未启用 herdr 时（m.herdr == nil）是廉价 no-op。
func (m model) herdrSync(next model) {
	if m.herdr == nil || next.herdr == nil {
		return
	}
	if herdrSnapshotOf(m) == herdrSnapshotOf(next) {
		return
	}
	next.herdr.submit(herdrSnapshotOf(next))
}
