package builtin

import (
	"context"
	"encoding/json"
	"fmt"
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
	Question      string
	Options       []string
	BashHash      string // non-empty when this is a bash approval request; carries the command hash
	BashToolUseID string // stable id for the pending bash tool call
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
		Question string `json:"question"`
		Options  string `json:"options"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	if args.Question == "" {
		return "", fmt.Errorf("question 参数是必填的")
	}

	options := strings.Split(args.Options, "|")
	cleanOptions := make([]string, 0, len(options))
	for _, opt := range options {
		opt = strings.TrimSpace(opt)
		if opt != "" {
			cleanOptions = append(cleanOptions, opt)
		}
	}
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
