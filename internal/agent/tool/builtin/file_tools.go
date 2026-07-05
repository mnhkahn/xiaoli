package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultFileToolMaxBytes = 256 * 1024
	defaultFileToolMaxItems = 100
)

type FileToolConfig struct {
	AllowedRoots []string
	MaxBytes     int64
	MaxItems     int
}

type fileToolBase struct {
	allowedRoots []string
	maxBytes     int64
	maxItems     int
}

type GlobTool struct{ fileToolBase }
type ReadFileTool struct{ fileToolBase }
type GrepTool struct{ fileToolBase }
type EditFileTool struct{ fileToolBase }

func NewGlobTool(cfg FileToolConfig) tool.InvokableTool {
	return &GlobTool{fileToolBase: newFileToolBase(cfg)}
}

func NewReadFileTool(cfg FileToolConfig) tool.InvokableTool {
	return &ReadFileTool{fileToolBase: newFileToolBase(cfg)}
}

func NewGrepTool(cfg FileToolConfig) tool.InvokableTool {
	return &GrepTool{fileToolBase: newFileToolBase(cfg)}
}

func NewEditFileTool(cfg FileToolConfig) tool.InvokableTool {
	return &EditFileTool{fileToolBase: newFileToolBase(cfg)}
}

func newFileToolBase(cfg FileToolConfig) fileToolBase {
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultFileToolMaxBytes
	}
	maxItems := cfg.MaxItems
	if maxItems <= 0 {
		maxItems = defaultFileToolMaxItems
	}
	return fileToolBase{
		allowedRoots: cleanAbsoluteRoots(cfg.AllowedRoots),
		maxBytes:     maxBytes,
		maxItems:     maxItems,
	}
}

func (t *GlobTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "glob",
		Desc: "Find files under the current trusted workspace by glob pattern. Supports ** for recursive matching and {a,b} alternatives.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"pattern": {Type: "string", Desc: "Glob pattern, for example **/*.go or *.{json,yaml}.", Required: true},
			"path":    {Type: "string", Desc: "Optional directory to search. Relative paths are resolved under the workspace root."},
			"limit":   {Type: "integer", Desc: "Maximum number of matches to return. Defaults to 100."},
		}),
	}, nil
}

func (t *ReadFileTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "read_file",
		Desc: "Read a text file under the current trusted workspace. Large and binary files are truncated or skipped.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path":      {Type: "string", Desc: "File path to read. Relative paths are resolved under the workspace root.", Required: true},
			"max_bytes": {Type: "integer", Desc: "Optional byte cap for this read."},
		}),
	}, nil
}

func (t *GrepTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "grep",
		Desc: "Search text files under the current trusted workspace using Go regular expressions.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"pattern":     {Type: "string", Desc: "Regular expression to search for.", Required: true},
			"glob":        {Type: "string", Desc: "Optional file glob filter, for example **/*.go."},
			"path":        {Type: "string", Desc: "Optional directory to search. Relative paths are resolved under the workspace root."},
			"output_mode": {Type: "string", Desc: "files_with_matches, content, or count. Defaults to files_with_matches."},
			"limit":       {Type: "integer", Desc: "Maximum number of result items. Defaults to 100."},
		}),
	}, nil
}

func (t *EditFileTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "edit_file",
		Desc: "Edit a text file under the current trusted workspace by exact string replacement. The old string must match once unless replace_all is true.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path":        {Type: "string", Desc: "File path to edit. Relative paths are resolved under the workspace root.", Required: true},
			"old_string":  {Type: "string", Desc: "Exact text to replace.", Required: true},
			"new_string":  {Type: "string", Desc: "Replacement text.", Required: true},
			"replace_all": {Type: "boolean", Desc: "Replace every occurrence instead of requiring a unique match."},
		}),
	}, nil
}

func (t *GlobTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fileToolError("invalid JSON arguments: %v", err), nil
	}
	searchRoot, err := t.resolveDir(args.Path)
	if err != nil {
		return fileToolError("%v", err), nil
	}
	limit := t.limit(args.Limit)
	matches, truncated, err := t.glob(searchRoot, args.Pattern, limit)
	if err != nil {
		return fileToolError("%v", err), nil
	}
	return fileToolJSON(map[string]any{"ok": true, "matches": matches, "count": len(matches), "truncated": truncated})
}

