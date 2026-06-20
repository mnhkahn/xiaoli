package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type SubAgentSpec struct {
	Name         string
	Description  string
	SystemPrompt string
	MaxSteps     int
	AllowTools   bool
}

type TaskTool struct {
	subAgents   map[string]SubAgentSpec
	runSubAgent func(ctx context.Context, spec SubAgentSpec, prompt string) (string, error)
}

func NewTaskTool(subAgents map[string]SubAgentSpec, fn func(ctx context.Context, spec SubAgentSpec, prompt string) (string, error)) *TaskTool {
	return &TaskTool{subAgents: subAgents, runSubAgent: fn}
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
				Desc:     "任务的简要说明，用于在用户界面显示任务状态",
				Required: true,
			},
			"prompt": {
				Type:     schema.String,
				Desc:     "子代理要执行的具体指令。可以包含详细信息、上下文和需要的输出格式",
				Required: true,
			},
			"subagent_type": {
				Type:     schema.String,
				Desc:     "子代理类型，选择使用哪个子代理来执行任务",
				Required: true,
				Enum:     typeNames,
			},
		}),
	}, nil
}

func (t *TaskTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		SubAgentType string `json:"subagent_type"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return taskResult("error", "参数解析失败："+err.Error()), nil
	}
	if args.Description == "" {
		return taskResult("error", "参数 description 是必填的"), nil
	}
	if args.Prompt == "" {
		return taskResult("error", "参数 prompt 是必填的"), nil
	}
	if args.SubAgentType == "" {
		return taskResult("error", "参数 subagent_type 是必填的"), nil
	}

	spec, ok := t.subAgents[args.SubAgentType]
	if !ok {
		return taskResult("error", fmt.Sprintf("未知的子代理类型 %q，可用：%s", args.SubAgentType, joinKeys(t.subAgents))), nil
	}

	result, err := t.runSubAgent(ctx, spec, args.Prompt)
	if err != nil {
		return taskResult("error", "子代理执行失败："+err.Error()), nil
	}
	if result == "" {
		return taskResult("error", "子代理返回空结果"), nil
	}

	return taskResult("completed", result), nil
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
