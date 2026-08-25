package runtime

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mnhkahn/gogogo/logger"
)

const (
	modelToolOutputMaxBytes = 12 * 1024
	modelToolOutputMaxLines = 300
)

// toolOutputStore retains raw results outside the model transcript. The model
// receives a bounded, deterministic projection instead.
type toolOutputStore struct {
	dir string
	mu  sync.Mutex
	ids map[[32]byte]string
}

func newToolOutputStore(dataDir string) *toolOutputStore {
	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	return &toolOutputStore{dir: filepath.Join(dataDir, "tool-output"), ids: make(map[[32]byte]string)}
}

func (s *toolOutputStore) store(raw string) (string, error) {
	if s == nil || raw == "" {
		return "", nil
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids[sha256.Sum256([]byte(raw))] = id
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(s.dir, id+".txt"), []byte(raw), 0600); err != nil {
		return "", err
	}
	return id, nil
}

func (s *toolOutputStore) idFor(raw string) string {
	if s == nil || raw == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ids[sha256.Sum256([]byte(raw))]
}

func outputIDFromProjection(text string) string {
	const marker = "output_id="
	idx := strings.LastIndex(text, marker)
	if idx < 0 || len(text) < idx+len(marker)+24 {
		return ""
	}
	id := text[idx+len(marker) : idx+len(marker)+24]
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return ""
		}
	}
	return id
}

func (s *toolOutputStore) read(id string, startLine, maxLines int) (string, error) {
	if s == nil || len(id) != 24 {
		return "", fmt.Errorf("未知的 output_id")
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", fmt.Errorf("无效的 output_id")
		}
	}
	b, err := os.ReadFile(filepath.Join(s.dir, id+".txt"))
	if err != nil {
		return "", fmt.Errorf("读取工具输出失败：%w", err)
	}
	if startLine < 1 {
		startLine = 1
	}
	if maxLines <= 0 || maxLines > 200 {
		maxLines = 200
	}
	lines := strings.Split(string(b), "\n")
	if startLine > len(lines) {
		return fmt.Sprintf("output_id=%s 没有第 %d 行（共 %d 行）", id, startLine, len(lines)), nil
	}
	end := startLine - 1 + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	return fmt.Sprintf("output_id=%s lines=%d-%d/%d\n%s", id, startLine, end, len(lines), strings.Join(lines[startLine-1:end], "\n")), nil
}

func projectToolOutput(name, raw string, store *toolOutputStore) string {
	if raw == "" {
		return raw
	}
	id, err := store.store(raw)
	if err != nil {
		logger.Infof("tool output retain failed: tool=%s bytes=%d err=%v", name, len(raw), err)
	}
	if len(raw) <= modelToolOutputMaxBytes && countLines(raw) <= modelToolOutputMaxLines {
		return raw
	}
	preview := boundedToolPreview(raw, modelToolOutputMaxBytes, modelToolOutputMaxLines)
	ref := ""
	if id != "" {
		ref = fmt.Sprintf("\n\n[完整输出已保留；output_id=%s。需要细节时请用更精确的命令、搜索或按范围读取。]", id)
	}
	return preview + ref
}

func countLines(s string) int { return strings.Count(s, "\n") + 1 }

func boundedToolPreview(raw string, maxBytes, maxLines int) string {
	lines := strings.Split(raw, "\n")
	if len(lines) > maxLines {
		head := maxLines / 2
		tail := maxLines - head
		lines = append(append(lines[:head], "... [中间输出已省略] ..."), lines[len(lines)-tail:]...)
	}
	text := strings.Join(lines, "\n")
	if len(text) <= maxBytes {
		return text
	}
	head := maxBytes / 2
	tail := maxBytes - head
	return cutUTF8Prefix(text, head) + "\n... [中间输出已省略] ...\n" + cutUTF8Suffix(text, tail)
}

func cutUTF8Prefix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

func cutUTF8Suffix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
