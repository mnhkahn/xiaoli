package admin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
)

func TestFileSkillBackendListReadsOnlyEnabledMetadata(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "holiday", "假期查询", "holiday body")
	writeTestSkill(t, root, "mo", "书法查询", "mo body")
	writeTestSkill(t, root, "disabled", "disabled skill", "disabled body")

	backend, err := newFileSkillBackend(fileSkillBackendConfig{
		Roots:    []string{root},
		Enabled:  []string{"mo", "holiday"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("newFileSkillBackend() error = %v", err)
	}

	got, err := backend.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(List()) = %d, want 2: %#v", len(got), got)
	}
	if got[0].Name != "holiday" || got[0].Description != "假期查询" {
		t.Fatalf("first skill = %#v, want holiday metadata", got[0])
	}
	if got[1].Name != "mo" || got[1].Description != "书法查询" {
		t.Fatalf("second skill = %#v, want mo metadata", got[1])
	}
}

func TestFileSkillBackendGetLoadsSkillBodyOnDemand(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "holiday", "假期查询", "initial body")

	backend, err := newFileSkillBackend(fileSkillBackendConfig{
		Roots:    []string{root},
		Enabled:  []string{"*"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("newFileSkillBackend() error = %v", err)
	}
	if _, err := backend.List(context.Background()); err != nil {
		t.Fatalf("List() error = %v", err)
	}

	writeTestSkill(t, root, "holiday", "假期查询", "updated body")
	got, err := backend.Get(context.Background(), "holiday")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "holiday" || got.Content != "updated body\n" {
		t.Fatalf("Get() = %#v, want updated body loaded from disk", got)
	}
}

func TestFileSkillBackendGetRejectsDisabledSkill(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "holiday", "假期查询", "holiday body")
	writeTestSkill(t, root, "mo", "书法查询", "mo body")

	backend, err := newFileSkillBackend(fileSkillBackendConfig{
		Roots:    []string{root},
		Enabled:  []string{"holiday"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("newFileSkillBackend() error = %v", err)
	}

	if _, err := backend.Get(context.Background(), "mo"); err == nil {
		t.Fatal("Get(disabled) error = nil, want error")
	}
}

func TestFileSkillBackendGetEnforcesMaxBytes(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "holiday", "假期查询", "body is too long")

	backend, err := newFileSkillBackend(fileSkillBackendConfig{
		Roots:    []string{root},
		Enabled:  []string{"*"},
		MaxBytes: 32,
	})
	if err != nil {
		t.Fatalf("newFileSkillBackend() error = %v", err)
	}

	if _, err := backend.Get(context.Background(), "holiday"); err == nil {
		t.Fatal("Get(oversize) error = nil, want error")
	}
}

func TestFileSkillBackendImplementsEinoSkillBackend(t *testing.T) {
	var _ einoskill.Backend = (*fileSkillBackend)(nil)
}

func TestLoadConfigSetsDefaultSkillConfig(t *testing.T) {
	t.Setenv("XIAOLI_SKILL_ROOTS", "")
	t.Setenv("XIAOLI_ENABLED_SKILLS", "")
	t.Setenv("XIAOLI_SKILL_MAX_BYTES", "")

	cfg := LoadConfig()
	if len(cfg.SkillRoots) != 1 || cfg.SkillRoots[0] != "/opt/xiaoli/skills" {
		t.Fatalf("SkillRoots = %#v, want /opt/xiaoli/skills", cfg.SkillRoots)
	}
	if len(cfg.EnabledSkills) != 1 || cfg.EnabledSkills[0] != "*" {
		t.Fatalf("EnabledSkills = %#v, want *", cfg.EnabledSkills)
	}
	if cfg.SkillMaxBytes != defaultSkillMaxBytes {
		t.Fatalf("SkillMaxBytes = %d, want %d", cfg.SkillMaxBytes, defaultSkillMaxBytes)
	}
}

func writeTestSkill(t *testing.T, root, name, desc, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
}
