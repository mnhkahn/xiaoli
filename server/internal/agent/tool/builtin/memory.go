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

func (b *redisMemoryBackend) Save(ctx context.Context, field, value string) error {
	return b.client.HSet(ctx, b.key, field, value).Err()
}

func (b *redisMemoryBackend) Forget(ctx context.Context, field string) error {
	return b.client.HDel(ctx, b.key, field).Err()
}

func (b *redisMemoryBackend) List(ctx context.Context) (map[string]string, error) {
	return b.client.HGetAll(ctx, b.key).Result()
}

type MemorySaveTool struct {
	backend MemoryBackend
}

func NewMemorySaveTool(backend MemoryBackend) *MemorySaveTool {
	return &MemorySaveTool{backend: backend}
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
		}),
	}, nil
}

func (t *MemorySaveTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	args.Key = strings.TrimSpace(args.Key)
	args.Value = strings.TrimSpace(args.Value)
	if args.Key == "" || args.Value == "" {
		return "", fmt.Errorf("key 和 value 都是必填参数")
	}

	if err := t.backend.Save(ctx, args.Key, args.Value); err != nil {
		return "", fmt.Errorf("保存失败：%v", err)
	}
	return fmt.Sprintf("已记住：%s → %s", args.Key, args.Value), nil
}

type MemoryForgetTool struct {
	backend MemoryBackend
}

func NewMemoryForgetTool(backend MemoryBackend) *MemoryForgetTool {
	return &MemoryForgetTool{backend: backend}
}

func (t *MemoryForgetTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "memory_forget",
		Desc: `删除一条已保存的用户记忆。当用户说"忘了""删除""不记得那个了"时调用。`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"key": {
				Type:     schema.String,
				Desc:     "要删除的记忆标签名",
				Required: true,
			},
		}),
	}, nil
}

func (t *MemoryForgetTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	args.Key = strings.TrimSpace(args.Key)
	if args.Key == "" {
		return "", fmt.Errorf("key 是必填参数")
	}

	if err := t.backend.Forget(ctx, args.Key); err != nil {
		return "", fmt.Errorf("删除失败：%v", err)
	}
	return fmt.Sprintf("已忘记：%s", args.Key), nil
}

type MemoryListTool struct {
	backend MemoryBackend
}

func NewMemoryListTool(backend MemoryBackend) *MemoryListTool {
	return &MemoryListTool{backend: backend}
}

func (t *MemoryListTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "memory_list",
		Desc:        `列出所有已保存的用户记忆。当用户问"你记得我什么""我告诉过你什么"时调用。`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *MemoryListTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	data, err := t.backend.List(ctx)
	if err != nil {
		return "", fmt.Errorf("读取失败：%v", err)
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
		fmt.Fprintf(&b, "\n- %s：%s", k, data[k])
	}
	return b.String(), nil
}

func LoadMemories(ctx context.Context, backend MemoryBackend) string {
	data, err := backend.List(ctx)
	if err != nil || len(data) == 0 {
		return ""
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("=== 用户记忆 ===")
	for _, k := range keys {
		fmt.Fprintf(&b, "\n- %s：%s", k, data[k])
	}
	return b.String()
}
