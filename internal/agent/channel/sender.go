package channel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SendTarget identifies where a message should be sent.
// Exactly one field should be populated based on Channel type.
type SendTarget struct {
	Channel          Type   // The channel type
	UserID           string // Lark: open_id, WeChat: user id
	ChatID           string // Lark: chat_id
	DeviceID         string // ESP32: device id
	ReplyToMessageID string // Original message id for replies
	ContextToken     string // WeChat context token for threaded bot replies
}

// Attachment represents a file attachment to be sent
type Attachment struct {
	Path        string // Local filesystem path (must be absolute)
	DisplayName string // User-visible filename
	MIMEType    string // Detected MIME type
	Size        int64  // File size in bytes
}

// Sender interface implemented by each channel type
type Sender interface {
	// SendText sends a plain text message
	SendText(ctx context.Context, target SendTarget, text string) error

	// SendAttachment sends a file attachment with optional caption
	SendAttachment(ctx context.Context, target SendTarget, attachment Attachment, caption string) error
}

type contextKey string

const targetContextKey contextKey = "sendTarget"

// WithSendTarget attaches a SendTarget to the context for use by channel_send tool
func WithSendTarget(ctx context.Context, target SendTarget) context.Context {
	return context.WithValue(ctx, targetContextKey, target)
}

// SendTargetFromContext retrieves the SendTarget from context
func SendTargetFromContext(ctx context.Context) (SendTarget, bool) {
	target, ok := ctx.Value(targetContextKey).(SendTarget)
	return target, ok
}

// ValidatePath validates that a path is safe for file operations.
// Only allows paths under trusted roots, no symlinks.
func ValidatePath(path string, allowedRoots []string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute")
	}
	cleanPath := filepath.Clean(path)

	// No symlinks
	info, err := os.Lstat(cleanPath)
	if err != nil {
		return fmt.Errorf("stat failed: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlinks not allowed")
	}

	// Must be a regular file
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}

	resolvedPath, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		return fmt.Errorf("resolve path failed: %w", err)
	}
	if !isPathAllowed(resolvedPath, allowedRoots) {
		return fmt.Errorf("path not in allowed directories")
	}

	return nil
}

func isPathAllowed(path string, allowedRoots []string) bool {
	for _, root := range allowedRoots {
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		if !filepath.IsAbs(cleanRoot) {
			continue
		}
		resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
		if err != nil {
			resolvedRoot = cleanRoot
		}
		rel, err := filepath.Rel(resolvedRoot, path)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}
