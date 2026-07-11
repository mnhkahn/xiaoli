package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	var builtSource, builtOutput, ranBinary string
	var ranArgs, ranEnv []string
	result := runSourceBootstrap(sourceBootstrapDeps{
		Args:    []string{"xiaoli", "-version"},
		Environ: []string{"KEEP=yes"},
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

func containsEnv(env []string, want string) bool {
	for _, value := range env {
		if value == want {
			return true
		}
	}
	return false
}
