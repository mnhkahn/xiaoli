package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	sourceEnvName          = "XIAOLI_SOURCE"
	sourceBootstrapEnvName = "XIAOLI_SOURCE_BOOTSTRAPPED"
)

type sourceBootstrapDeps struct {
	Args         []string
	Environ      []string
	Stderr       io.Writer
	Getenv       func(string) string
	UserCacheDir func() (string, error)
	NeedsBuild   func(sourceDir, target string) (bool, error)
	Build        func(sourceDir, output string) error
	Run          func(binary string, args, env []string) error
}

type sourceBootstrapResult struct {
	Handled  bool
	ExitCode int
	Err      error
}

func defaultSourceBootstrapDeps() sourceBootstrapDeps {
	return sourceBootstrapDeps{
		Args:         os.Args,
		Environ:      os.Environ(),
		Stderr:       os.Stderr,
		Getenv:       os.Getenv,
		UserCacheDir: os.UserCacheDir,
		NeedsBuild:   sourceNeedsRebuild,
		Build: func(sourceDir, output string) error {
			goBin, err := exec.LookPath("go")
			if err != nil {
				return fmt.Errorf("找不到 go 命令: %w", err)
			}
			cmd := exec.Command(goBin, "build", "-o", output, "./tui/cmd/xiaoli")
			cmd.Dir = sourceDir
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("编译失败: %w", err)
			}
			return nil
		},
		Run: func(binary string, args, env []string) error {
			cmd := exec.Command(binary, args...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Env = env
			return cmd.Run()
		},
	}
}

var (
	sourceBuildActiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	sourceBuildDoneStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
)

func runSourceBootstrap(deps sourceBootstrapDeps) sourceBootstrapResult {
	getenv := deps.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	sourceDir := strings.TrimSpace(getenv(sourceEnvName))
	if sourceDir == "" || strings.TrimSpace(getenv(sourceBootstrapEnvName)) != "" {
		return sourceBootstrapResult{}
	}
	if !filepath.IsAbs(sourceDir) {
		return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("%s 必须是本地绝对路径，当前为 %q", sourceEnvName, sourceDir)}
	}
	root, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("读取 %s 失败: %w", sourceEnvName, err)}
	}
	if err := validateSourceRoot(root); err != nil {
		return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: err}
	}

	userCacheDir := deps.UserCacheDir
	if userCacheDir == nil {
		userCacheDir = os.UserCacheDir
	}
	cacheRoot, err := userCacheDir()
	if err != nil {
		return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("定位用户缓存目录失败: %w", err)}
	}
	cacheDir := filepath.Join(cacheRoot, "xiaoli", "source-build")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("创建源码构建缓存目录失败: %w", err)}
	}
	statusWriter := deps.Stderr
	if statusWriter == nil {
		statusWriter = io.Discard
	}
	target := filepath.Join(cacheDir, sourceBinaryName())
	fmt.Fprintln(statusWriter, sourceBuildActiveStyle.Render("(^_^) Checking source changes…"))
	needsBuild := deps.NeedsBuild
	if needsBuild == nil {
		needsBuild = sourceNeedsRebuild
	}
	rebuild, err := needsBuild(root, target)
	if err != nil {
		return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("检测源码变化失败: %w", err)}
	}
	if rebuild {
		temporary, err := os.CreateTemp(cacheDir, "xiaoli-build-*")
		if err != nil {
			return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("创建临时构建文件失败: %w", err)}
		}
		temporaryPath := temporary.Name()
		if err := temporary.Close(); err != nil {
			_ = os.Remove(temporaryPath)
			return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("关闭临时构建文件失败: %w", err)}
		}
		if err := os.Remove(temporaryPath); err != nil {
			return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("准备临时构建文件失败: %w", err)}
		}
		defer os.Remove(temporaryPath)

		build := deps.Build
		if build == nil {
			build = defaultSourceBootstrapDeps().Build
		}
		buildStarted := time.Now()
		fmt.Fprintln(statusWriter, sourceBuildActiveStyle.Render("(^_^) Source changed; recompiling Xiaoli…"))
		if err := build(root, temporaryPath); err != nil {
			return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: err}
		}
		if err := os.Rename(temporaryPath, target); err != nil {
			return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("安装源码构建失败: %w", err)}
		}
		buildElapsed := time.Since(buildStarted).Round(10 * time.Millisecond)
		fmt.Fprintln(statusWriter, sourceBuildDoneStyle.Render(fmt.Sprintf("(ok) Recompiled in %s; starting Xiaoli…", buildElapsed)))
	} else {
		fmt.Fprintln(statusWriter, sourceBuildDoneStyle.Render("(ok) Source unchanged; skipping rebuild and starting Xiaoli…"))
	}

	args := deps.Args
	if args == nil {
		args = os.Args
	}
	if len(args) > 0 {
		args = args[1:]
	}
	env := append([]string(nil), deps.Environ...)
	if env == nil {
		env = os.Environ()
	}
	env = append(env, sourceBootstrapEnvName+"=1")
	run := deps.Run
	if run == nil {
		run = defaultSourceBootstrapDeps().Run
	}
	if err := run(target, args, env); err != nil {
		return sourceBootstrapResult{Handled: true, ExitCode: exitCode(err), Err: err}
	}
	return sourceBootstrapResult{Handled: true}
}

var errSourceBuildInputNewer = errors.New("source build input is newer")

func sourceNeedsRebuild(sourceDir, target string) (bool, error) {
	targetInfo, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, relative := range []string{"go.mod", "go.sum", "internal", "tui"} {
		path := filepath.Join(sourceDir, relative)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return false, err
		}
		err := filepath.WalkDir(path, func(_ string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.ModTime().After(targetInfo.ModTime()) {
				return errSourceBuildInputNewer
			}
			return nil
		})
		if errors.Is(err, errSourceBuildInputNewer) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

func validateSourceRoot(root string) error {
	for _, relative := range []string{"go.mod", filepath.Join("tui", "cmd", "xiaoli", "main.go")} {
		info, err := os.Stat(filepath.Join(root, relative))
		if err != nil || info.IsDir() {
			return fmt.Errorf("%s 不是有效的小李源码目录：缺少 %s", sourceEnvName, relative)
		}
	}
	return nil
}

func sourceBinaryName() string {
	if runtime.GOOS == "windows" {
		return "xiaoli.exe"
	}
	return "xiaoli"
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if err == nil {
		return 0
	}
	if !errors.As(err, &exitErr) {
		return 1
	}
	return exitErr.ExitCode()
}
