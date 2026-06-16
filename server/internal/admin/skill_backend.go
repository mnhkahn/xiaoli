package admin

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"gopkg.in/yaml.v3"
)

const defaultSkillMaxBytes int64 = 64 * 1024

type fileSkillBackendConfig struct {
	Roots    []string
	Enabled  []string
	MaxBytes int64
}

type fileSkillBackend struct {
	mu       sync.RWMutex
	maxBytes int64
	enabled  map[string]bool
	all      bool
	skills   map[string]indexedSkill
}

type indexedSkill struct {
	einoskill.FrontMatter
	path string
	dir  string
}

func newFileSkillBackend(cfg fileSkillBackendConfig) (*fileSkillBackend, error) {
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultSkillMaxBytes
	}
	enabled, all := skillAllowlist(cfg.Enabled)
	backend := &fileSkillBackend{
		maxBytes: maxBytes,
		enabled:  enabled,
		all:      all,
		skills:   map[string]indexedSkill{},
	}
	if err := backend.scan(cfg.Roots); err != nil {
		return nil, err
	}
	return backend, nil
}

func skillAllowlist(items []string) (map[string]bool, bool) {
	allowed := map[string]bool{}
	if len(items) == 0 {
		return allowed, true
	}
	for _, item := range items {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		if name == "*" {
			return map[string]bool{}, true
		}
		allowed[name] = true
	}
	if len(allowed) == 0 {
		return allowed, true
	}
	return allowed, false
}

func (b *fileSkillBackend) scan(roots []string) error {
	next := map[string]indexedSkill{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("scan skill root %s: %w", root, err)
		}
		if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil {
			if err := b.indexSkill(next, root); err != nil {
				return err
			}
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
				if err := b.indexSkill(next, dir); err != nil {
					return err
				}
			}
		}
	}

	b.mu.Lock()
	b.skills = next
	b.mu.Unlock()
	return nil
}

func (b *fileSkillBackend) indexSkill(dst map[string]indexedSkill, dir string) error {
	path := filepath.Join(dir, "SKILL.md")
	frontMatter, err := readSkillFrontMatter(path)
	if err != nil {
		return err
	}
	if frontMatter.Name == "" {
		return fmt.Errorf("skill %s: missing name", path)
	}
	if !b.all && !b.enabled[frontMatter.Name] {
		return nil
	}
	dst[frontMatter.Name] = indexedSkill{
		FrontMatter: frontMatter,
		path:        path,
		dir:         dir,
	}
	return nil
}

func (b *fileSkillBackend) List(context.Context) ([]einoskill.FrontMatter, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.skills))
	for name := range b.skills {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]einoskill.FrontMatter, 0, len(names))
	for _, name := range names {
		out = append(out, b.skills[name].FrontMatter)
	}
	return out, nil
}

func (b *fileSkillBackend) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.skills)
}

func (b *fileSkillBackend) Get(_ context.Context, name string) (einoskill.Skill, error) {
	name = strings.TrimSpace(name)
	b.mu.RLock()
	item, ok := b.skills[name]
	b.mu.RUnlock()
	if !ok {
		return einoskill.Skill{}, fmt.Errorf("skill %q is not enabled or does not exist", name)
	}

	raw, err := readLimitedFile(item.path, b.maxBytes)
	if err != nil {
		return einoskill.Skill{}, err
	}
	frontMatter, body, err := parseSkillFile(raw, item.path)
	if err != nil {
		return einoskill.Skill{}, err
	}
	if frontMatter.Name == "" {
		frontMatter.Name = item.Name
	}
	return einoskill.Skill{
		FrontMatter:   frontMatter,
		Content:       body,
		BaseDirectory: item.dir,
	}, nil
}

func readLimitedFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read skill %s: %w", path, err)
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read skill %s: %w", path, err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("skill %s exceeds max size %d bytes", path, maxBytes)
	}
	return raw, nil
}

func readSkillFrontMatter(path string) (einoskill.FrontMatter, error) {
	f, err := os.Open(path)
	if err != nil {
		return einoskill.FrontMatter{}, fmt.Errorf("read skill metadata %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024), 64*1024)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return einoskill.FrontMatter{}, fmt.Errorf("skill %s: missing frontmatter", path)
	}

	var front bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			return decodeSkillFrontMatter(front.Bytes(), path)
		}
		front.WriteString(line)
		front.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return einoskill.FrontMatter{}, fmt.Errorf("read skill metadata %s: %w", path, err)
	}
	return einoskill.FrontMatter{}, fmt.Errorf("skill %s: unterminated frontmatter", path)
}

func parseSkillFile(raw []byte, path string) (einoskill.FrontMatter, string, error) {
	parts := bytes.SplitN(raw, []byte("---"), 3)
	if len(parts) != 3 || strings.TrimSpace(string(parts[0])) != "" {
		return einoskill.FrontMatter{}, "", fmt.Errorf("skill %s: missing frontmatter", path)
	}
	frontMatter, err := decodeSkillFrontMatter(parts[1], path)
	if err != nil {
		return einoskill.FrontMatter{}, "", err
	}
	body := strings.TrimLeft(string(parts[2]), "\r\n")
	return frontMatter, body, nil
}

func decodeSkillFrontMatter(raw []byte, path string) (einoskill.FrontMatter, error) {
	var frontMatter einoskill.FrontMatter
	if err := yaml.Unmarshal(raw, &frontMatter); err != nil {
		return einoskill.FrontMatter{}, fmt.Errorf("parse skill frontmatter %s: %w", path, err)
	}
	return frontMatter, nil
}
