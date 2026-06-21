package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/redis/go-redis/v9"
)

type MemoryBackend interface {
	Save(ctx context.Context, key, value string) error
	Forget(ctx context.Context, key string) error
	List(ctx context.Context) (map[string]string, error)
	Clear(ctx context.Context) error
}

type MemoryBackends struct {
	Global  MemoryBackend
	Channel MemoryBackend
}

type redisMemoryBackend struct {
	client *redis.Client
	key    string
}

func NewMemoryBackend(client *redis.Client, prefix, channel, user string) MemoryBackend {
	return &redisMemoryBackend{
		client: client,
		key:    prefix + "memory:" + channel + ":" + user,
	}
}

func NewMemoryBackendScoped(client *redis.Client, prefix, channel, user, scope string) MemoryBackend {
	if scope == "global" {
		return &redisMemoryBackend{
			client: client,
			key:    prefix + "memory:global:" + user,
		}
	}
	return NewMemoryBackend(client, prefix, channel, user)
}

func (b *redisMemoryBackend) Save(ctx context.Context, field, value string) error {
	return b.client.HSet(ctx, b.key, field, value).Err()
}

func (b *redisMemoryBackend) Forget(ctx context.Context, field string) error {
	return b.client.HDel(ctx, b.key, field).Err()
}

func (b *redisMemoryBackend) List(ctx context.Context) (map[string]string, error) {
	return b.client.HGetAll(ctx, b.key).Result()
}

func (b *redisMemoryBackend) Clear(ctx context.Context) error {
	return b.client.Del(ctx, b.key).Err()
}

type MemorySaveTool struct {
	backends *MemoryBackends
}

func NewMemorySaveTool(backends *MemoryBackends) *MemorySaveTool {
	return &MemorySaveTool{backends: backends}
}

func (t *MemorySaveTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "memory_save",
		Desc: `记住一条关于用户的信息，比如偏好、个人情况、重要事实。之后每次对话都会自动加载这些信息作为上下文。仅在用户明确说"记一下""记住""以后按这个来"时调用。不要自己判断哪些信息值得记，等用户开口。覆盖已有记录时会自动更新。`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"key": {
				Type:     schema.String,
				Desc:     `记忆的分类标签，如"饮食偏好""工作""家庭""兴趣"，简洁明确`,
				Required: true,
			},
			"value": {
				Type:     schema.String,
				Desc:     "记忆的具体内容，描述要完整清晰",
				Required: true,
			},
			"scope": {
				Type: schema.String,
				Desc: `记忆的作用域："global" 表示全局记忆（所有设备/频道可见），"channel" 表示仅当前频道可见。默认为 "global"。`,
			},
		}),
	}, nil
}

func (t *MemorySaveTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	args.Key = strings.TrimSpace(args.Key)
	args.Value = strings.TrimSpace(args.Value)
	if args.Key == "" || args.Value == "" {
		return "", fmt.Errorf("key 和 value 都是必填参数")
	}
	backend := t.selectBackend(args.Scope)
	if err := backend.Save(ctx, args.Key, args.Value); err != nil {
		return "", fmt.Errorf("保存失败：%v", err)
	}
	scopeLabel := "全局"
	if args.Scope == "channel" {
		scopeLabel = "当前频道"
	}
	return fmt.Sprintf("已记住（%s）：%s → %s", scopeLabel, args.Key, args.Value), nil
}

func (t *MemorySaveTool) selectBackend(scope string) MemoryBackend {
	if scope == "channel" && t.backends.Channel != nil {
		return t.backends.Channel
	}
	if t.backends.Global != nil {
		return t.backends.Global
	}
	return t.backends.Channel
}

type MemoryForgetTool struct {
	backends *MemoryBackends
}

func NewMemoryForgetTool(backends *MemoryBackends) *MemoryForgetTool {
	return &MemoryForgetTool{backends: backends}
}

func (t *MemoryForgetTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "memory_forget",
		Desc: `删除一条已保存的用户记忆。调用前先调 memory_list 查看已保存的记忆，确认 key 名后再删除。当用户说"忘了""删除""不记得那个了"时调用。`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"key": {
				Type:     schema.String,
				Desc:     "要删除的记忆标签名",
				Required: true,
			},
			"scope": {
				Type: schema.String,
				Desc: `记忆的作用域："global" 表示全局记忆（所有设备/频道可见），"channel" 表示仅当前频道可见。默认为 "global"。`,
			},
		}),
	}, nil
}

