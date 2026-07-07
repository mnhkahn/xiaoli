package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func stubDeps(latestTag string, fetchErr error) (*bytes.Buffer, *bytes.Buffer, *[][]string, upgradeCLIDeps) {
	var stdout, stderr bytes.Buffer
	var runs [][]string
	deps := upgradeCLIDeps{
		Stdout: &stdout,
		Stderr: &stderr,
		FetchLatest: func(ctx context.Context) (releaseInfo, error) {
			if fetchErr != nil {
				return releaseInfo{}, fetchErr
			}
			return releaseInfo{Tag: latestTag, URL: "https://example/release"}, nil
		},
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Run: func(bin string, args []string, stdout, stderr io.Writer) error {
			runs = append(runs, append([]string{bin}, args...))
			return nil
		},
	}
	return &stdout, &stderr, &runs, deps
}

func TestUpgradeCLICheckReportsNewer(t *testing.T) {
	stdout, _, _, deps := stubDeps("v1.2.3", nil)
	deps.Current = "v1.0.0"
	code := runUpgradeCLIWithDeps([]string{"-check"}, deps)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	got := stdout.String()
	if !strings.Contains(got, "当前版本: v1.0.0") || !strings.Contains(got, "最新版本: v1.2.3") {
		t.Fatalf("missing version lines: %q", got)
	}
	if !strings.Contains(got, "go install github.com/mnhkahn/xiaoli/tui/cmd/xiaoli@v1.2.3") {
		t.Fatalf("expected install command in output, got %q", got)
	}
}

func TestUpgradeCLICheckUpToDate(t *testing.T) {
	stdout, _, _, deps := stubDeps("v1.0.0", nil)
	deps.Current = "v1.0.0"
	code := runUpgradeCLIWithDeps([]string{"-check"}, deps)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stdout.String(), "已是最新版本") {
		t.Fatalf("expected up-to-date message, got %q", stdout.String())
	}
}

func TestUpgradeCLIRunsGoInstall(t *testing.T) {
	stdout, _, runs, deps := stubDeps("v2.0.0", nil)
	deps.Current = "v1.0.0"
	code := runUpgradeCLIWithDeps(nil, deps)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if len(*runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(*runs))
	}
	run := (*runs)[0]
	if run[0] != "/usr/bin/go" || run[1] != "install" || !strings.HasSuffix(run[2], "@v2.0.0") {
		t.Fatalf("unexpected run command %v", run)
	}
	if !strings.Contains(stdout.String(), "升级完成") {
		t.Fatalf("expected success line, got %q", stdout.String())
	}
}

func TestUpgradeCLIExplicitVersionSkipsFetch(t *testing.T) {
	stdout, _, runs, deps := stubDeps("", errors.New("should not be called"))
	fetched := false
	deps.FetchLatest = func(ctx context.Context) (releaseInfo, error) {
		fetched = true
		return releaseInfo{}, errors.New("unexpected")
	}
	deps.Current = "v1.0.0"
	code := runUpgradeCLIWithDeps([]string{"v0.3.0"}, deps)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if fetched {
		t.Fatal("explicit version should not trigger release fetch")
	}
	if len(*runs) != 1 || !strings.HasSuffix((*runs)[0][2], "@v0.3.0") {
		t.Fatalf("unexpected runs %v", *runs)
	}
	if !strings.Contains(stdout.String(), "@v0.3.0") {
		t.Fatalf("expected explicit tag in output, got %q", stdout.String())
	}
}

