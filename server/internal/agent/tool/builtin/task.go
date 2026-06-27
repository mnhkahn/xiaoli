package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"xiaoli/server/internal/event"
)

type SubAgentSpec struct {
	Name          string
	Description   string
	SystemPrompt  string
	MaxSteps      int
	AllowTools    bool
	IsFork        bool
	DisabledTools []string
}

type SubAgentRuntime struct {
	TaskID        string
	Background    bool
	SessionKey    string
	ParentSession string
	DeviceID      string
	ChannelName   string
}

type BackgroundJob struct {
	mu            sync.Mutex
	ID            string
	Status        string
	Result        string
	Error         string
	Done          chan struct{}
	CreatedAt     time.Time
	ParentSession string
		AgentName     string
		AgentType     string
		Description   string
		StartedAt     time.Time
		FinishedAt    *time.Time
		Duration      time.Duration
}

func (j *BackgroundJob) Snapshot() BackgroundJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	return BackgroundJob{
		ID: j.ID, Status: j.Status, Result: j.Result,
		Error: j.Error, Done: j.Done, CreatedAt: j.CreatedAt,
		ParentSession: j.ParentSession,
		AgentName: j.AgentName, AgentType: j.AgentType,
		Description: j.Description,
		StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
		Duration: j.Duration,
	}
}

type TaskTool struct {
	subAgents     map[string]SubAgentSpec
	runSubAgent   func(ctx context.Context, spec SubAgentSpec, rt *SubAgentRuntime, prompt string) (string, error)
	injectResult  func(ctx context.Context, taskID, state, content string) error
	eventBus      event.Publisher
	mu            sync.Mutex
	jobs          map[string]*BackgroundJob
	activeJobs    int32
	maxConcurrent int32
	jobIDCounter  uint64
	resolveCfg    ResolveConfig
}

// SubAgentSpecByName returns the SubAgentSpec for the named agent, if loaded.
// Used by A2A routing to invoke a dedicated subagent without going through
// the main agent's task tool.
func (t *TaskTool) SubAgentSpecByName(name string) (SubAgentSpec, bool) {
	spec, ok := t.subAgents[name]
	return spec, ok
}

func NewTaskTool(subAgents map[string]SubAgentSpec, fn func(ctx context.Context, spec SubAgentSpec, rt *SubAgentRuntime, prompt string) (string, error), eventBus event.Publisher) *TaskTool {
	if eventBus == nil {
		eventBus = noopPublisher{}
	}
	return &TaskTool{
		subAgents:     subAgents,
		runSubAgent:   fn,
		jobs:          make(map[string]*BackgroundJob),
		maxConcurrent: 5,
		resolveCfg:    ResolveConfig{AllowedRoots: nil, MaxFileBytes: 64 * 1024},
		eventBus:      eventBus,
	}
}

// noopPublisher is a no-op event publisher for backward compatibility
type noopPublisher struct{}

func (n noopPublisher) Publish(ctx context.Context, e event.Event) error {
	return nil
}

func (t *TaskTool) SetInjectFn(fn func(ctx context.Context, taskID, state, content string) error) {
	t.injectResult = fn
}

func (t *TaskTool) SetAllowedRoots(roots []string) {
	t.resolveCfg.AllowedRoots = roots
}

func DefaultSubAgents() map[string]SubAgentSpec {
	return map[string]SubAgentSpec{
		"explore": {
			Name:         "explore",
			Description:  "快速探索代码库、搜索文件内容和理解项目结构",
			SystemPrompt: "你是一个代码探索者。快速浏览文件和目录，理解代码结构和功能。回答要简洁直接。",
			MaxSteps:     5,
			AllowTools:   false,
		},
		"general": {
			Name:         "general",
			Description:  "通用多步骤任务执行，适合实现功能、重构或修复",
			SystemPrompt: "你是一个通用任务执行者。按步骤完成任务，提供清晰的输出。如果需要修改代码，请直接输出修改后的代码内容。",
			MaxSteps:     15,
			AllowTools:   true,
		},
	}
}

