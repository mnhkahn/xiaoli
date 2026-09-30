package main

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
)

type herdrCallRecorder struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *herdrCallRecorder) exec(ctx context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), args...))
	return nil
}

func (r *herdrCallRecorder) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.calls...)
}

func newTestHerdrReporter(execFn herdrExecFunc) *herdrReporter {
	return &herdrReporter{
		binPath: "/fake/herdr",
		paneID:  "pane-1",
		execFn:  execFn,
		wake:    make(chan struct{}, 1),
	}
}

func TestNewHerdrReporterFromEnvDisabled(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_PANE_ID", "pane-1")
	t.Setenv("HERDR_BIN_PATH", "/fake/herdr")
	if r := newHerdrReporterFromEnv(); r != nil {
		t.Fatalf("newHerdrReporterFromEnv() = %v, want nil without HERDR_ENV=1", r)
	}

	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "")
	if r := newHerdrReporterFromEnv(); r != nil {
		t.Fatalf("newHerdrReporterFromEnv() = %v, want nil without HERDR_PANE_ID", r)
	}

	t.Setenv("HERDR_PANE_ID", "pane-1")
	t.Setenv("HERDR_BIN_PATH", "")
	if r := newHerdrReporterFromEnv(); r != nil {
		t.Fatalf("newHerdrReporterFromEnv() = %v, want nil without HERDR_BIN_PATH", r)
	}
}

func TestNewHerdrReporterFromEnvEnabled(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "pane-1")
	t.Setenv("HERDR_BIN_PATH", "/fake/herdr")
	r := newHerdrReporterFromEnv()
	if r == nil {
		t.Fatal("newHerdrReporterFromEnv() = nil, want reporter")
	}
	r.execFn = func(context.Context, string, ...string) error { return nil }
	r.release()
}

func TestHerdrReporterNilReceiverNoOp(t *testing.T) {
	var r *herdrReporter
	r.submit(herdrSnapshot{state: herdrStateWorking})
	r.release()
}

func TestHerdrNextSeqMonotonic(t *testing.T) {
	r := newTestHerdrReporter(func(context.Context, string, ...string) error { return nil })
	prev := r.nextSeq()
	for i := 0; i < 1000; i++ {
		next := r.nextSeq()
		if next <= prev {
			t.Fatalf("nextSeq() = %d, want > %d", next, prev)
		}
		prev = next
	}
}

func TestHerdrSendArgsAndSessionOnce(t *testing.T) {
	rec := &herdrCallRecorder{}
	r := newTestHerdrReporter(rec.exec)

	r.send(herdrSnapshot{state: herdrStateWorking, sessionID: "ses-1"})
	r.send(herdrSnapshot{state: herdrStateBlocked, message: "是否允许执行命令：rm -rf x", sessionID: "ses-1"})
	r.send(herdrSnapshot{state: herdrStateIdle, sessionID: "ses-2"})

	calls := rec.snapshot()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	joined := make([]string, len(calls))
	var seqs []int64
	for i, args := range calls {
		joined[i] = strings.Join(args, " ")
		if !strings.HasPrefix(joined[i], "pane report-agent pane-1 --source xiaoli --agent xiaoli --state ") {
			t.Fatalf("call %d args = %q, want report-agent prefix", i, joined[i])
		}
		seq, ok := herdrFlagValue(args, "--seq")
		if !ok {
			t.Fatalf("call %d missing --seq: %q", i, joined[i])
		}
		n, err := strconv.ParseInt(seq, 10, 64)
		if err != nil {
			t.Fatalf("call %d --seq %q not a number", i, seq)
		}
		seqs = append(seqs, n)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("seqs not strictly increasing: %v", seqs)
		}
	}
	if !strings.Contains(joined[0], "--state working") {
		t.Fatalf("call 0 = %q, want working", joined[0])
	}
	if !strings.Contains(joined[1], "--state blocked --message 是否允许执行命令：rm -rf x") {
		t.Fatalf("call 1 = %q, want blocked with message", joined[1])
	}
	// 恢复命令只在会话首次出现时附带，切换会话时重新附带。
	if !strings.Contains(joined[0], "--agent-session-id ses-1 -- xiaoli -s ses-1") {
		t.Fatalf("call 0 = %q, want resume command for ses-1", joined[0])
	}
	if strings.Contains(joined[1], "--agent-session-id") {
		t.Fatalf("call 1 = %q, want no repeated resume command for same session", joined[1])
	}
	if !strings.Contains(joined[2], "--agent-session-id ses-2 -- xiaoli -s ses-2") {
		t.Fatalf("call 2 = %q, want resume command for new session ses-2", joined[2])
	}
}