func (t *ReadFileTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Path     string `json:"path"`
		MaxBytes int64  `json:"max_bytes"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fileToolError("invalid JSON arguments: %v", err), nil
	}
	full, rel, err := t.resolveFile(args.Path)
	if err != nil {
		return fileToolError("%v", err), nil
	}
	maxBytes := t.maxBytes
	if args.MaxBytes > 0 && args.MaxBytes < maxBytes {
		maxBytes = args.MaxBytes
	}
	content, truncated, err := readTextFile(full, maxBytes)
	if err != nil {
		return fileToolError("%v", err), nil
	}
	return fileToolJSON(map[string]any{"ok": true, "path": rel, "content": content, "truncated": truncated})
}

func (t *GrepTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Pattern    string `json:"pattern"`
		Glob       string `json:"glob"`
		Path       string `json:"path"`
		OutputMode string `json:"output_mode"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fileToolError("invalid JSON arguments: %v", err), nil
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return fileToolError("invalid regex: %v", err), nil
	}
	searchRoot, err := t.resolveDir(args.Path)
	if err != nil {
		return fileToolError("%v", err), nil
	}
	mode := strings.TrimSpace(args.OutputMode)
	if mode == "" {
		mode = "files_with_matches"
	}
	limit := t.limit(args.Limit)
	results, truncated, err := t.grep(searchRoot, args.Glob, re, mode, limit)
	if err != nil {
		return fileToolError("%v", err), nil
	}
	return fileToolJSON(map[string]any{"ok": true, "mode": mode, "results": results, "count": len(results), "truncated": truncated})
}

func (t *EditFileTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fileToolError("invalid JSON arguments: %v", err), nil
	}
	full, rel, err := t.resolveFile(args.Path)
	if err != nil {
		return fileToolError("%v", err), nil
	}
	if args.OldString == "" {
		return fileToolError("old_string is required"), nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return fileToolError("read file failed: %v", err), nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return fileToolError("binary file edit skipped"), nil
	}
	text := string(data)
	count := strings.Count(text, args.OldString)
	if count == 0 {
		return fileToolError("old_string not found"), nil
	}
	if !args.ReplaceAll && count != 1 {
		return fileToolError("old_string matches %d times; provide a more specific old_string or set replace_all", count), nil
	}
	next := strings.Replace(text, args.OldString, args.NewString, 1)
	replacements := 1
	if args.ReplaceAll {
		next = strings.ReplaceAll(text, args.OldString, args.NewString)
		replacements = count
	}
	if err := os.WriteFile(full, []byte(next), 0o644); err != nil {
		return fileToolError("write file failed: %v", err), nil
	}
	return fileToolJSON(map[string]any{"ok": true, "path": rel, "replacements": replacements})
}

func (t fileToolBase) limit(value int) int {
	if value <= 0 || value > t.maxItems {
		return t.maxItems
	}
	return value
}

func (t fileToolBase) resolveDir(raw string) (string, error) {
	if len(t.allowedRoots) == 0 {
		return "", fmt.Errorf("file tools have no allowed roots configured")
	}
	base := t.allowedRoots[0]
	target := strings.TrimSpace(raw)
	if target == "" {
		target = base
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	target = filepath.Clean(target)
	if err := validateReadablePath(target, t.allowedRoots); err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("directory unavailable: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", raw)
	}
	return target, nil
}

func (t fileToolBase) resolveFile(raw string) (string, string, error) {
	if len(t.allowedRoots) == 0 {
		return "", "", fmt.Errorf("file tools have no allowed roots configured")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("path is required")
	}
	base := t.allowedRoots[0]
	target := raw
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	target = filepath.Clean(target)
	if err := validateReadablePath(target, t.allowedRoots); err != nil {
		return "", "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", "", fmt.Errorf("file unavailable: %w", err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("is a directory: %s", raw)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("symlinks not allowed")
	}
	rel := relativeToAnyRoot(target, t.allowedRoots)
	return target, rel, nil
}

func (t fileToolBase) glob(searchRoot, pattern string, limit int) ([]string, bool, error) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return nil, false, fmt.Errorf("pattern is required")
	}
	type item struct {
		path string
		mod  int64
	}
	var items []item
	truncated := false
	err := filepath.WalkDir(searchRoot, func(full string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if shouldSkipFileToolDir(d) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel := relativeToAnyRoot(full, t.allowedRoots)
		ok, err := matchGlob(pattern, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		mod := int64(0)
		if info, err := d.Info(); err == nil {
			mod = info.ModTime().UnixNano()
		}
		if len(items) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		items = append(items, item{path: filepath.ToSlash(rel), mod: mod})
		return nil
	})
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].mod == items[j].mod {
			return items[i].path < items[j].path
		}
		return items[i].mod > items[j].mod
	})
	matches := make([]string, 0, len(items))
	for _, item := range items {
		matches = append(matches, item.path)
	}
	return matches, truncated, err
}

