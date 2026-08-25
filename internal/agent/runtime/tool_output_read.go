package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type toolOutputReadTool struct{ store *toolOutputStore }

func (t *toolOutputReadTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "read_tool_output", Desc: "按行读取此前被压缩的工具原始输出。仅在现有 output_id 缺少关键证据时调用。", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"output_id":  {Type: schema.String, Desc: "工具结果中给出的 output_id", Required: true},
		"start_line": {Type: schema.Integer, Desc: "起始行，从 1 开始"},
		"max_lines":  {Type: schema.Integer, Desc: "最多读取行数，最大 200"},
	})}, nil
}

func (t *toolOutputReadTool) InvokableRun(_ context.Context, arguments string, _ ...tool.Option) (string, error) {
	var in struct {
		OutputID  string `json:"output_id"`
		StartLine int    `json:"start_line"`
		MaxLines  int    `json:"max_lines"`
	}
	if err := json.Unmarshal([]byte(arguments), &in); err != nil {
		return "", fmt.Errorf("参数解析失败：%w", err)
	}
	return t.store.read(in.OutputID, in.StartLine, in.MaxLines)
}

func (a *Agent) appendToolOutputReader(tools []tool.BaseTool) []tool.BaseTool {
	if a == nil || a.toolOutputs == nil {
		return tools
	}
	return append(tools, a.WrapTool(&toolOutputReadTool{store: a.toolOutputs}, "runtime"))
}
