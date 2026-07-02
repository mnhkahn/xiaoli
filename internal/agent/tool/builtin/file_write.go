package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type FileWriteConfig struct {
	AllowedRoots []string
}

type FileWriteTool struct {
	allowedRoots []string
}

func NewFileWriteTool(cfg FileWriteConfig) tool.BaseTool {
	return &FileWriteTool{allowedRoots: cleanAbsoluteRoots(cfg.AllowedRoots)}
}

func (t *FileWriteTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "file_write",
		Desc: "Write text content to a local file under trusted directories. Use this before tools that need a real file path, such as PDF generation and channel_send.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"filename": {
				Type: "string",
				Desc: "Safe basename for the file. Used with the default artifact directory when file_path is not provided.",
			},
			"file_path": {
				Type: "string",
				Desc: "Optional absolute file path. Must be under trusted directories.",
			},
			"content": {
				Type:     "string",
				Desc:     "Text content to write.",
				Required: true,
			},
			"mime_type": {
				Type: "string",
				Desc: "Optional MIME type, for example text/markdown or text/html.",
			},
		}),
	}, nil
}

func (t *FileWriteTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Filename string `json:"filename"`
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
		MIMEType string `json:"mime_type"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fileWriteError("invalid JSON arguments: %v", err), nil
	}
	path, displayName, err := t.resolvePath(ctx, args.FilePath, args.Filename)
	if err != nil {
		return fileWriteError("%v", err), nil
	}
	if err := t.write(path, args.Content); err != nil {
		return fileWriteError("%v", err), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fileWriteError("stat written file failed: %v", err), nil
	}
	mimeType := strings.TrimSpace(args.MIMEType)
	if mimeType == "" {
		mimeType = "text/plain"
	}
	raw, _ := json.Marshal(map[string]any{
		"ok":           true,
		"file_path":    path,
		"display_name": displayName,
		"mime_type":    mimeType,
		"size":         info.Size(),
		"dir":          filepath.Dir(path),
	})
	return string(raw), nil
}

func (t *FileWriteTool) resolvePath(ctx context.Context, rawPath, rawFilename string) (string, string, error) {
	if len(t.allowedRoots) == 0 {
		return "", "", fmt.Errorf("file_write has no allowed directories configured")
	}
	if strings.TrimSpace(rawPath) != "" {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if !filepath.IsAbs(path) {
			return "", "", fmt.Errorf("file_path must be absolute")
		}
		if err := validateWritablePath(path, t.allowedRoots); err != nil {
			return "", "", err
		}
		return path, filepath.Base(path), nil
	}
	filename := strings.TrimSpace(rawFilename)
	if filename == "" {
		return "", "", fmt.Errorf("filename is required when file_path is not provided")
	}
	if filename != filepath.Base(filename) || filename == "." || filename == string(filepath.Separator) {
		return "", "", fmt.Errorf("filename must be a safe basename")
	}
	root := t.allowedRoots[0]
	session := sanitizeArtifactSegment(contextSessionID(ctx))
	path := filepath.Join(root, "xiaoli-artifacts", session, filename)
	if err := validateWritablePath(path, t.allowedRoots); err != nil {
		return "", "", err
	}
	return path, filename, nil
}

func (t *FileWriteTool) write(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directory failed: %w", err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks not allowed")
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat target failed: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write file failed: %w", err)
	}
	return nil
}

func contextSessionID(ctx context.Context) string {
	if value, _ := ctx.Value(SubAgentParentKey).(string); value != "" {
		return value
	}
	if conv := recentImageConversation(ctx); conv != "" {
		return conv
	}
	return "default"
}

var artifactSegmentRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeArtifactSegment(value string) string {
	value = strings.Trim(artifactSegmentRe.ReplaceAllString(value, "_"), "._-")
	if value == "" {
		return "default"
	}
	if len(value) > 80 {
		return value[:80]
	}
	return value
}

func cleanAbsoluteRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" || !filepath.IsAbs(root) || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
	}
	return out
}

func validateWritablePath(path string, allowedRoots []string) error {
	parent := filepath.Dir(filepath.Clean(path))
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if os.IsNotExist(err) {
			resolvedParent = parent
		} else {
			return fmt.Errorf("resolve parent path failed: %w", err)
		}
	}
	for _, root := range allowedRoots {
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			resolvedRoot = root
		}
		if isPathUnderRoot(resolvedParent, resolvedRoot) || isPathUnderRoot(parent, filepath.Clean(root)) {
			return nil
		}
	}
	return fmt.Errorf("file_path not in allowed directories")
}

func isPathUnderRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func fileWriteError(format string, args ...any) string {
	raw, _ := json.Marshal(map[string]any{"ok": false, "error": fmt.Sprintf(format, args...)})
	return string(raw)
}
