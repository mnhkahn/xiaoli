package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
)

type minuteBucket struct {
	requests           int64
	errors             int64
	promptTokens       int64
	cachedPromptTokens int64
	completionTokens   int64
}

type modelIDKeyType struct{}

var modelIDKey = modelIDKeyType{}

type modelStartTimeKey struct{}

type Recorder struct {
	mu      sync.Mutex
	buckets map[string]map[string]*minuteBucket

	toolStats map[string]*toolStatsEntry
}

type toolStatsEntry struct {
	Calls  int64
	Errors int64
}

func llmLogFullMessages() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("LLM_LOG_FULL_MESSAGES")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

type ContextUsage struct {
	Model          string
	ContextLength  int
	MaxTokens      int
	EstimatedInput int
	CompressAt     int
}

type StatusOptions struct {
	Context *ContextUsage
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
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			mi := model.ConvCallbackInput(input)
			if mi == nil {
				return ctx
			}

			ctx = context.WithValue(ctx, modelStartTimeKey{}, time.Now())
			if st := traceFromContext(ctx); st != nil {
				run := st.nextModelRun()
				ctx = context.WithValue(ctx, traceRunKey{}, run)
				logTraceModelStart(ctx, run, mi.Messages, len(mi.Tools))
			} else {
				chars, nonSystem := traceMessagesStats(mi.Messages)
				logger.Infof("[LLM] START messages=%d non_system_messages=%d prompt_chars=%d tools=%d", len(mi.Messages), nonSystem, chars, len(mi.Tools))
			}

			if llmLogFullMessages() {
				// ========== 打印完整的工具定义 - 调试时确认 skill 有没有传进来 ==========
				if len(mi.Tools) > 0 {
					logger.Infof("[LLM] ========== TOOLS DEFINITION (%d tools) ==========", len(mi.Tools))
					for i, t := range mi.Tools {
						if t == nil {
							continue
						}
						toolJSON, _ := json.Marshal(t)
						logger.Infof("[LLM] TOOL #%d: %s", i+1, string(toolJSON))
					}
					logger.Infof("[LLM] ================================================")
				} else {
					logger.Infof("[LLM] ========== NO TOOLS PROVIDED ==========")
				}

				// ========== 打印完整的消息历史 ==========
				logger.Infof("[LLM] ========== MESSAGES (%d messages) ==========", len(mi.Messages))
				for i, msg := range mi.Messages {
					if msg == nil {
						continue
					}
					logger.Infof("[LLM] MSG #%d (role=%s): %s", i+1, msg.Role, msg.Content)
					if len(msg.ToolCalls) > 0 {
						for j, tc := range msg.ToolCalls {
							logger.Infof("[LLM]   TOOL_CALL #%d: name=%q args=%s", j+1, tc.Function.Name, tc.Function.Arguments)
						}
					}
					if msg.ToolCallID != "" {
						logger.Infof("[LLM]   TOOL_RESULT id=%q name=%q", msg.ToolCallID, msg.Name)
					}
				}
				logger.Infof("[LLM] ================================================")
			}

			return ctx
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			mid := r.resolveModelID(ctx, output)
			mo := model.ConvCallbackOutput(output)
			if mo != nil {
				r.record(mid, mo)
			}

			start, _ := ctx.Value(modelStartTimeKey{}).(time.Time)
			elapsed := time.Since(start)

			promptTokens, completionTokens, totalTokens := 0, 0, 0
			if mo != nil && mo.TokenUsage != nil {
				promptTokens = mo.TokenUsage.PromptTokens
				completionTokens = mo.TokenUsage.CompletionTokens
				totalTokens = mo.TokenUsage.TotalTokens
			}

			if run, _ := ctx.Value(traceRunKey{}).(traceRun); run.Step > 0 {
				var msg *schema.Message
				if mo != nil {
					msg = mo.Message
				}
				logTraceModelEnd(ctx, run, msg, promptTokens, completionTokens, totalTokens, elapsed)
			} else {
				logger.Infof("[LLM] END tokens={prompt:%d completion:%d total:%d} elapsed=%v",
					promptTokens, completionTokens, totalTokens, elapsed)
			}

			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			mid := r.resolveModelID(ctx, nil)
			r.recordError(mid)

			start, _ := ctx.Value(modelStartTimeKey{}).(time.Time)
			elapsed := time.Since(start)

			if run, _ := ctx.Value(traceRunKey{}).(traceRun); run.Step > 0 {
				logTraceModelError(ctx, run, info.Name, err, elapsed)
			} else {
				logger.Infof("[LLM] ERROR model=%s elapsed=%v err=%v", info.Name, elapsed, err)
			}
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
		prompt := int64(mo.TokenUsage.PromptTokens)
		cached := int64(0)
		if mo.TokenUsage.PromptTokenDetails.CachedTokens > 0 {
			cached = int64(mo.TokenUsage.PromptTokenDetails.CachedTokens)
		}
		if cached > prompt {
			cached = prompt
		}
		if cached < 0 {
			cached = 0
		}
		b.promptTokens += prompt
		b.cachedPromptTokens += cached
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

func (r *Recorder) Status(opts StatusOptions) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var b strings.Builder

	if ctx := opts.Context; ctx != nil && ctx.ContextLength > 0 {
		b.WriteString("━━━ 当前上下文 ━━━\n")
		fmt.Fprintf(&b, "模型：%s\n", ctx.Model)
		fmt.Fprintf(&b, "窗口：%d\n", ctx.ContextLength)
		fmt.Fprintf(&b, "当前输入估算：%d", ctx.EstimatedInput)
		if ctx.MaxTokens > 0 {
			fmt.Fprintf(&b, "\n输出预留：%d", ctx.MaxTokens)
		}
		b.WriteByte('\n')
		pct := float64(ctx.EstimatedInput) / float64(ctx.ContextLength) * 100
		filled := int(pct) / 10
		if filled > 10 {
			filled = 10
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
		fmt.Fprintf(&b, "占用：%.1f%% %s\n", pct, bar)
		fmt.Fprintf(&b, "阈值：约 %d 后触发自动压缩\n", ctx.CompressAt)
	}

	if len(r.buckets) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("━━━ LLM 调用统计（近 60 分钟）━━━\n")

		models := make([]string, 0, len(r.buckets))
		for mid := range r.buckets {
			models = append(models, mid)
		}
		sortStrings(models)

		for _, mid := range models {
			mb := r.buckets[mid]
			fmt.Fprintf(&b, "\n%s\n", mid)
			var totalReqs, totalErrs, totalPi, totalPo, totalCached int64
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
				totalCached += bkt.cachedPromptTokens
				fmt.Fprintf(&b, "  %s  %d req  %d err  %d in  %d out", mm, bkt.requests, bkt.errors, bkt.promptTokens, bkt.completionTokens)
				if bkt.promptTokens > 0 {
					cached := bkt.cachedPromptTokens
					if cached < 0 {
						cached = 0
					}
					hit := float64(cached) / float64(bkt.promptTokens) * 100
					fmt.Fprintf(&b, "  cached %d  hit %.1f%%", cached, hit)
				}
				b.WriteByte('\n')
			}
			if totalReqs > 0 {
				b.WriteString("  ─────────────────────\n")
				fmt.Fprintf(&b, "  合计    %d req  %d err  %d in  %d out", totalReqs, totalErrs, totalPi, totalPo)
				if totalPi > 0 {
					cached := totalCached
					if cached < 0 {
						cached = 0
					}
					hit := float64(cached) / float64(totalPi) * 100
					fmt.Fprintf(&b, "  cached %d  hit %.1f%%", cached, hit)
				}
				b.WriteByte('\n')
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
	category string
	recorder *Recorder
}

func newToolCounter(inner tool.InvokableTool, recorder *Recorder, category string) *toolCounter {
	info, _ := inner.Info(context.Background())
	toolName := "unknown"
	if info != nil && info.Name != "" {
		toolName = info.Name
	}
	return &toolCounter{inner: inner, toolName: toolName, category: category, recorder: recorder}
}

func (w *toolCounter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *toolCounter) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	w.recorder.RecordToolCall(w.toolName)
	st := traceFromContext(ctx)
	step := 0
	start := time.Now()
	if st != nil {
		step = st.nextToolStep()
		logTraceToolStart(ctx, step, w.toolName, w.category, argumentsInJSON)
	}
	result, err := w.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		w.recorder.RecordToolError(w.toolName)
	}
	if st != nil {
		logTraceToolEnd(ctx, step, w.toolName, w.category, len(result), time.Since(start), err, result)
	}
	return result, err
}

func (a *Agent) WrapTool(t tool.BaseTool, category string) tool.BaseTool {
	if a == nil || a.recorder == nil {
		return t
	}
	if invokable, ok := t.(tool.InvokableTool); ok {
		return newToolCounter(invokable, a.recorder, category)
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
