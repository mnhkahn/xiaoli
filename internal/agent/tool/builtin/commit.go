package builtin

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type commitRequestKeyType struct{}

var commitRequestKey = commitRequestKeyType{}

type CommitRequest struct{}

type CommitRequestHolder struct {
	mu        sync.Mutex
	requested bool
}

func NewCommitRequestHolder(ctx context.Context) (context.Context, *CommitRequestHolder) {
	holder := &CommitRequestHolder{}
	return context.WithValue(ctx, commitRequestKey, holder), holder
}

func (h *CommitRequestHolder) Request() {
	h.mu.Lock()
	h.requested = true
	h.mu.Unlock()
}

func (h *CommitRequestHolder) Get() *CommitRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.requested {
		return nil
	}
	return &CommitRequest{}
}

type CommitTool struct{}

func NewCommitTool() *CommitTool { return &CommitTool{} }

func (t *CommitTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "commit",
		Desc:        "启动当前工作区的 Git 提交流程。仅当用户明确要求提交当前修改时调用。调用后会生成提交信息并等待用户确认，不会直接提交或推送。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *CommitTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	if holder, ok := ctx.Value(commitRequestKey).(*CommitRequestHolder); ok {
		holder.Request()
		return "已启动 Git 提交流程，正在生成提交方案并等待用户确认。", nil
	}
	return "当前渠道不支持 Git 提交流程。", nil
}
