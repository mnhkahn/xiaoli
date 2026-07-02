package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type Tool struct {
	info     *schema.ToolInfo
	client   *Client
	ToolName string
}

func (t *Tool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *Tool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		args = map[string]any{}
	}
	return t.client.Call(ctx, t.ToolName, args)
}

func UniqueSafeToolName(name string, used map[string]int) string {
	safe := SafeToolName(name)
	if used == nil {
		return safe
	}
	used[safe]++
	if used[safe] == 1 {
		return safe
	}
	for {
		suffix := "_" + strconvItoa(used[safe])
		base := safe
		if len(base)+len(suffix) > 64 {
			base = base[:64-len(suffix)]
		}
		candidate := base + suffix
		if used[candidate] == 0 {
			used[candidate] = 1
			return candidate
		}
		used[safe]++
	}
}

func SafeToolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	var b strings.Builder
	b.Grow(len(name))
	lastUnderscore := false
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	safe := strings.Trim(b.String(), "_")
	if safe == "" {
		safe = "tool"
	}
	if len(safe) > 64 {
		safe = strings.TrimRight(safe[:64], "_-")
		if safe == "" {
			safe = "tool"
		}
	}
	return safe
}

func strconvItoa(v int) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = digits[v%10]
		v /= 10
	}
	return string(b[i:])
}
