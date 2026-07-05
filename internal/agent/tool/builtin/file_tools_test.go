package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobToolMatchesRecursivePatterns(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(root, "cmd", "app", "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(root, "README.md"), "# readme\n")

	tool := NewGlobTool(FileToolConfig{AllowedRoots: []string{root}})
	got, err := tool.InvokableRun(context.Background(), `{"pattern":"**/*.go"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	var result struct {
		Matches []string `json:"matches"`
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("glob result JSON error = %v: %s", err, got)
	}
	joined := strings.Join(result.Matches, "\n")
	for _, want := range []string{"main.go", "cmd/app/main.go"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("glob matches = %#v, missing %q", result.Matches, want)
		}
	}
}

func TestReadFileToolRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	mustWriteFile(t, outside, "secret\n")

	tool := NewReadFileTool(FileToolConfig{AllowedRoots: []string{root}})
	got, err := tool.InvokableRun(context.Background(), `{"path":"`+jsonEscape(outside)+`"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(got, `"ok":false`) || !strings.Contains(got, "outside allowed roots") {
		t.Fatalf("read outside result = %s", got)
	}
}

func TestGrepToolReturnsMatchingContent(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "main.go"), "package main\nfunc target() {}\n")
	mustWriteFile(t, filepath.Join(root, "notes.txt"), "target\n")

	tool := NewGrepTool(FileToolConfig{AllowedRoots: []string{root}})
	got, err := tool.InvokableRun(context.Background(), `{"pattern":"target","glob":"**/*.go","output_mode":"content"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(got, "main.go") || !strings.Contains(got, "func target") || strings.Contains(got, "notes.txt") {
		t.Fatalf("grep result = %s", got)
	}
}

func TestEditFileToolRequiresUniqueOldString(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	mustWriteFile(t, path, "x := 1\nx := 1\n")

	tool := NewEditFileTool(FileToolConfig{AllowedRoots: []string{root}})
	got, err := tool.InvokableRun(context.Background(), `{"path":"main.go","old_string":"x := 1","new_string":"x := 2"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(got, `"ok":false`) || !strings.Contains(got, "matches 2 times") {
		t.Fatalf("edit duplicate result = %s", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x := 1\nx := 1\n" {
		t.Fatalf("file changed on failed edit: %q", string(data))
	}
}

func TestEditFileToolReplacesUniqueString(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	mustWriteFile(t, path, "x := 1\n")

	tool := NewEditFileTool(FileToolConfig{AllowedRoots: []string{root}})
	got, err := tool.InvokableRun(context.Background(), `{"path":"main.go","old_string":"x := 1","new_string":"x := 2"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(got, `"ok":true`) || !strings.Contains(got, `"replacements":1`) {
		t.Fatalf("edit result = %s", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x := 2\n" {
		t.Fatalf("file content = %q", string(data))
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jsonEscape(value string) string {
	raw, _ := json.Marshal(value)
	return strings.Trim(string(raw), `"`)
}