func (t *TaskTool) QueryJob(taskID string) *BackgroundJob {
	t.mu.Lock()
	defer t.mu.Unlock()
	job := t.jobs[taskID]
	if job == nil {
		return nil
	}
	snap := job.Snapshot()
	return &snap
}

func (t *TaskTool) Info(context.Context) (*schema.ToolInfo, error) {
	typeNames := sortedKeys(t.subAgents)
	desc := "创建一个子代理（subagent）来执行独立任务。当遇到多步骤、探索性或可并行的工作时，委托给子代理执行。\n\n可用的子代理类型："
	for _, name := range typeNames {
		spec := t.subAgents[name]
		if spec.Description != "" {
			desc += fmt.Sprintf("\n- %s: %s", name, spec.Description)
		}
	}

	return &schema.ToolInfo{
		Name: "task",
		Desc: desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"description": {
				Type:     schema.String,
				Desc:     "任务的简要说明，用于在用户界面显示任务状态。查任务状态时可不填。",
				Required: false,
			},
			"prompt": {
				Type:     schema.String,
				Desc:     "子代理要执行的具体指令。查任务状态时可不填。",
				Required: false,
			},
			"subagent_type": {
				Type:     schema.String,
				Desc:     "子代理类型，选择使用哪个子代理来执行任务。查任务状态时可不填。",
				Required: false,
				Enum:     typeNames,
			},
			"task_id": {
				Type:     schema.String,
				Desc:     "已有任务 ID（之前返回的 task_id）。传入后复用该任务的子会话继续执行。不填则创建新任务。",
				Required: false,
			},
			"background": {
				Type:     schema.Boolean,
				Desc:     "是否后台运行。true 时立即返回 task_id，之后可用此 id 查询状态或复用子会话。",
				Required: false,
			},
			"task_status": {
				Type:     schema.String,
				Desc:     "查询指定 task_id 的当前状态。传入之前返回的 task_id，返回 running/completed/failed 及结果。",
				Required: false,
			},
		}),
	}, nil
}

