package admin

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PrepareAgentsDirectory makes the standard home .agents path point at a
// configurable persistent directory. Existing home data is migrated without
// overwriting files already present in the persistent directory.
func PrepareAgentsDirectory(agentsDir, homeAgentsDir, seedSkillsDir string) error {
	agentsDir = filepath.Clean(strings.TrimSpace(agentsDir))
	homeAgentsDir = filepath.Clean(strings.TrimSpace(homeAgentsDir))
	if agentsDir == "." || !filepath.IsAbs(agentsDir) {
		return fmt.Errorf("XIAOLI_AGENTS_DIR must be an absolute path: %q", agentsDir)
	}
	if agentsDir == string(filepath.Separator) {
		return errors.New("XIAOLI_AGENTS_DIR must not be the filesystem root")
	}
	if homeAgentsDir == "." || !filepath.IsAbs(homeAgentsDir) {
		return fmt.Errorf("home agents directory must be an absolute path: %q", homeAgentsDir)
	}
	if agentsDir == homeAgentsDir {
		return errors.New("XIAOLI_AGENTS_DIR must differ from the home .agents path")
	}
	if pathContains(homeAgentsDir, agentsDir) || pathContains(agentsDir, homeAgentsDir) {
		return errors.New("XIAOLI_AGENTS_DIR and the home .agents path must not contain each other")
	}

	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return fmt.Errorf("create persistent agents directory: %w", err)
	}
	if err := migrateHomeAgentsDirectory(homeAgentsDir, agentsDir); err != nil {
		return err
	}
	if err := ensureAgentsSymlink(homeAgentsDir, agentsDir); err != nil {
		return err
	}
	if err := seedSkillsIfEmpty(filepath.Join(agentsDir, "skills"), seedSkillsDir); err != nil {
		return err
	}
	return nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil || relative == "." {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func migrateHomeAgentsDirectory(homeAgentsDir, agentsDir string) error {
	info, err := os.Lstat(homeAgentsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect home agents directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("home agents path is not a directory or symlink: %s", homeAgentsDir)
	}
	if err := copyMissingTree(homeAgentsDir, agentsDir); err != nil {
		return fmt.Errorf("migrate home agents directory: %w", err)
	}
	backup := homeAgentsDir + ".pre-persistent"
	if _, err := os.Lstat(backup); err == nil {
		return fmt.Errorf("cannot migrate home agents directory: backup already exists: %s", backup)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect home agents backup: %w", err)
	}
	if err := os.Rename(homeAgentsDir, backup); err != nil {
		return fmt.Errorf("back up home agents directory: %w", err)
	}
	return nil
}

func ensureAgentsSymlink(homeAgentsDir, agentsDir string) error {
	if err := os.MkdirAll(filepath.Dir(homeAgentsDir), 0o755); err != nil {
		return fmt.Errorf("create home directory: %w", err)
	}
	if info, err := os.Lstat(homeAgentsDir); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("home agents path is not a symlink: %s", homeAgentsDir)
		}
		linkTarget, err := os.Readlink(homeAgentsDir)
		if err != nil {
			return fmt.Errorf("read home agents symlink: %w", err)
		}
		if !filepath.IsAbs(linkTarget) {
			linkTarget = filepath.Join(filepath.Dir(homeAgentsDir), linkTarget)
		}
		if filepath.Clean(linkTarget) == agentsDir {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect home agents symlink: %w", err)
	}

	temporaryLink := homeAgentsDir + ".new"
	_ = os.Remove(temporaryLink)
	if err := os.Symlink(agentsDir, temporaryLink); err != nil {
		return fmt.Errorf("create home agents symlink: %w", err)
	}
	if err := os.Rename(temporaryLink, homeAgentsDir); err != nil {
		_ = os.Remove(temporaryLink)
		return fmt.Errorf("install home agents symlink: %w", err)
	}
	return nil
}

func seedSkillsIfEmpty(skillsDir, seedSkillsDir string) error {
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return fmt.Errorf("create persistent skills directory: %w", err)
	}
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return fmt.Errorf("read persistent skills directory: %w", err)
	}
	if len(entries) > 0 || strings.TrimSpace(seedSkillsDir) == "" {
		return nil
	}
	if _, err := os.Stat(seedSkillsDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect seed skills directory: %w", err)
	}
	if err := copyMissingTree(seedSkillsDir, skillsDir); err != nil {
		return fmt.Errorf("seed persistent skills directory: %w", err)
	}
	return nil
}

func copyMissingTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if _, err := os.Lstat(target); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if errors.Is(err, os.ErrExist) {
			return input.Close()
		}
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}