func TestHerdrReleaseArgs(t *testing.T) {
	rec := &herdrCallRecorder{}
	r := newTestHerdrReporter(rec.exec)
	r.send(herdrSnapshot{state: herdrStateIdle})
	r.release()

	calls := rec.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	got := strings.Join(calls[1], " ")
	if !strings.HasPrefix(got, "pane release-agent pane-1 --source xiaoli --agent xiaoli --seq ") {
		t.Fatalf("release args = %q", got)
	}
	seqReport := herdrMustSeq(t, calls[0])
	seqRelease := herdrMustSeq(t, calls[1])
	if seqRelease <= seqReport {
		t.Fatalf("release seq %d not greater than report seq %d", seqRelease, seqReport)
	}

	// 重复 release 不再执行。
	r.release()
	if len(rec.snapshot()) != 2 {
		t.Fatalf("calls after second release = %d, want 2", len(rec.snapshot()))
	}
}

func TestHerdrCoalescesPendingReports(t *testing.T) {
	started := make(chan struct{}, 1)
	resume := make(chan struct{})
	rec := &herdrCallRecorder{}
	execFn := func(ctx context.Context, name string, args ...string) error {
		rec.exec(ctx, name, args...)
		select {
		case started <- struct{}{}:
		default:
		}
		<-resume
		return nil
	}
	r := newTestHerdrReporter(execFn)
	go r.loop()

	r.submit(herdrSnapshot{state: herdrStateWorking})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not pick up first report")
	}
	// 第一条在途，随后两条只保留最新。
	r.submit(herdrSnapshot{state: herdrStateBlocked, message: "first"})
	r.submit(herdrSnapshot{state: herdrStateBlocked, message: "latest"})
	close(resume)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.snapshot()) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	calls := rec.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2 (in-flight + latest)", len(calls))
	}
	if got := strings.Join(calls[1], " "); !strings.Contains(got, "--message latest") {
		t.Fatalf("second call = %q, want latest pending report", got)
	}
	r.release()
}

func TestHerdrSnapshotOf(t *testing.T) {
	idle := model{sessionID: "ses-1"}
	if got := herdrSnapshotOf(idle); got.state != herdrStateIdle || got.sessionID != "ses-1" {
		t.Fatalf("herdrSnapshotOf(idle) = %+v", got)
	}

	working := model{busy: true}
	if got := herdrSnapshotOf(working); got.state != herdrStateWorking {
		t.Fatalf("herdrSnapshotOf(busy) = %+v, want working", got)
	}

	blocked := model{
		busy: true,
		pendingToolConfirm: &agentbuiltin.PendingToolUseConfirm{
			Question: "是否允许执行命令：git status",
		},
	}
	got := herdrSnapshotOf(blocked)
	if got.state != herdrStateBlocked || got.message != "是否允许执行命令：git status" {
		t.Fatalf("herdrSnapshotOf(pending confirm) = %+v, want blocked with question", got)
	}

	ask := model{pendingQuestion: "生成的提交信息是否符合要求？", pendingOptions: []string{"确认", "取消"}}
	if got := herdrSnapshotOf(ask); got.state != herdrStateBlocked || got.message != "生成的提交信息是否符合要求？" {
		t.Fatalf("herdrSnapshotOf(pending ask) = %+v, want blocked with question", got)
	}
}

func TestHerdrSyncReportsOnlyChanges(t *testing.T) {
	rec := &herdrCallRecorder{}
	r := newTestHerdrReporter(rec.exec)
	go r.loop()
	defer func() {
		r.execFn = func(context.Context, string, ...string) error { return nil }
		r.release()
	}()

	before := model{herdr: r}
	same := model{herdr: r}
	before.herdrSync(same)
	waitHerdrCalls(t, rec, 0)

	changed := model{herdr: r, busy: true}
	before.herdrSync(changed)
	waitHerdrCalls(t, rec, 1)

	// 未启用 herdr 时是 no-op。
	plain := model{}
	plain.herdrSync(model{busy: true})
}

// waitHerdrCalls 等待调用数稳定到 want；want 为 0 时用固定小延迟确认没有调用。
func waitHerdrCalls(t *testing.T, rec *herdrCallRecorder, want int) {
	t.Helper()
	if want == 0 {
		time.Sleep(20 * time.Millisecond)
		if got := len(rec.snapshot()); got != 0 {
			t.Fatalf("calls = %d, want 0", got)
		}
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.snapshot()) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("calls = %d, want %d", len(rec.snapshot()), want)
}

func herdrFlagValue(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func herdrMustSeq(t *testing.T, args []string) int64 {
	t.Helper()
	value, ok := herdrFlagValue(args, "--seq")
	if !ok {
		t.Fatalf("missing --seq in %q", strings.Join(args, " "))
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("--seq %q not a number", value)
	}
	return n
}