func TestUpgradeCLIFallsBackToLatestOnFetchError(t *testing.T) {
	stdout, stderr, runs, deps := stubDeps("", errors.New("boom"))
	deps.Current = "v1.0.0"
	code := runUpgradeCLIWithDeps(nil, deps)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if len(*runs) != 1 || !strings.HasSuffix((*runs)[0][2], "@latest") {
		t.Fatalf("expected fallback to @latest, got %v", *runs)
	}
	if !strings.Contains(stderr.String(), "查询最新版本失败") {
		t.Fatalf("expected fetch failure warning in stderr, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "升级完成") {
		t.Fatalf("expected success line, got %q", stdout.String())
	}
}

func TestUpgradeCLIMissingGoReportsHint(t *testing.T) {
	stdout, stderr, runs, deps := stubDeps("v2.0.0", nil)
	deps.Current = "v1.0.0"
	deps.LookPath = func(name string) (string, error) { return "", errors.New("not found") }
	code := runUpgradeCLIWithDeps(nil, deps)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if len(*runs) != 0 {
		t.Fatalf("run should not be invoked when go is missing")
	}
	if !strings.Contains(stderr.String(), "找不到 go 命令") {
		t.Fatalf("expected missing-go hint, got %q", stderr.String())
	}
	_ = stdout
}

func TestUpgradeCLIPrintDoesNotFetch(t *testing.T) {
	stdout, _, runs, deps := stubDeps("v9.9.9", nil)
	deps.Current = "v1.0.0"
	fetched := false
	deps.FetchLatest = func(ctx context.Context) (releaseInfo, error) {
		fetched = true
		return releaseInfo{Tag: "v9.9.9"}, nil
	}
	code := runUpgradeCLIWithDeps([]string{"-print"}, deps)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if fetched {
		t.Fatal("-print without version must not hit the network")
	}
	if len(*runs) != 0 {
		t.Fatalf("-print must not invoke Run, got %v", *runs)
	}
	if !strings.Contains(stdout.String(), "@latest") {
		t.Fatalf("expected @latest fallback in offline print output, got %q", stdout.String())
	}
}

func TestUpgradeCLIPrintWithExplicitVersionSkipsFetch(t *testing.T) {
	stdout, _, runs, deps := stubDeps("", errors.New("should not fetch"))
	fetched := false
	deps.FetchLatest = func(ctx context.Context) (releaseInfo, error) {
		fetched = true
		return releaseInfo{}, errors.New("nope")
	}
	deps.Current = "v1.0.0"
	code := runUpgradeCLIWithDeps([]string{"-print", "v0.3.0"}, deps)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if fetched {
		t.Fatal("explicit version + -print must not fetch")
	}
	if len(*runs) != 0 {
		t.Fatalf("-print must not invoke Run, got %v", *runs)
	}
	if !strings.Contains(stdout.String(), "@v0.3.0") {
		t.Fatalf("expected @v0.3.0 in output, got %q", stdout.String())
	}
}

func TestUpgradeCLIRejectsExtraArgs(t *testing.T) {
	_, stderr, runs, deps := stubDeps("v2.0.0", nil)
	deps.Current = "v1.0.0"
	code := runUpgradeCLIWithDeps([]string{"v0.3.0", "extra"}, deps)
	if code != 2 {
		t.Fatalf("expected exit 2 for extra args, got %d", code)
	}
	if len(*runs) != 0 {
		t.Fatalf("extra args must abort before Run, got %v", *runs)
	}
	if !strings.Contains(stderr.String(), "只能指定一个版本参数") {
		t.Fatalf("expected extra-args error, got %q", stderr.String())
	}
}

func TestUpgradeCLIFallbackAnnotatedInOutput(t *testing.T) {
	stdout, _, runs, deps := stubDeps("", errors.New("boom"))
	deps.Current = "v1.0.0"
	code := runUpgradeCLIWithDeps(nil, deps)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if len(*runs) != 1 || !strings.HasSuffix((*runs)[0][2], "@latest") {
		t.Fatalf("expected fallback install, got %v", *runs)
	}
	if !strings.Contains(stdout.String(), "升级目标: latest (fallback)") {
		t.Fatalf("expected fallback annotation in stdout, got %q", stdout.String())
	}
}

func TestUpgradeCLIHelpGoesToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := upgradeCLIDeps{Stdout: &stdout, Stderr: &stderr}
	code := runUpgradeCLIWithDeps([]string{"-h"}, deps)
	if code != 0 {
		t.Fatalf("expected exit 0 for -h, got %d", code)
	}
	if !strings.Contains(stdout.String(), "用法: xiaoli upgrade") {
		t.Fatalf("expected usage on stdout, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
