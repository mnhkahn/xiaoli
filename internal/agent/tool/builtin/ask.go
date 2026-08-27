package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type askDataKeyType struct{}

var AskDataKey = askDataKeyType{}

type AskDataHolder struct {
	mu   sync.Mutex
	Data *AskData
}

func (h *AskDataHolder) Set(d *AskData) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Data = d
}

func (h *AskDataHolder) Get() *AskData {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.Data
}

func NewAskDataHolder(ctx context.Context) (context.Context, *AskDataHolder) {
	holder := &AskDataHolder{}
	return context.WithValue(ctx, AskDataKey, holder), holder
}

type AskData struct {
	Question string
	Options  []string
}

type AskUserQuestionTool struct{}

func NewAskUserQuestionTool() *AskUserQuestionTool {
	return &AskUserQuestionTool{}
}

func (t *AskUserQuestionTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "ask_user_question",
		Desc: "向用户发送问题让用户选择。问题会以选项按钮（飞书）或文字列表（其他渠道）展示给用户。注意：工具立即返回，不阻塞等待用户回答。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"question": {
				Type:     schema.String,
				Desc:     "要问用户的问题",
				Required: true,
			},
			"options": {
				Type: schema.String,
				Desc: "可选项，用 | 分隔。例如：确认|取消。不填则默认显示是/否",
			},
		}),
	}, nil
}

func (t *AskUserQuestionTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Question string          `json:"question"`
		Options  json.RawMessage `json:"options"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	if args.Question == "" {
		return "", fmt.Errorf("question 参数是必填的")
	}

	cleanOptions := askOptions(args.Options)
	if len(cleanOptions) == 0 {
		cleanOptions = []string{"是", "否"}
	}

	if holder, ok := ctx.Value(AskDataKey).(*AskDataHolder); ok {
		holder.Set(&AskData{
			Question: args.Question,
			Options:  cleanOptions,
		})
	}

	return fmt.Sprintf("已向用户提问：%s，等待用户选择", args.Question), nil
}

func askOptions(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{"是", "否"}
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return cleanAskOptions(strings.Split(text, "|"))
	}
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		return cleanAskOptions(values)
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) == nil {
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if value, ok := object[key].(string); ok {
				values = append(values, value)
			}
		}
		if len(values) > 0 {
			return cleanAskOptions(values)
		}
	}
	return []string{"是", "否"}
}

func cleanAskOptions(options []string) []string {
	clean := make([]string, 0, len(options))
	for _, option := range options {
		if option = strings.TrimSpace(option); option != "" {
			clean = append(clean, option)
		}
	}
	if len(clean) == 0 {
		return []string{"是", "否"}
	}
	return clean
}
