package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourceBootstrapSkipsWithoutSource(t *testing.T) {
	result := runSourceBootstrap(sourceBootstrapDeps{
		Getenv: func(string) string { return "" },
	})
	if result.Handled || result.Err != nil {
		t.Fatalf("result = %#v, want no bootstrap", result)
	}
}

func TestSourceBootstrapRejectsRelativePath(t *testing.T) {
	result := runSourceBootstrap(sourceBootstrapDeps{
		Getenv: func(name string) string {
			if name == sourceEnvName {
				return "../xiaoli"
			}
			return ""
		},
	})
	if !result.Handled || result.ExitCode != 1 || result.Err == nil {
		t.Fatalf("result = %#v, want handled path error", result)
	}
	if !strings.Contains(result.Err.Error(), "绝对路径") {
		t.Fatalf("error = %v, want absolute path guidance", result.Err)
	}
}

func TestSourceBootstrapBuildsAndRunsFreshBinary(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "tui", "cmd", "xiaoli"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"go.mod", filepath.Join("tui", "cmd", "xiaoli", "main.go")} {
		if err := os.WriteFile(filepath.Join(source, relative), []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cache := t.TempDir()
	var status bytes.Buffer
	var builtSource, builtOutput, ranBinary string
	var ranArgs, ranEnv []string
	result := runSourceBootstrap(sourceBootstrapDeps{
		Args:    []string{"xiaoli", "-version"},
		Environ: []string{"KEEP=yes"},
		Stderr:  &status,
		Getenv: func(name string) string {
			if name == sourceEnvName {
				return source
			}
			return ""
		},
		UserCacheDir: func() (string, error) { return cache, nil },
		Build: func(sourceDir, output string) error {
			builtSource, builtOutput = sourceDir, output
			return os.WriteFile(output, []byte("binary"), 0o755)
		},
		Run: func(binary string, args, env []string) error {
			ranBinary = binary
			ranArgs = append([]string(nil), args...)
			ranEnv = append([]string(nil), env...)
			return nil
		},
	})

	if !result.Handled || result.ExitCode != 0 || result.Err != nil {
		t.Fatalf("result = %#v, want successful bootstrap", result)
	}
	wantSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	if builtSource != wantSource || builtOutput == "" {
		t.Fatalf("build source/output = %q/%q, want %q and temporary output", builtSource, builtOutput, wantSource)
	}
	wantBinary := filepath.Join(cache, "xiaoli", "source-build", sourceBinaryName())
	if ranBinary != wantBinary {
		t.Fatalf("ran binary = %q, want %q", ranBinary, wantBinary)
	}
	if len(ranArgs) != 1 || ranArgs[0] != "-version" {
		t.Fatalf("run args = %#v, want [-version]", ranArgs)
	}
	if !containsEnv(ranEnv, sourceBootstrapEnvName+"=1") || !containsEnv(ranEnv, "KEEP=yes") {
		t.Fatalf("run env = %#v, want inherited env and bootstrap marker", ranEnv)
	}
	if _, err := os.Stat(wantBinary); err != nil {
		t.Fatalf("installed source binary missing: %v", err)
	}
	plainStatus := ansiEscapeRE.ReplaceAllString(status.String(), "")
	if !strings.Contains(plainStatus, "Checking source changes") || !strings.Contains(plainStatus, "recompiling Xiaoli") || !strings.Contains(plainStatus, "Recompiled in") {
		t.Fatalf("source build status = %q, want checking, compiling, and completed messages", plainStatus)
	}
}

func TestSourceBootstrapReportsAndSkipsUnchangedSource(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "tui", "cmd", "xiaoli"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"go.mod", filepath.Join("tui", "cmd", "xiaoli", "main.go")} {
		if err := os.WriteFile(filepath.Join(source, relative), []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cache := t.TempDir()
	target := filepath.Join(cache, "xiaoli", "source-build", sourceBinaryName())
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("cached binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var status bytes.Buffer
	var ranBinary string
	result := runSourceBootstrap(sourceBootstrapDeps{
		Args:   []string{"xiaoli", "-version"},
		Stderr: &status,
		Getenv: func(name string) string {
			if name == sourceEnvName {
				return source
			}
			return ""
		},
		UserCacheDir: func() (string, error) { return cache, nil },
		NeedsBuild:   func(string, string) (bool, error) { return false, nil },
		Build: func(string, string) error {
			t.Fatal("Build called for unchanged source")
			return nil
		},
		Run: func(binary string, _ []string, _ []string) error {
			ranBinary = binary
			return nil
		},
	})

	if !result.Handled || result.ExitCode != 0 || result.Err != nil {
		t.Fatalf("result = %#v, want successful cached bootstrap", result)
	}
	if ranBinary != target {
		t.Fatalf("ran binary = %q, want cached %q", ranBinary, target)
	}
	plainStatus := ansiEscapeRE.ReplaceAllString(status.String(), "")
	if !strings.Contains(plainStatus, "Checking source changes") || !strings.Contains(plainStatus, "Source unchanged; skipping rebuild") {
		t.Fatalf("source build status = %q, want checking and skipped messages", plainStatus)
	}
}

func TestSourceBootstrapSkipsBootstrappedChild(t *testing.T) {
	result := runSourceBootstrap(sourceBootstrapDeps{
		Getenv: func(name string) string {
			if name == sourceEnvName {
				return t.TempDir()
			}
			if name == sourceBootstrapEnvName {
				return "1"
			}
			return ""
		},
	})
	if result.Handled || result.Err != nil {
		t.Fatalf("result = %#v, want bootstrapped child skipped", result)
	}
}

func TestSourceNeedsRebuildDetectsNewerInput(t *testing.T) {
	source := t.TempDir()
	input := filepath.Join(source, "tui", "main.go")
	if err := os.MkdirAll(filepath.Dir(input), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), sourceBinaryName())
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	newer := time.Now().Add(time.Minute)
	if err := os.Chtimes(input, newer, newer); err != nil {
		t.Fatal(err)
	}
	rebuild, err := sourceNeedsRebuild(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if !rebuild {
		t.Fatal("sourceNeedsRebuild() = false, want true for newer source input")
	}
}

func containsEnv(env []string, want string) bool {
	for _, value := range env {
		if value == want {
			return true
		}
	}
	return false
}
