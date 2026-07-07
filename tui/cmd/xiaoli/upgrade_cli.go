package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// upgradeCLIDeps 收拢外部依赖，方便在测试里替换。
type upgradeCLIDeps struct {
	Stdout      io.Writer
	Stderr      io.Writer
	FetchLatest func(ctx context.Context) (releaseInfo, error)
	LookPath    func(name string) (string, error)
	Run         func(bin string, args []string, stdout, stderr io.Writer) error
	Current     string
}

// runUpgradeCLI 处理 `xiaoli upgrade` 子命令；返回进程退出码。
func runUpgradeCLI(args []string) int {
	return runUpgradeCLIWithDeps(args, defaultUpgradeCLIDeps())
}

func defaultUpgradeCLIDeps() upgradeCLIDeps {
	return upgradeCLIDeps{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		FetchLatest: func(ctx context.Context) (releaseInfo, error) {
			client := &http.Client{Timeout: 5 * time.Second}
			return fetchLatestRelease(ctx, client, latestReleaseURL)
		},
		LookPath: exec.LookPath,
		Run: func(bin string, args []string, stdout, stderr io.Writer) error {
			cmd := exec.Command(bin, args...)
			cmd.Stdout = stdout
			cmd.Stderr = stderr
			cmd.Stdin = os.Stdin
			return cmd.Run()
		},
		Current: buildVersion(),
	}
}

func runUpgradeCLIWithDeps(args []string, deps upgradeCLIDeps) int {
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	if deps.Stderr == nil {
		deps.Stderr = os.Stderr
	}

	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	// flag 解析错误默认走 stderr；-h 由我们在 Usage 里显式打到 stdout。
	fs.SetOutput(deps.Stderr)
	check := fs.Bool("check", false, "只检查最新版本，不执行安装")
	printOnly := fs.Bool("print", false, "只打印将要执行的命令，不实际执行（默认不联网）")
	writeUsage := func(w io.Writer) {
		fmt.Fprintln(w, "用法: xiaoli upgrade [flags] [version]")
		fmt.Fprintln(w, "示例:")
		fmt.Fprintln(w, "  xiaoli upgrade            升级到最新的 GitHub release")
		fmt.Fprintln(w, "  xiaoli upgrade v0.3.0     升级到指定版本 tag")
		fmt.Fprintln(w, "  xiaoli upgrade latest     使用 Go module @latest 查询")
		fmt.Fprintln(w, "  xiaoli upgrade -check     只显示最新版本信息")
		fmt.Fprintln(w, "  xiaoli upgrade -print     只打印 go install 命令（不联网）")
		fmt.Fprintln(w, "flags:")
	}
	// -h/--help：flag 包会把 ErrHelp 前先调用 Usage，我们把说明写到 stdout。
	fs.Usage = func() {
		writeUsage(deps.Stdout)
		prev := fs.Output()
		fs.SetOutput(deps.Stdout)
		fs.PrintDefaults()
		fs.SetOutput(prev)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if fs.NArg() > 1 {
		fmt.Fprintf(deps.Stderr, "upgrade: 只能指定一个版本参数，收到: %v\n", fs.Args())
		writeUsage(deps.Stderr)
		return 2
	}

	target := strings.TrimSpace(fs.Arg(0))
	current := strings.TrimSpace(deps.Current)
	if current == "" {
		current = "dev"
	}

	// 需要联网查最新版本的情况：
	//   - -check 必须查（要显示最新版本）
	//   - 未指定 target 且不是 -print（要决定升到哪）
	// -print 模式不联网，target 缺失时直接使用 @latest。
	var (
		release    releaseInfo
		releaseErr error
	)
	needFetch := *check || (target == "" && !*printOnly)
	if needFetch {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if deps.FetchLatest != nil {
			release, releaseErr = deps.FetchLatest(ctx)
		} else {
			releaseErr = errors.New("no release fetcher configured")
		}
	}

	if *check {
		fmt.Fprintf(deps.Stdout, "当前版本: %s\n", current)
		if releaseErr != nil {
			fmt.Fprintf(deps.Stderr, "版本检查失败: %v\n", releaseErr)
			fmt.Fprintln(deps.Stdout, "如需手动升级，可运行:")
			fmt.Fprintln(deps.Stdout, "  "+upgradeCommand("latest"))
			return 1
		}
		fmt.Fprintf(deps.Stdout, "最新版本: %s\n", release.Tag)
		switch {
		case isDevVersion(current):
			fmt.Fprintln(deps.Stdout, "当前是开发构建，建议运行:")
			fmt.Fprintln(deps.Stdout, "  "+upgradeCommand(release.Tag))
		case compareVersions(current, release.Tag) < 0:
			fmt.Fprintln(deps.Stdout, "发现新版本，运行以下命令升级:")
			fmt.Fprintln(deps.Stdout, "  "+upgradeCommand(release.Tag))
		default:
			fmt.Fprintln(deps.Stdout, "已是最新版本。")
		}
		return 0
	}

	// 决定升级到哪个 tag。
	resolved := target
	fellBack := false
	if resolved == "" {
		if releaseErr == nil && strings.TrimSpace(release.Tag) != "" {
			resolved = release.Tag
		} else {
			if releaseErr != nil {
				fmt.Fprintf(deps.Stderr, "查询最新版本失败: %v，回退到 @latest。\n", releaseErr)
			}
			resolved = "latest"
			fellBack = true
		}
	}
	cmdStr := upgradeCommand(resolved)

	if *printOnly {
		fmt.Fprintln(deps.Stdout, cmdStr)
		return 0
	}

	if deps.LookPath == nil {
		deps.LookPath = exec.LookPath
	}
	goBin, err := deps.LookPath("go")
	if err != nil {
		fmt.Fprintln(deps.Stderr, "找不到 go 命令，请先安装 Go 工具链，或手动运行：")
		fmt.Fprintln(deps.Stderr, "  "+cmdStr)
		return 1
	}

	fmt.Fprintf(deps.Stdout, "当前版本: %s\n", current)
	if fellBack {
		fmt.Fprintf(deps.Stdout, "升级目标: %s (fallback)\n", resolved)
	} else {
		fmt.Fprintf(deps.Stdout, "升级目标: %s\n", resolved)
	}
	fmt.Fprintln(deps.Stdout, "执行:", cmdStr)
	installArgs := []string{"install", "github.com/mnhkahn/xiaoli/tui/cmd/xiaoli@" + resolved}
	if err := deps.Run(goBin, installArgs, deps.Stdout, deps.Stderr); err != nil {
		fmt.Fprintln(deps.Stderr, "升级失败:", err)
		return 1
	}
	fmt.Fprintln(deps.Stdout, "升级完成。重启 xiaoli 即可使用新版本。")
	return 0
}
