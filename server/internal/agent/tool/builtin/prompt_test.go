package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePromptRefsFileRef(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := ResolveConfig{
		AllowedRoots: []string{dir},
		MaxFileBytes: 4096,
	}
	result, err := ResolvePromptRefs(context.Background(), "file:"+src+"\nread this file", cfg, nil)
	if err != nil {
		t.Fatalf("ResolvePromptRefs error = %v", err)
	}
	if !contains(result, "hello world") {
		t.Fatalf("result should contain file content, got %q", result)
	}
}

func TestResolvePromptRefsFileRefNotFound(t *testing.T) {
	cfg := ResolveConfig{
		AllowedRoots: []string{"/tmp"},
		MaxFileBytes: 4096,
	}
	result, err := ResolvePromptRefs(context.Background(), "file:/nonexistent/path.txt", cfg, nil)
	if err != nil {
		t.Fatalf("ResolvePromptRefs error = %v", err)
	}
	if !contains(result, "文件读取失败") {
		t.Fatalf("result should contain error message, got %q", result)
	}
}

func TestResolvePromptRefsFileRefDisabledWithoutRoots(t *testing.T) {
	result, err := ResolvePromptRefs(context.Background(), "file:/etc/hosts", ResolveConfig{MaxFileBytes: 4096}, nil)
	if err != nil {
		t.Fatalf("ResolvePromptRefs error = %v", err)
	}
	if contains(result, "[文件读取失败") || contains(result, "```") {
		t.Fatalf("without AllowedRoots, file: refs should be silently ignored, got %q", result)
	}
	if !contains(result, "file:/etc/hosts") {
		t.Fatalf("original line should be preserved, got %q", result)
	}
}

func TestResolvePromptRefsAgentRef(t *testing.T) {
	lookup := func(name string) (string, bool) {
		if name == "explore" {
			return "快速探索代码库", true
		}
		return "", false
	}
	result, err := ResolvePromptRefs(context.Background(), "@explore 看看这个文件", DefaultResolveConfig(), lookup)
	if err != nil {
		t.Fatalf("ResolvePromptRefs error = %v", err)
	}
	if !contains(result, "快速探索代码库") {
		t.Fatalf("result should contain agent description, got %q", result)
	}
}

func TestResolvePromptRefsUnknownAgentRef(t *testing.T) {
	lookup := func(name string) (string, bool) {
		return "", false
	}
	result, err := ResolvePromptRefs(context.Background(), "@unknown 做点什么", DefaultResolveConfig(), lookup)
	if err != nil {
		t.Fatalf("ResolvePromptRefs error = %v", err)
	}
	if !contains(result, "未知代理") {
		t.Fatalf("result should contain unknown agent warning, got %q", result)
	}
}

func TestResolvePromptRefsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	cfg := ResolveConfig{
		AllowedRoots: []string{dir},
		MaxFileBytes: 4096,
	}
	result, err := ResolvePromptRefs(context.Background(), "file:../../../etc/passwd", cfg, nil)
	if err != nil {
		t.Fatalf("ResolvePromptRefs error = %v", err)
	}
	if !contains(result, "文件读取失败") {
		t.Fatalf("result should contain error message, got %q", result)
	}
}

func TestResolvePromptRefsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.txt")
	data := make([]byte, 100)
	for i := range data {
		data[i] = 'a'
	}
	if err := os.WriteFile(src, data, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := ResolveConfig{
		AllowedRoots: []string{dir},
		MaxFileBytes: 50,
	}
	result, err := ResolvePromptRefs(context.Background(), "file:"+src, cfg, nil)
	if err != nil {
		t.Fatalf("ResolvePromptRefs error = %v", err)
	}
	if !contains(result, "文件读取失败") {
		t.Fatalf("result should contain file too large error, got %q", result)
	}
}

func TestResolvePromptRefsDirectoryRef(t *testing.T) {
	dir := t.TempDir()
	cfg := ResolveConfig{
		AllowedRoots: []string{dir},
		MaxFileBytes: 4096,
	}
	result, err := ResolvePromptRefs(context.Background(), "file:"+dir, cfg, nil)
	if err != nil {
		t.Fatalf("ResolvePromptRefs error = %v", err)
	}
	if !contains(result, "文件读取失败") {
		t.Fatalf("result should contain error for directory, got %q", result)
	}
}

func TestResolvePromptRefsEmptyContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(src, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := ResolveConfig{
		AllowedRoots: []string{dir},
		MaxFileBytes: 4096,
	}
	result, err := ResolvePromptRefs(context.Background(), "file:"+src, cfg, nil)
	if err != nil {
		t.Fatalf("ResolvePromptRefs error = %v", err)
	}
	if !contains(result, "```\n\n```") && !contains(result[len(result)-10:], "``") {
		t.Fatalf("result should contain empty code block, got %q", result)
	}
}

func contains(s, substr string) bool {
	return len(substr) > 0 && s != "" && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
