package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ResolveConfig struct {
	AllowedRoots []string
	MaxFileBytes int64
}

func DefaultResolveConfig() ResolveConfig {
	return ResolveConfig{
		MaxFileBytes: 64 * 1024,
	}
}

func ResolvePromptRefs(ctx context.Context, prompt string, cfg ResolveConfig, lookupAgent func(name string) (string, bool)) (string, error) {
	var result strings.Builder
	lines := strings.Split(prompt, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "@") && lookupAgent != nil {
			parts := strings.Fields(trimmed)
			if len(parts) >= 1 {
				name := strings.TrimPrefix(parts[0], "@")
				if desc, ok := lookupAgent(name); ok {
					result.WriteString(line)
					result.WriteString("\n")
					result.WriteString("[代理 ")
					result.WriteString(name)
					result.WriteString("：")
					result.WriteString(desc)
					result.WriteString("]\n")
					continue
				}
				result.WriteString(line)
				result.WriteString("\n")
				result.WriteString("[警告：未知代理 \"")
				result.WriteString(name)
				result.WriteString("\"]\n")
				continue
			}
		}

		if len(cfg.AllowedRoots) > 0 && (strings.HasPrefix(trimmed, "file:") || strings.HasPrefix(trimmed, "file：")) {
			path := strings.TrimSpace(trimmed[5:])
			path = strings.Trim(path, "\"'")
			content, err := readFileRef(path, cfg)
			if err != nil {
				result.WriteString(line)
				result.WriteString("\n")
				result.WriteString("[文件读取失败：")
				result.WriteString(err.Error())
				result.WriteString("]\n")
				continue
			}
			result.WriteString(line)
			result.WriteString("\n")
			result.WriteString("```\n")
			result.WriteString(content)
			result.WriteString("\n```\n")
			continue
		}

		result.WriteString(line)
		result.WriteString("\n")
	}

	return result.String(), nil
}

func readFileRef(path string, cfg ResolveConfig) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("路径解析失败：%v", err)
	}

	abs = filepath.Clean(abs)

	if len(cfg.AllowedRoots) > 0 {
		allowed := false
		for _, root := range cfg.AllowedRoots {
			rootAbs, err := filepath.Abs(root)
			if err != nil {
				continue
			}
			rootAbs = filepath.Clean(rootAbs)
			if strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) || abs == rootAbs {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("路径不在允许的根目录范围内")
		}
	}

	if strings.Contains(abs, "..") {
		return "", fmt.Errorf("路径包含 .. 越界访问")
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("文件不存在")
		}
		return "", fmt.Errorf("无法访问文件：%v", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("路径是目录，不是文件")
	}

	maxBytes := cfg.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	if info.Size() > maxBytes {
		return "", fmt.Errorf("文件过大（%d bytes，上限 %d bytes）", info.Size(), maxBytes)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("读取失败：%v", err)
	}

	return string(data), nil
}