func (t fileToolBase) grep(searchRoot, glob string, re *regexp.Regexp, mode string, limit int) ([]map[string]any, bool, error) {
	if mode != "files_with_matches" && mode != "content" && mode != "count" {
		return nil, false, fmt.Errorf("unsupported output_mode: %s", mode)
	}
	var results []map[string]any
	truncated := false
	err := filepath.WalkDir(searchRoot, func(full string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if shouldSkipFileToolDir(d) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel := relativeToAnyRoot(full, t.allowedRoots)
		if strings.TrimSpace(glob) != "" {
			ok, err := matchGlob(filepath.ToSlash(glob), filepath.ToSlash(rel))
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		content, _, err := readTextFile(full, t.maxBytes)
		if err != nil {
			return nil
		}
		lines := strings.Split(content, "\n")
		matches := 0
		for idx, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			matches++
			if mode == "content" {
				if len(results) >= limit {
					truncated = true
					return filepath.SkipAll
				}
				results = append(results, map[string]any{"path": filepath.ToSlash(rel), "line": idx + 1, "text": line})
			}
		}
		if matches == 0 || mode == "content" {
			return nil
		}
		if len(results) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		row := map[string]any{"path": filepath.ToSlash(rel)}
		if mode == "count" {
			row["count"] = matches
		}
		results = append(results, row)
		return nil
	})
	return results, truncated, err
}

func validateReadablePath(target string, allowedRoots []string) error {
	target = filepath.Clean(target)
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		resolvedTarget = target
	}
	for _, root := range allowedRoots {
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			resolvedRoot = root
		}
		if isPathUnderRoot(resolvedTarget, resolvedRoot) || isPathUnderRoot(target, filepath.Clean(root)) {
			return nil
		}
	}
	return fmt.Errorf("path outside allowed roots")
}

func relativeToAnyRoot(full string, roots []string) string {
	for _, root := range roots {
		if rel, err := filepath.Rel(root, full); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(full)
}

func readTextFile(full string, maxBytes int64) (string, bool, error) {
	info, err := os.Stat(full)
	if err != nil {
		return "", false, err
	}
	if info.Size() > maxBytes {
		data, err := os.ReadFile(full)
		if err != nil {
			return "", false, err
		}
		if bytes.IndexByte(data[:min(len(data), int(maxBytes))], 0) >= 0 {
			return "", false, fmt.Errorf("binary file skipped")
		}
		return string(data[:maxBytes]), true, nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", false, err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", false, fmt.Errorf("binary file skipped")
	}
	return string(data), false, nil
}

func shouldSkipFileToolDir(d os.DirEntry) bool {
	if !d.IsDir() {
		return false
	}
	switch d.Name() {
	case ".git", "node_modules", "dist", "build":
		return true
	default:
		return false
	}
}

func matchGlob(pattern, name string) (bool, error) {
	for _, expanded := range expandBraceGlob(pattern) {
		ok, err := matchGlobSegments(strings.Split(expanded, "/"), strings.Split(name, "/"))
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func matchGlobSegments(pattern, name []string) (bool, error) {
	if len(pattern) == 0 {
		return len(name) == 0, nil
	}
	if pattern[0] == "**" {
		if ok, err := matchGlobSegments(pattern[1:], name); ok || err != nil {
			return ok, err
		}
		for i := range name {
			if ok, err := matchGlobSegments(pattern[1:], name[i+1:]); ok || err != nil {
				return ok, err
			}
		}
		return false, nil
	}
	if len(name) == 0 {
		return false, nil
	}
	ok, err := path.Match(pattern[0], name[0])
	if err != nil || !ok {
		return ok, err
	}
	return matchGlobSegments(pattern[1:], name[1:])
}

func expandBraceGlob(pattern string) []string {
	start := strings.Index(pattern, "{")
	end := strings.Index(pattern, "}")
	if start < 0 || end < 0 || end < start {
		return []string{pattern}
	}
	prefix, suffix := pattern[:start], pattern[end+1:]
	var out []string
	for _, part := range strings.Split(pattern[start+1:end], ",") {
		for _, expanded := range expandBraceGlob(prefix + part + suffix) {
			out = append(out, expanded)
		}
	}
	return out
}

func fileToolJSON(value map[string]any) (string, error) {
	raw, _ := json.Marshal(value)
	return string(raw), nil
}

func fileToolError(format string, args ...any) string {
	raw, _ := json.Marshal(map[string]any{"ok": false, "error": fmt.Sprintf(format, args...)})
	return string(raw)
}
