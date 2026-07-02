package builtin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type LogSearchTool struct {
	logDir string
}

func NewLogSearchTool(logDir string) *LogSearchTool {
	return &LogSearchTool{logDir: logDir}
}

func (t *LogSearchTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "log_search",
		Desc: `搜索服务器日志，按关键词和时间范围过滤。返回匹配的日志行，最新的日志优先。
日志时间格式为 "2006-01-02 15:04"，时区使用服务器配置时区。`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"keyword": {
				Type:     schema.String,
				Desc:     "搜索关键词，支持子字符串匹配，不区分大小写",
				Required: false,
			},
			"start_time": {
				Type:     schema.String,
				Desc:     "开始时间，格式 2006-01-02 15:04，可选",
				Required: false,
			},
			"end_time": {
				Type:     schema.String,
				Desc:     "结束时间，格式 2006-01-02 15:04，可选",
				Required: false,
			},
			"max_lines": {
				Type:     schema.Integer,
				Desc:     "返回最大行数，默认 50，最多 200",
				Required: false,
			},
		}),
	}, nil
}

func (t *LogSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Keyword   string `json:"keyword"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		MaxLines  int    `json:"max_lines"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}

	maxLines := args.MaxLines
	if maxLines <= 0 {
		maxLines = 50
	}
	if maxLines > 200 {
		maxLines = 200
	}

	keyword := strings.ToLower(strings.TrimSpace(args.Keyword))

	var startTime, endTime time.Time
	var err error

	if args.StartTime != "" {
		startTime, err = time.ParseInLocation("2006-01-02 15:04", args.StartTime, time.Local)
		if err != nil {
			return "", fmt.Errorf("start_time 格式错误：%v，使用格式 2006-01-02 15:04", err)
		}
	} else {
		startTime = time.Now().Add(-time.Hour)
	}
	if args.EndTime != "" {
		endTime, err = time.ParseInLocation("2006-01-02 15:04", args.EndTime, time.Local)
		if err != nil {
			return "", fmt.Errorf("end_time 格式错误：%v，使用格式 2006-01-02 15:04", err)
		}
	}

	if _, err := os.Stat(t.logDir); os.IsNotExist(err) {
		return fmt.Sprintf("日志目录不存在：%s", t.logDir), nil
	}

	logFiles, err := filepath.Glob(filepath.Join(t.logDir, "*.log"))
	if err != nil {
		return "", fmt.Errorf("查找日志文件失败：%v", err)
	}
	if len(logFiles) == 0 {
		return fmt.Sprintf("日志目录 %s 中没有 .log 文件", t.logDir), nil
	}

	sort.Slice(logFiles, func(i, j int) bool {
		fi, _ := os.Stat(logFiles[i])
		fj, _ := os.Stat(logFiles[j])
		return fi.ModTime().After(fj.ModTime())
	})

	var results []string
	scannedCount := 0
	matchedCount := 0

	for _, logFile := range logFiles {
		if len(results) >= maxLines {
			break
		}

		file, err := os.Open(logFile)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(file)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		file.Close()

		for i := len(lines) - 1; i >= 0; i-- {
			if len(results) >= maxLines {
				break
			}

			line := lines[i]
			scannedCount++

			logTime, ok := parseLogTime(line)
			if ok {
				if !startTime.IsZero() && logTime.Before(startTime) {
					continue
				}
				if !endTime.IsZero() && logTime.After(endTime) {
					continue
				}
			}

			if keyword != "" && !strings.Contains(strings.ToLower(line), keyword) {
				continue
			}

			matchedCount++
			results = append(results, line)
		}
	}

	if len(results) == 0 {
		if keyword == "" {
			return "没有找到日志", nil
		}
		return fmt.Sprintf("搜索关键词 %q 没有匹配的日志", keyword), nil
	}

	desc := fmt.Sprintf("共扫描 %d 行，匹配 %d 行，显示最近 %d 行：\n", scannedCount, matchedCount, len(results))
	for i := len(results) - 1; i >= 0; i-- {
		desc += results[i] + "\n"
	}

	return desc, nil
}

func parseLogTime(line string) (time.Time, bool) {
	if len(line) < 24 {
		return time.Time{}, false
	}

	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 4 {
		return time.Time{}, false
	}

	timeStr := parts[1] + " " + parts[2]
	t, err := time.ParseInLocation("2006/01/02 15:04:05", timeStr, time.Local)
	if err != nil {
		return time.Time{}, false
	}

	return t, true
}
