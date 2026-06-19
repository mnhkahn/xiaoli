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

	builtinCalls  int64
	builtinErrors int64
	mcpCalls      int64
	mcpErrors     int64
	skillCalls    int64
	skillErrors   int64
}

var (
	registerOnce sync.Once
	globalRec    *Recorder
)

func GlobalRecorder() *Recorder {
	registerOnce.Do(func() {
		globalRec = &Recorder{
			buckets: make(map[string]map[string]*minuteBucket),
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

func (r *Recorder) Status() string {
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
				fmt.Fprintf(&b, "  %s  %d req  %d err  %d in  %d out\n", mm, bkt.requests, bkt.errors, bkt.promptTokens, bkt.completionTokens)
			}
			if totalReqs > 0 {
				b.WriteString("  ─────────────────────\n")
				fmt.Fprintf(&b, "  合计    %d req  %d err  %d in  %d out\n", totalReqs, totalErrs, totalPi, totalPo)
			}
		}
	}

	if r.builtinCalls > 0 || r.mcpCalls > 0 || r.skillCalls > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("━━━ 工具调用统计（累计）━━━\n")
		writeToolLine(&b, "内置工具", r.builtinCalls, r.builtinErrors)
		writeToolLine(&b, "MCP 工具", r.mcpCalls, r.mcpErrors)
		writeToolLine(&b, "Skills", r.skillCalls, r.skillErrors)
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

func (r *Recorder) RecordToolCall(category string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch category {
	case "builtin":
		r.builtinCalls++
	case "mcp":
		r.mcpCalls++
	case "skill":
		r.skillCalls++
	}
}

func (r *Recorder) RecordToolError(category string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch category {
	case "builtin":
		r.builtinErrors++
	case "mcp":
		r.mcpErrors++
	case "skill":
		r.skillErrors++
	}
}

type toolCounter struct {
	inner    tool.InvokableTool
	category string
	recorder *Recorder
}

func (w *toolCounter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *toolCounter) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	w.recorder.RecordToolCall(w.category)
	result, err := w.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		w.recorder.RecordToolError(w.category)
	}
	return result, err
}

func (a *Agent) WrapTool(t tool.BaseTool, category string) tool.BaseTool {
	if a == nil || a.recorder == nil {
		return t
	}
	if invokable, ok := t.(tool.InvokableTool); ok {
		return &toolCounter{
			inner:    invokable,
			category: category,
			recorder: a.recorder,
		}
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