func (t *MemoryForgetTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Key   string `json:"key"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	args.Key = strings.TrimSpace(args.Key)
	if args.Key == "" {
		return "", fmt.Errorf("key 是必填参数")
	}
	backend := t.selectBackend(args.Scope)
	if err := backend.Forget(ctx, args.Key); err != nil {
		return "", fmt.Errorf("删除失败：%v", err)
	}
	scopeLabel := "全局"
	if args.Scope == "channel" {
		scopeLabel = "当前频道"
	}
	return fmt.Sprintf("已从 %s 记忆中删除：%s", scopeLabel, args.Key), nil
}

func (t *MemoryForgetTool) selectBackend(scope string) MemoryBackend {
	if scope == "channel" && t.backends.Channel != nil {
		return t.backends.Channel
	}
	if t.backends.Global != nil {
		return t.backends.Global
	}
	return t.backends.Channel
}

type MemoryListTool struct {
	backends *MemoryBackends
}

func NewMemoryListTool(backends *MemoryBackends) *MemoryListTool {
	return &MemoryListTool{backends: backends}
}

func (t *MemoryListTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "memory_list",
		Desc: `列出所有已保存的用户记忆。当用户问"你记得我什么""我告诉过你什么"时调用。`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"scope": {
				Type: schema.String,
				Desc: `查询范围："global" 仅全局记忆，"channel" 仅当前频道，"all" 两者合并。默认为 "all"。`,
			},
		}),
	}, nil
}

func (t *MemoryListTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	data, err := t.collectMemories(ctx, args.Scope)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "目前没有保存的记忆。", nil
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("已保存的记忆：")
	for _, k := range keys {
		b.WriteString("\n- ")
		b.WriteString(k)
		b.WriteString("：")
		b.WriteString(data[k])
	}
	return b.String(), nil
}

func (t *MemoryListTool) collectMemories(ctx context.Context, scope string) (map[string]string, error) {
	result := make(map[string]string)
	switch scope {
	case "global":
		if t.backends.Global != nil {
			data, err := t.backends.Global.List(ctx)
			if err != nil {
				return nil, fmt.Errorf("读取全局记忆失败：%v", err)
			}
			for k, v := range data {
				result[k] = v
			}
		}
	case "channel":
		if t.backends.Channel != nil {
			data, err := t.backends.Channel.List(ctx)
			if err != nil {
				return nil, fmt.Errorf("读取频道记忆失败：%v", err)
			}
			for k, v := range data {
				result[k] = v
			}
		}
	default:
		if t.backends.Global != nil {
			data, err := t.backends.Global.List(ctx)
			if err != nil {
				return nil, fmt.Errorf("读取全局记忆失败：%v", err)
			}
			for k, v := range data {
				result[k] = v
			}
		}
		if t.backends.Channel != nil {
			data, err := t.backends.Channel.List(ctx)
			if err != nil {
				return nil, fmt.Errorf("读取频道记忆失败：%v", err)
			}
			for k, v := range data {
				if _, exists := result[k]; exists {
					result[k+" (channel)"] = v
				} else {
					result[k] = v
				}
			}
		}
	}
	return result, nil
}

func LoadMemories(ctx context.Context, backends *MemoryBackends) string {
	if backends == nil {
		return ""
	}
	result := make(map[string]string)
	if backends.Global != nil {
		data, err := backends.Global.List(ctx)
		if err == nil {
			for k, v := range data {
				result[k] = v
			}
		}
	}
	if backends.Channel != nil {
		data, err := backends.Channel.List(ctx)
		if err == nil {
			for k, v := range data {
				result[k] = v
			}
		}
	}
	if len(result) == 0 {
		return ""
	}
	keys := make([]string, 0, len(result))
	for k := range result {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("=== 用户记忆 ===")
	for _, k := range keys {
		fmt.Fprintf(&b, "\n- %s：%s", k, result[k])
	}
	return b.String()
}