func (t *TaskTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		SubAgentType string `json:"subagent_type"`
		TaskID       string `json:"task_id,omitempty"`
		Background   bool   `json:"background,omitempty"`
		TaskStatus   string `json:"task_status,omitempty"`
Fork         bool   `json:"fork,omitempty"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return taskResult("error", "参数解析失败："+err.Error()), nil
	}

	if args.TaskStatus != "" {
		job := t.QueryJob(args.TaskStatus)
		if job == nil {
			return taskResult("error", fmt.Sprintf("未找到任务 %q", args.TaskStatus)), nil
		}
		content := fmt.Sprintf("任务 %s 状态：%s", job.ID, job.Status)
		if job.Result != "" {
			content += "\n\n" + job.Result
		}
		if job.Error != "" {
			content += "\n\n错误：" + job.Error
		}
		return taskResult(job.Status, content), nil
	}

	if args.Description == "" {
		return taskResult("error", "参数 description 是必填的"), nil
	}
	if args.Prompt == "" {
		return taskResult("error", "参数 prompt 是必填的"), nil
	}
	isFork := args.Fork || args.SubAgentType == "fork"
	if !isFork && args.SubAgentType == "" {
		return taskResult("error", "参数 subagent_type 是必填的（或设置 fork: true）"), nil
	}
	var spec SubAgentSpec
	var agentName string
	if isFork {
		spec = t.subAgents["fork"]
		agentName = "fork"
		args.Background = true
	} else {
		var ok bool
		spec, ok = t.subAgents[args.SubAgentType]
		if !ok {
			return taskResult("error", fmt.Sprintf("未知的子代理类型 %q，可用：%s", args.SubAgentType, joinKeys(t.subAgents))), nil
		}
		agentName = args.SubAgentType
	}

	resolvedPrompt, resolveErr := ResolvePromptRefs(ctx, args.Prompt, t.resolveCfg,
		func(name string) (string, bool) {
			s, ok := t.subAgents[name]
			if !ok {
				return "", false
			}
			return s.Description, true
		},
	)
	if resolveErr != nil {
		return taskResult("error", "指令解析失败："+resolveErr.Error()), nil
	}
	args.Prompt = resolvedPrompt

	if args.Background {
		t.mu.Lock()
		if t.activeJobs >= t.maxConcurrent {
			t.mu.Unlock()
			return taskResult("error", fmt.Sprintf("后台任务已达上限（%d），请等待当前任务完成后再试", t.maxConcurrent)), nil
		}
		jobID := fmt.Sprintf("task_%d", t.jobIDCounter)
		t.jobIDCounter++
		effectiveTaskID := args.TaskID
		if effectiveTaskID == "" {
			effectiveTaskID = jobID
		}
		if existing, ok := t.jobs[effectiveTaskID]; ok {
			snap := existing.Snapshot()
			if snap.Status == "running" {
				t.mu.Unlock()
				return taskResult("error", fmt.Sprintf("任务 %s 正在运行中，不能重复启动", effectiveTaskID)), nil
			}
		}
		t.activeJobs++
		parentSession, _ := ctx.Value(SubAgentParentKey).(string)
	deviceID, _ := ctx.Value(SubAgentDeviceIDKey).(string)
	channelName, _ := ctx.Value(SubAgentChannelKey).(string)
		now := time.Now()
		job := &BackgroundJob{
			ID:            effectiveTaskID,
			Status:        "running",
			Done:          make(chan struct{}),
			CreatedAt:     now,
			ParentSession: parentSession,
			AgentName:     agentName,
			AgentType:     "normal",
			Description:   args.Description,
			StartedAt:     now,
		}
		t.jobs[effectiveTaskID] = job
		t.mu.Unlock()

		// Publish event when task starts
		t.publishTodoUpdate(ctx, parentSession, effectiveTaskID)

		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		go func() {
			startTime := time.Now()
			defer cancel()
			defer func() {
				if r := recover(); r != nil {
					panicMsg := fmt.Sprintf("panic: %v", r)
					job.mu.Lock()
					job.Status = "failed"
					job.Error = panicMsg
					job.mu.Unlock()
					if t.injectResult != nil {
						t.injectResult(bgCtx, effectiveTaskID, "failed", panicMsg)
					}
					close(job.Done)
					t.releaseSlot()
					t.cleanupOldJobs()

					// Publish event on panic
					t.publishTodoUpdate(bgCtx, parentSession, effectiveTaskID)
				}
			}()

			rt := &SubAgentRuntime{TaskID: effectiveTaskID, SessionKey: effectiveTaskID, Background: true, ParentSession: parentSession, DeviceID: deviceID, ChannelName: channelName}
			if parentSession != "" {
				rt.SessionKey = parentSession + ":" + effectiveTaskID
			}
			result, err := t.runSubAgent(bgCtx, spec, rt, args.Prompt)

			job.mu.Lock()
			if err != nil {
				job.Status = "failed"
				job.Error = err.Error()
				now := time.Now()
				job.FinishedAt = &now
				job.Duration = now.Sub(startTime)
			} else if result == "" {
				job.Status = "failed"
				job.Error = "子代理返回空结果"
				now := time.Now()
				job.FinishedAt = &now
				job.Duration = now.Sub(startTime)
			} else {
				job.Status = "completed"
				job.Result = result
				now := time.Now()
				job.FinishedAt = &now
				job.Duration = now.Sub(startTime)
			}
			state := job.Status
			resultContent := job.Result
			if state == "failed" {
				resultContent = job.Error
			}
			job.mu.Unlock()

			if t.injectResult != nil {
				if injectErr := t.injectResult(bgCtx, effectiveTaskID, state, resultContent); injectErr != nil {
					job.mu.Lock()
					if job.Error != "" {
						job.Error += "; "
					}
					job.Error += "inject failed: " + injectErr.Error()
					job.mu.Unlock()
				}
			}

			close(job.Done)
			t.releaseSlot()
			t.cleanupOldJobs()

			// Publish event when task completes
			t.publishTodoUpdate(bgCtx, parentSession, effectiveTaskID)
		}()

		return taskResult("running", effectiveTaskID), nil
	}

	parentSession, _ := ctx.Value(SubAgentParentKey).(string)
	deviceID, _ := ctx.Value(SubAgentDeviceIDKey).(string)
	channelName, _ := ctx.Value(SubAgentChannelKey).(string)
	rt := &SubAgentRuntime{TaskID: args.TaskID, SessionKey: args.TaskID, ParentSession: parentSession, DeviceID: deviceID, ChannelName: channelName}
	if parentSession != "" && args.TaskID != "" {
		rt.SessionKey = parentSession + ":" + args.TaskID
	}
	result, err := t.runSubAgent(ctx, spec, rt, args.Prompt)
	if err != nil {
		return taskResult("error", "子代理执行失败："+err.Error()), nil
	}
	if result == "" {
		return taskResult("error", "子代理返回空结果"), nil
	}

	return taskResult("completed", result), nil
}

func (t *TaskTool) releaseSlot() {
	t.mu.Lock()
	t.activeJobs--
	t.mu.Unlock()
}

// publishTodoUpdate publishes a todo.updated event.
// If parentSession is provided, only jobs for that session are included.
// If changedTaskID is provided, it identifies which specific task triggered the update.
func (t *TaskTool) publishTodoUpdate(ctx context.Context, parentSession string, changedTaskID string) {
	jobs := t.ListJobs()

	// Filter to only the affected session if known
	var filteredJobs []JobSummary
	if parentSession != "" {
		for _, j := range jobs {
			if j.ParentSession == parentSession {
				filteredJobs = append(filteredJobs, j)
			}
		}
	} else {
		filteredJobs = jobs
	}

	todos := make([]event.Todo, 0, len(filteredJobs))
	for _, j := range filteredJobs {
		todos = append(todos, event.Todo{
			ID:            j.ID,
			Title:         j.Description,
			Status:        j.Status,
			SessionID:     j.ParentSession,
			ParentSession: j.ParentSession,
			StartedAt:     j.StartedAt.Unix(),
			CompletedAt: func() int64 {
				if j.FinishedAt != nil {
					return j.FinishedAt.Unix()
				}
				return 0
			}(),
		})
	}

	_ = t.eventBus.Publish(ctx, event.Event{
		Type:      event.TypeTodoUpdated,
		SessionID: parentSession,
		Data: event.TodoUpdatedData{
			SessionID:     parentSession,
			ChangedTaskID: changedTaskID,
			Todos:         todos,
		},
	})
}

type JobSummary struct {
	ID            string
	Status        string
	CreatedAt     time.Time
	StartedAt     time.Time
	FinishedAt    *time.Time
	ParentSession string
	AgentName     string
	AgentType     string
	Description   string
	Duration      time.Duration
}

func (t *TaskTool) ListJobs() []JobSummary {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]JobSummary, 0, len(t.jobs))
	for _, job := range t.jobs {
		snap := job.Snapshot()
		result = append(result, JobSummary{
			ID:            snap.ID,
			Status:        snap.Status,
			CreatedAt:     snap.CreatedAt,
			StartedAt:     snap.StartedAt,
			FinishedAt:    snap.FinishedAt,
			ParentSession: snap.ParentSession,
			AgentName:     snap.AgentName,
			AgentType:     snap.AgentType,
			Description:   snap.Description,
			Duration:      snap.Duration,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}

func (t *TaskTool) cleanupOldJobs() {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := time.Now().Add(-30 * time.Minute)
	for id, job := range t.jobs {
		if job.CreatedAt.Before(cutoff) {
			delete(t.jobs, id)
		}
	}
}

func taskResult(state, content string) string {
	return fmt.Sprintf(`<task state="%s">
%s
</task>`, state, content)
}

func joinKeys(m map[string]SubAgentSpec) string {
	keys := sortedKeys(m)
	sep := ""
	var r string
	for _, k := range keys {
		r += sep + k
		sep = ", "
	}
	return r
}

func sortedKeys(m map[string]SubAgentSpec) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
