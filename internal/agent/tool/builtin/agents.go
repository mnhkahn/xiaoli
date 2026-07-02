package builtin

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type AgentKind string

const (
	AgentNormal AgentKind = "normal"
	AgentFork   AgentKind = "fork"
)

type AgentDefinition struct {
	Name          string    `yaml:"name"`
	Description   string    `yaml:"description"`
	Kind          AgentKind `yaml:"type"`
	Source        string    // "hardcoded" | "file"
	SystemPrompt  string    `yaml:"-"` // body after frontmatter
	MaxSteps      int       `yaml:"max_steps"`
	AllowTools    bool      `yaml:"allow_tools"`
	DisabledTools []string  `yaml:"disallowedTools,omitempty"`
}

func (d AgentDefinition) ToSubAgentSpec() SubAgentSpec {
	return SubAgentSpec{
		Name:          d.Name,
		Description:   d.Description,
		SystemPrompt:  d.SystemPrompt,
		MaxSteps:      d.MaxSteps,
		AllowTools:    d.AllowTools,
		IsFork:        d.Kind == AgentFork,
		DisabledTools: d.DisabledTools,
	}
}

type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]AgentDefinition
	alias  map[string]string
}

func NewAgentRegistry() *AgentRegistry {
	r := &AgentRegistry{
		agents: make(map[string]AgentDefinition),
		alias:  map[string]string{},
	}
	r.registerBuiltin()
	return r
}

func (r *AgentRegistry) registerBuiltin() {
	// Only mechanism-critical agents are hardcoded:
	//   - general: default fallback / main agent type
	//   - fork: runtime-level fork path, cannot be expressed as a Markdown agent
	// Other agent types (explore, researcher, etc.) live as files under agents/.
	r.agents["general"] = AgentDefinition{
		Name:         "general",
		Description:  "通用多步骤任务执行，适合实现功能、重构或修复",
		Kind:         AgentNormal,
		Source:       "hardcoded",
		SystemPrompt: "你是一个通用任务执行者。按步骤完成任务，提供清晰的输出。如果需要修改代码，请直接输出修改后的代码内容。",
		MaxSteps:     15,
		AllowTools:   true,
	}
	r.agents["fork"] = AgentDefinition{
		Name:        "fork",
		Description: "复制当前上下文执行并行任务，适合依赖当前长上下文的验证、多路线推理",
		Kind:        AgentFork,
		Source:      "hardcoded",
		MaxSteps:    10,
		AllowTools:  true,
	}
	r.alias["general-purpose"] = "general"
}

func (r *AgentRegistry) Resolve(name string) (AgentDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if def, ok := r.agents[name]; ok {
		return def, true
	}
	if target, ok := r.alias[name]; ok {
		def, ok := r.agents[target]
		return def, ok
	}
	return AgentDefinition{}, false
}

func (r *AgentRegistry) Get(name string) (AgentDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.agents[name]
	return def, ok
}

func (r *AgentRegistry) List() map[string]AgentDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]AgentDefinition, len(r.agents))
	for k, v := range r.agents {
		out[k] = v
	}
	return out
}

func (r *AgentRegistry) ListSpecs() map[string]SubAgentSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	specs := make(map[string]SubAgentSpec, len(r.agents)+len(r.alias))
	for name, def := range r.agents {
		specs[name] = def.ToSubAgentSpec()
	}
	for alias, target := range r.alias {
		if def, ok := r.agents[target]; ok {
			specs[alias] = def.ToSubAgentSpec()
		}
	}
	return specs
}

func (r *AgentRegistry) IsBuiltinName(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.agents[name]; ok {
		return true
	}
	if _, ok := r.alias[name]; ok {
		return true
	}
	return false
}

func (r *AgentRegistry) AddFromFile(def AgentDefinition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.agents[def.Name]; ok {
		return nil // builtin dominates
	}
	if _, ok := r.alias[def.Name]; ok {
		return nil // reserved alias
	}
	def.Source = "file"
	r.agents[def.Name] = def
	return nil
}

func (r *AgentRegistry) LoadAgentFiles(roots []string) error {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			path := filepath.Join(root, entry.Name())
			def, err := parseAgentFile(path)
			if err != nil {
				continue
			}
			r.AddFromFile(def)
		}
	}
	return nil
}

func parseAgentFile(path string) (AgentDefinition, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return AgentDefinition{}, fmt.Errorf("read agent file %s: %w", path, err)
	}
	parts := bytes.SplitN(raw, []byte("---"), 3)
	if len(parts) != 3 || strings.TrimSpace(string(parts[0])) != "" {
		return AgentDefinition{}, fmt.Errorf("agent %s: missing frontmatter", path)
	}
	var def AgentDefinition
	if err := yaml.Unmarshal(parts[1], &def); err != nil {
		return AgentDefinition{}, fmt.Errorf("agent %s: parse frontmatter: %w", path, err)
	}
	def.SystemPrompt = strings.TrimLeft(string(parts[2]), "\r\n")
	if def.Name == "" {
		return AgentDefinition{}, fmt.Errorf("agent %s: name is required", path)
	}
	return def, nil
}

// FileAgentRoots returns the default search paths for Markdown agent files.
func FileAgentRoots() []string {
	return []string{
		"/opt/xiaoli/agents",
		"agents",
	}
}
