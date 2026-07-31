package admin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareAgentsDirectorySeedsAndLinks(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, "data", ".agents")
	homeAgentsDir := filepath.Join(root, "home", ".agents")
	seedSkillsDir := filepath.Join(root, "image", "skills")
	seedFile := filepath.Join(seedSkillsDir, "trello", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(seedFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedFile, []byte("seed skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PrepareAgentsDirectory(agentsDir, homeAgentsDir, seedSkillsDir); err != nil {
		t.Fatalf("PrepareAgentsDirectory() error = %v", err)
	}
	assertSymlinkTarget(t, homeAgentsDir, agentsDir)
	persistedFile := filepath.Join(agentsDir, "skills", "trello", "SKILL.md")
	if data, err := os.ReadFile(persistedFile); err != nil || string(data) != "seed skill" {
		t.Fatalf("persisted seed = %q, %v", data, err)
	}

	if err := os.WriteFile(persistedFile, []byte("updated skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PrepareAgentsDirectory(agentsDir, homeAgentsDir, seedSkillsDir); err != nil {
		t.Fatalf("second PrepareAgentsDirectory() error = %v", err)
	}
	if data, err := os.ReadFile(persistedFile); err != nil || string(data) != "updated skill" {
		t.Fatalf("persisted update = %q, %v; seed must not overwrite it", data, err)
	}
}

func TestPrepareAgentsDirectoryMigratesExistingHome(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, "data", ".agents")
	homeAgentsDir := filepath.Join(root, "home", ".agents")
	seedSkillsDir := filepath.Join(root, "image", "skills")
	homeSkill := filepath.Join(homeAgentsDir, "skills", "trello", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(homeSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homeSkill, []byte("newer home skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(seedSkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := PrepareAgentsDirectory(agentsDir, homeAgentsDir, seedSkillsDir); err != nil {
		t.Fatalf("PrepareAgentsDirectory() error = %v", err)
	}
	assertSymlinkTarget(t, homeAgentsDir, agentsDir)
	if data, err := os.ReadFile(filepath.Join(agentsDir, "skills", "trello", "SKILL.md")); err != nil || string(data) != "newer home skill" {
		t.Fatalf("migrated skill = %q, %v", data, err)
	}
	if info, err := os.Stat(homeAgentsDir + ".pre-persistent"); err != nil || !info.IsDir() {
		t.Fatalf("home backup missing: info=%v err=%v", info, err)
	}
}

func TestPrepareAgentsDirectoryRejectsUnsafeTarget(t *testing.T) {
	homeAgentsDir := filepath.Join(t.TempDir(), "home", ".agents")
	if err := PrepareAgentsDirectory("relative/path", filepath.Join(t.TempDir(), ".agents"), ""); err == nil {
		t.Fatal("PrepareAgentsDirectory() error = nil, want relative path rejection")
	}
	if err := PrepareAgentsDirectory(string(filepath.Separator), filepath.Join(t.TempDir(), ".agents"), ""); err == nil {
		t.Fatal("PrepareAgentsDirectory() error = nil, want filesystem root rejection")
	}
	if err := PrepareAgentsDirectory(filepath.Join(homeAgentsDir, "persistent"), homeAgentsDir, ""); err == nil {
		t.Fatal("PrepareAgentsDirectory() error = nil, want nested target rejection")
	}
}

func assertSymlinkTarget(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("Readlink(%q) error = %v", path, err)
	}
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("symlink target = %q, want %q", got, want)
	}
}
