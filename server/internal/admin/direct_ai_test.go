package admin

import (
	"context"
	"strings"
	"testing"
)

func TestMCPToolsExposeOpenAICompatibleNames(t *testing.T) {
	session := &deviceSession{
		deviceID: "device-1",
		tools: []map[string]any{
			{
				"name":        "self.camera.take_photo",
				"description": "Take a photo",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"question": map[string]any{"type": "string"},
					},
					"required": []any{"question"},
				},
			},
		},
	}

	tools := mcpToolsToEinoTools(session, nil)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	wrapped, ok := tools[0].(*mcpTool)
	if !ok {
		t.Fatalf("tool type = %T, want *mcpTool", tools[0])
	}
	info, err := wrapped.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if strings.Contains(info.Name, ".") {
		t.Fatalf("exposed tool name %q is not OpenAI-compatible", info.Name)
	}
	if info.Name != "self_camera_take_photo" {
		t.Fatalf("exposed tool name = %q, want self_camera_take_photo", info.Name)
	}
	if wrapped.toolName != "self.camera.take_photo" {
		t.Fatalf("wrapped original tool name = %q, want self.camera.take_photo", wrapped.toolName)
	}
}
