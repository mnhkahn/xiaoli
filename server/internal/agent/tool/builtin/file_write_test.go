package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestFileWriteToolWritesContentUnderSessionArtifactDir(t *testing.T) {
	root := t.TempDir()
	tb := NewFileWriteTool(FileWriteConfig{AllowedRoots: []string{root}})
	inv, ok := tb.(tool.InvokableTool)
	if !ok {
		t.Fatal("file_write should be invokable")
	}
	ctx := context.WithValue(context.Background(), SubAgentParentKey, "ses/abc:123")

	got, err := inv.InvokableRun(ctx, `{"filename":"guide.md","content":"# Guide\n\nhello","mime_type":"text/markdown"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("result is not JSON: %q", got)
	}
	if payload["ok"] != true {
		t.Fatalf("result = %s, want ok", got)
	}
	filePath, _ := payload["file_path"].(string)
	if filePath == "" {
		t.Fatalf("result = %s, want file_path", got)
	}
	if !strings.HasPrefix(filePath, filepath.Join(root, "xiaoli-artifacts", "ses_abc_123")+string(filepath.Separator)) {
		t.Fatalf("file_path = %q, want session artifact dir under root", filePath)
	}
	body, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", filePath, err)
	}
	if string(body) != "# Guide\n\nhello" {
		t.Fatalf("file body = %q", string(body))
	}
	if payload["display_name"] != "guide.md" || payload["mime_type"] != "text/markdown" {
		t.Fatalf("result = %#v, want display_name and mime_type", payload)
	}
}

func TestFileWriteToolRejectsPathOutsideAllowedRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "guide.md")
	tb := NewFileWriteTool(FileWriteConfig{AllowedRoots: []string{root}})
	inv := tb.(tool.InvokableTool)

	got, err := inv.InvokableRun(context.Background(), `{"file_path":"`+outside+`","content":"hello"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(got, `"ok":false`) || !strings.Contains(got, "allowed") {
		t.Fatalf("result = %s, want allowed-roots error", got)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside file stat err = %v, want not exist", err)
	}
}

func TestFileWriteToolRejectsUnsafeFilename(t *testing.T) {
	root := t.TempDir()
	tb := NewFileWriteTool(FileWriteConfig{AllowedRoots: []string{root}})
	inv := tb.(tool.InvokableTool)

	got, err := inv.InvokableRun(context.Background(), `{"filename":"../secret.md","content":"hello"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(got, `"ok":false`) || !strings.Contains(got, "filename") {
		t.Fatalf("result = %s, want filename error", got)
	}
}
