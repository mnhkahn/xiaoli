package mcp

import (
	"context"
	"strings"
	"testing"
)

type fakeDeviceCaller struct {
	deviceID string
	toolName string
	args     map[string]any
	timeout  int
}

func (f *fakeDeviceCaller) CallTool(_ context.Context, deviceID string, toolName string, args map[string]any, timeout int) (any, string, error) {
	f.deviceID = deviceID
	f.toolName = toolName
	f.args = args
	f.timeout = timeout
	return map[string]any{"ok": true}, "", nil
}

func TestNewDeviceToolsExposeOpenAICompatibleNames(t *testing.T) {
	rawTools := []map[string]any{
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
	}

	tools := NewDeviceTools("device-1", rawTools, &fakeDeviceCaller{})
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	wrapped, ok := tools[0].(*DeviceTool)
	if !ok {
		t.Fatalf("tool type = %T, want *DeviceTool", tools[0])
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
	if wrapped.ToolName != "self.camera.take_photo" {
		t.Fatalf("wrapped original tool name = %q, want self.camera.take_photo", wrapped.ToolName)
	}
}

func TestDeviceToolInvokesCaller(t *testing.T) {
	caller := &fakeDeviceCaller{}
	tools := NewDeviceTools("device-1", []map[string]any{{"name": "camera.take"}}, caller)
	wrapped, ok := tools[0].(*DeviceTool)
	if !ok {
		t.Fatalf("tool type = %T, want *DeviceTool", tools[0])
	}
	got, err := wrapped.InvokableRun(context.Background(), `{"question":"拍照"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(got, `"ok":true`) {
		t.Fatalf("InvokableRun() = %q, want JSON result", got)
	}
	if caller.deviceID != "device-1" || caller.toolName != "camera.take" || caller.timeout != 30 {
		t.Fatalf("caller = %#v, want device/tool/timeout", caller)
	}
	if caller.args["question"] != "拍照" {
		t.Fatalf("caller args = %#v, want parsed args", caller.args)
	}
}
