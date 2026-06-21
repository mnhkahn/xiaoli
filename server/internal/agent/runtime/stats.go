package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type minuteBucket struct {
	requests         int64
	errors           int64
	promptTokens     int64
	completionTokens int64
}

type modelIDKeyType struct{}

var modelIDKey = modelIDKeyType{}

type Recorder struct {
	mu      sync.Mutex
	buckets map[string]map[string]*minuteBucket

	toolStats map[string]*toolStatsEntry
}

type toolStatsEntry struct {
	Calls  int64
	Errors int64
}

var (
	registerOnce sync.Once
	globalRec    *Recorder
)

func GlobalRecorder() *Recorder {
	registerOnce.Do(func() {
		globalRec = &Recorder{
			buckets:   make(map[string]map[string]*minuteBucket),
			toolStats: make(map[string]*toolStatsEntry),
		}
		callbacks.AppendGlobalHandlers(globalRec.buildHandler())
		go globalRec.cleanupLoop()
	})
	return globalRec
}

func (r *Recorder) WithContext(ctx context.Context, modelID string) context.Context {
	return context.WithValue(ctx, modelIDKey, modelID)
}

func (r *Recorder) buildHandler() callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			mid := r.resolveModelID(ctx, output)
			mo := model.ConvCallbackOutput(output)
			if mo != nil {
				r.record(mid, mo)
			}
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			mid := r.resolveModelID(ctx, nil)
			r.recordError(mid)
			return ctx
		}).
		Build()
}

func (r *Recorder) resolveModelID(ctx context.Context, output callbacks.CallbackOutput) string {
	if id, ok := ctx.Value(modelIDKey).(string); ok && id != "" {
		return id
	}
	if mo := model.ConvCallbackOutput(output); mo != nil && mo.Config != nil && mo.Config.Model != "" {
		return mo.Config.Model
	}
	return "unknown"
}

func (r *Recorder) record(mid string, mo *model.CallbackOutput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.bucket(mid, time.Now())
	b.requests++
	if mo.TokenUsage != nil {
		b.promptTokens += int64(mo.TokenUsage.PromptTokens)
		b.completionTokens += int64(mo.TokenUsage.CompletionTokens)
	}
}

func (r *Recorder) recordError(mid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bucket(mid, time.Now()).errors++
}

func (r *Recorder) bucket(mid string, t time.Time) *minuteBucket {
	minute := t.Format("200601021504")
	mb, ok := r.buckets[mid]
	if !ok {
		mb = make(map[string]*minuteBucket)
		r.buckets[mid] = mb
	}
	b, ok := mb[minute]
	if !ok {
		b = &minuteBucket{}
		mb[minute] = b
	}
	return b
}

func (r *Recorder) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		r.mu.Lock()
		cutoff := time.Now().Add(-60 * time.Minute).Format("200601021504")
		for mid, mb := range r.buckets {
			for k := range mb {
				if k < cutoff {
					delete(mb, k)
				}
			}
			if len(mb) == 0 {
				delete(r.buckets, mid)
			}
		}
		r.mu.Unlock()
	}
}

func (r *Recorder) Status(contextLength int) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var b strings.Builder

	if len(r.buckets) > 0 {
		b.WriteString("━━━ LLM 调用统计（近 60 分钟）━━━\n")

		models := make([]string, 0, len(r.buckets))
		for mid := range r.buckets {
			models = append(models, mid)
		}
		sortStrings(models)

		for _, mid := range models {
			mb := r.buckets[mid]
			fmt.Fprintf(&b, "\n%s\n", mid)
			var totalReqs, totalErrs, totalPi, totalPo int64
			minutes := make([]string, 0, len(mb))
			for k := range mb {
				minutes = append(minutes, k)
			}
			sortStrings(minutes)

			for _, k := range minutes {
				bkt := mb[k]
				mm := k[8:10] + ":" + k[10:12]
				totalReqs += bkt.requests
				totalErrs += bkt.errors
				totalPi += bkt.promptTokens
				totalPo += bkt.completionTokens
				fmt.Fprintf(&b, "  %s  %d req  %d err  %d in", mm, bkt.requests, bkt.errors, bkt.promptTokens)
				if contextLength > 0 && bkt.promptTokens > 0 {
					pct := bkt.promptTokens * 100 / int64(contextLength)
					fmt.Fprintf(&b, "（%d%%）", pct)
				}
				fmt.Fprintf(&b, "  %d out\n", bkt.completionTokens)
			}
			if totalReqs > 0 {
				b.WriteString("  ─────────────────────\n")
				fmt.Fprintf(&b, "  合计    %d req  %d err  %d in", totalReqs, totalErrs, totalPi)
			if contextLength > 0 && totalPi > 0 {
				pct := totalPi * 100 / int64(contextLength)
				fmt.Fprintf(&b, "（%d%%）", pct)
			}
			fmt.Fprintf(&b, "  %d out\n", totalPo)
			}
		}
	}

	if len(r.toolStats) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("━━━ 工具调用统计（累计）━━━\n")
		names := make([]string, 0, len(r.toolStats))
		for name := range r.toolStats {
			names = append(names, name)
		}
		sortStrings(names)
		for _, name := range names {
			entry := r.toolStats[name]
			writeToolLine(&b, name, entry.Calls, entry.Errors)
		}
	}

	if b.Len() == 0 {
		return "暂无 LLM 调用记录（重启后清零）。"
	}
	return b.String()
}

func writeToolLine(b *strings.Builder, name string, calls, errors int64) {
	var rate string
	if calls > 0 {
		rate = fmt.Sprintf("成功率 %.0f%%", float64(calls-errors)/float64(calls)*100)
	}
	fmt.Fprintf(b, "%s  %d 次  %d 次失败  %s\n", name, calls, errors, rate)
}

func (r *Recorder) RecordToolCall(toolName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.toolStats == nil {
		r.toolStats = make(map[string]*toolStatsEntry)
	}
	entry := r.toolStats[toolName]
	if entry == nil {
		entry = &toolStatsEntry{}
		r.toolStats[toolName] = entry
	}
	entry.Calls++
}

func (r *Recorder) RecordToolError(toolName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.toolStats == nil {
		r.toolStats = make(map[string]*toolStatsEntry)
	}
	entry := r.toolStats[toolName]
	if entry == nil {
		entry = &toolStatsEntry{}
		r.toolStats[toolName] = entry
	}
	entry.Errors++
}

type toolCounter struct {
	inner    tool.InvokableTool
	toolName string
	recorder *Recorder
}

func newToolCounter(inner tool.InvokableTool, recorder *Recorder) *toolCounter {
	info, _ := inner.Info(context.Background())
	toolName := "unknown"
	if info != nil && info.Name != "" {
		toolName = info.Name
	}
	return &toolCounter{inner: inner, toolName: toolName, recorder: recorder}
}

func (w *toolCounter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *toolCounter) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	w.recorder.RecordToolCall(w.toolName)
	result, err := w.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		w.recorder.RecordToolError(w.toolName)
	}
	return result, err
}

func (a *Agent) WrapTool(t tool.BaseTool, category string) tool.BaseTool {
	if a == nil || a.recorder == nil {
		return t
	}
	if invokable, ok := t.(tool.InvokableTool); ok {
		return newToolCounter(invokable, a.recorder)
	}
	return t
}

func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}