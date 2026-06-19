package skill

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

const DefaultMaxBytes int64 = 64 * 1024

type BackendConfig struct {
	Roots    []string
	Enabled  []string
	MaxBytes int64
}

type Backend struct {
	mu       sync.RWMutex
	maxBytes int64
	enabled  map[string]bool
	all      bool
	skills   map[string]indexedSkill
}

type SkillFrontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
}

type indexedSkill struct {
	SkillFrontMatter
	fm   einoskill.FrontMatter
	path string
	dir  string
}

func NewFileBackend(cfg BackendConfig) (*Backend, error) {
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	enabled, all := skillAllowlist(cfg.Enabled)
	backend := &Backend{
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

func (b *Backend) scan(roots []string) error {
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

func (b *Backend) indexSkill(dst map[string]indexedSkill, dir string) error {
	path := filepath.Join(dir, "SKILL.md")
	sfm, err := readSkillFrontMatter(path)
	if err != nil {
		return err
	}
	if sfm.Name == "" {
		return fmt.Errorf("skill %s: missing name", path)
	}
	if !b.all && !b.enabled[sfm.Name] {
		return nil
	}
	dst[sfm.Name] = indexedSkill{
		SkillFrontMatter: sfm,
		fm: einoskill.FrontMatter{
			Name:        sfm.Name,
			Description: sfm.Description,
		},
		path: path,
		dir:  dir,
	}
	return nil
}

func (b *Backend) List(context.Context) ([]einoskill.FrontMatter, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.skills))
	for name := range b.skills {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]einoskill.FrontMatter, 0, len(names))
	for _, name := range names {
		out = append(out, b.skills[name].fm)
	}
	return out, nil
}

func (b *Backend) ListVersions() []SkillFrontMatter {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.skills))
	for name := range b.skills {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]SkillFrontMatter, 0, len(names))
	for _, name := range names {
		out = append(out, b.skills[name].SkillFrontMatter)
	}
	return out
}

func (b *Backend) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.skills)
}

func (b *Backend) Get(_ context.Context, name string) (einoskill.Skill, error) {
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
	sfm, body, err := parseSkillFile(raw, item.path)
	if err != nil {
		return einoskill.Skill{}, err
	}
	if sfm.Name == "" {
		sfm.Name = item.Name
	}
	return einoskill.Skill{
		FrontMatter: einoskill.FrontMatter{
			Name:        sfm.Name,
			Description: sfm.Description,
		},
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

func readSkillFrontMatter(path string) (SkillFrontMatter, error) {
	f, err := os.Open(path)
	if err != nil {
		return SkillFrontMatter{}, fmt.Errorf("read skill metadata %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024), 64*1024)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return SkillFrontMatter{}, fmt.Errorf("skill %s: missing frontmatter", path)
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
		return SkillFrontMatter{}, fmt.Errorf("read skill metadata %s: %w", path, err)
	}
	return SkillFrontMatter{}, fmt.Errorf("skill %s: unterminated frontmatter", path)
}

func parseSkillFile(raw []byte, path string) (SkillFrontMatter, string, error) {
	parts := bytes.SplitN(raw, []byte("---"), 3)
	if len(parts) != 3 || strings.TrimSpace(string(parts[0])) != "" {
		return SkillFrontMatter{}, "", fmt.Errorf("skill %s: missing frontmatter", path)
	}
	frontMatter, err := decodeSkillFrontMatter(parts[1], path)
	if err != nil {
		return SkillFrontMatter{}, "", err
	}
	body := strings.TrimLeft(string(parts[2]), "\r\n")
	return frontMatter, body, nil
}

func decodeSkillFrontMatter(raw []byte, path string) (SkillFrontMatter, error) {
	var sfm SkillFrontMatter
	if err := yaml.Unmarshal(raw, &sfm); err != nil {
		return SkillFrontMatter{}, fmt.Errorf("parse skill frontmatter %s: %w", path, err)
	}
	return sfm, nil
}
