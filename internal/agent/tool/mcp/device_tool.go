package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
)

type DeviceToolCaller interface {
	CallTool(ctx context.Context, deviceID string, toolName string, args map[string]any, timeout int) (result any, toolError string, err error)
}

type DeviceTool struct {
	info     *schema.ToolInfo
	caller   DeviceToolCaller
	DeviceID string
	ToolName string
}

func (t *DeviceTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *DeviceTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		args = map[string]any{}
	}
	result, toolError, err := t.caller.CallTool(ctx, t.DeviceID, t.ToolName, args, 30)
	if err != nil {
		return fmt.Sprintf("tool call error: %v", err), nil
	}
	if toolError != "" {
		return fmt.Sprintf("tool error: %s", toolError), nil
	}
	raw, _ := json.Marshal(result)
	return string(raw), nil
}

func NewDeviceTools(deviceID string, rawTools []map[string]any, caller DeviceToolCaller) []tool.BaseTool {
	var tools []tool.BaseTool
	usedNames := map[string]int{}
	for _, raw := range rawTools {
		name, _ := raw["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := raw["description"].(string)
		if desc == "" {
			desc = name
		}

		var paramsOneOf *schema.ParamsOneOf
		if inputSchema, ok := raw["inputSchema"]; ok && inputSchema != nil {
			schemaBytes, err := json.Marshal(inputSchema)
			if err == nil {
				var js einojsonschema.Schema
				if err := json.Unmarshal(schemaBytes, &js); err == nil {
					paramsOneOf = schema.NewParamsOneOfByJSONSchema(&js)
				}
			}
		}

		tools = append(tools, &DeviceTool{
			info: &schema.ToolInfo{
				Name:        UniqueSafeToolName(name, usedNames),
				Desc:        desc,
				ParamsOneOf: paramsOneOf,
			},
			caller:   caller,
			DeviceID: deviceID,
			ToolName: name,
		})
	}
	return tools
}
