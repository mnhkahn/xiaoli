package skill

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

	backend, err := NewFileBackend(BackendConfig{
		Roots:    []string{root},
		Enabled:  []string{"mo", "holiday"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("NewFileBackend() error = %v", err)
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

	backend, err := NewFileBackend(BackendConfig{
		Roots:    []string{root},
		Enabled:  []string{"*"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("NewFileBackend() error = %v", err)
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

	backend, err := NewFileBackend(BackendConfig{
		Roots:    []string{root},
		Enabled:  []string{"holiday"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("NewFileBackend() error = %v", err)
	}

	if _, err := backend.Get(context.Background(), "mo"); err == nil {
		t.Fatal("Get(disabled) error = nil, want error")
	}
}

func TestFileSkillBackendSkipsInvalidSkillMetadata(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "holiday", "假期查询", "holiday body")
	badDir := filepath.Join(root, "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(bad) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "SKILL.md"), []byte("# Bad Skill\n\nmissing frontmatter\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(bad) error = %v", err)
	}

	backend, err := NewFileBackend(BackendConfig{
		Roots:    []string{root},
		Enabled:  []string{"*"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("NewFileBackend() error = %v", err)
	}
	got, err := backend.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "holiday" {
		t.Fatalf("List() = %#v, want only valid holiday skill", got)
	}
}

func TestFileSkillBackendGetEnforcesMaxBytes(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "holiday", "假期查询", "body is too long")

	backend, err := NewFileBackend(BackendConfig{
		Roots:    []string{root},
		Enabled:  []string{"*"},
		MaxBytes: 32,
	})
	if err != nil {
		t.Fatalf("NewFileBackend() error = %v", err)
	}

	if _, err := backend.Get(context.Background(), "holiday"); err == nil {
		t.Fatal("Get(oversize) error = nil, want error")
	}
}

func TestFileSkillBackendImplementsEinoSkillBackend(t *testing.T) {
	var _ einoskill.Backend = (*Backend)(nil)
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
