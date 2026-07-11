package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	sourceEnvName          = "XIAOLI_SOURCE"
	sourceBootstrapEnvName = "XIAOLI_SOURCE_BOOTSTRAPPED"
)

type sourceBootstrapDeps struct {
	Args         []string
	Environ      []string
	Getenv       func(string) string
	UserCacheDir func() (string, error)
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
		Getenv:       os.Getenv,
		UserCacheDir: os.UserCacheDir,
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
	if err := build(root, temporaryPath); err != nil {
		return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: err}
	}
	target := filepath.Join(cacheDir, sourceBinaryName())
	if err := os.Rename(temporaryPath, target); err != nil {
		return sourceBootstrapResult{Handled: true, ExitCode: 1, Err: fmt.Errorf("安装源码构建失败: %w", err)}
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
