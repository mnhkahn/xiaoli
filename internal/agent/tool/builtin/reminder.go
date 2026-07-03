package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	agentworkflow "github.com/mnhkahn/xiaoli/internal/agent/workflow"
)

// reminderLocation 返回配置时区，无效或为空时回退到 Asia/Shanghai。
func reminderLocation(timezone string) *time.Location {
	if timezone != "" {
		if loc, err := time.LoadLocation(timezone); err == nil {
			return loc
		}
	}
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.Local
}

// normalizeReminderChannel 将 channel 名映射为提醒使用的渠道名
func normalizeReminderChannel(ch string) string {
	switch ch {
	case "lark_text":
		return "lark"
	case "wechat_text":
		return "wechat"
	case "device_voice":
		return "esp32"
	default:
		return ch
	}
}

type ReminderAddTool struct {
	store    *agentworkflow.ReminderStore
	timezone string
}

func NewReminderAddTool(store *agentworkflow.ReminderStore, timezone string) *ReminderAddTool {
	return &ReminderAddTool{store: store, timezone: timezone}
}

func (t *ReminderAddTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "reminder_add",
		Desc: `创建一条定时提醒，到点后会通过用户的设备语音播报提醒内容。当用户说"提醒我…""到时候叫我…""每天…记得…"时调用。创建后即时生效。`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"text": {
				Type:     schema.String,
				Desc:     "提醒内容，到点播报的原话，描述清楚要做什么",
				Required: true,
			},
			"type": {
				Type: schema.String,
				Desc: `提醒类型："once" 一次性（默认），"daily" 每天重复`,
				Enum: []string{"once", "daily"},
			},
			"time": {
				Type:     schema.String,
				Desc:     `提醒时间。type=once 时为 "2006-01-02 15:04" 格式的绝对时间（必须是将来）；type=daily 时为 "15:04" 格式的每天时刻。请根据环境信息里的当前时间换算出绝对时间。`,
				Required: true,
			},
		}),
	}, nil
}

func (t *ReminderAddTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Text string `json:"text"`
		Type string `json:"type"`
		Time string `json:"time"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	args.Text = strings.TrimSpace(args.Text)
	args.Time = strings.TrimSpace(args.Time)
	if args.Text == "" {
		return "", fmt.Errorf("text 是必填参数")
	}
	if args.Time == "" {
		return "", fmt.Errorf("time 是必填参数")
	}

	kind := agentworkflow.ReminderTriggerType(strings.TrimSpace(args.Type))
	if kind == "" {
		kind = agentworkflow.ReminderOnce
	}
	loc := reminderLocation(t.timezone)
	now := time.Now().In(loc)

	r := agentworkflow.Reminder{
		ID:        fmt.Sprintf("rmd_%d", time.Now().UnixNano()),
		Name:      args.Text,
		Enabled:   true,
		Action:    "speak",
		Text:      args.Text,
		CreatedAt: now.Format(time.RFC3339),
	}

	// 绑定发起提醒的语音设备：仅 ESP32 渠道的 deviceID 才是可播报的真实设备，
	// 飞书/微信的 deviceID 是用户标识，匹配不到设备，绑定后会导致提醒永远无法触发。
	channelName, _ := ctx.Value(SubAgentChannelKey).(string)
	channel := normalizeReminderChannel(channelName)
	r.Channel = channel // 记录创建时的来源 channel
	senderID, _ := ctx.Value(SubAgentDeviceIDKey).(string)
	r.SenderID = senderID // 记录发送者 ID：飞书 open_id / ESP32 device_id
	if channel == "esp32" && senderID != "" {
		r.Metadata = map[string]any{"device_ids": senderID}
	}

	var whenLabel string
	switch kind {
	case agentworkflow.ReminderOnce:
		parsed, err := parseReminderAbsTime(args.Time, loc)
		if err != nil {
			return "", fmt.Errorf(`时间格式不对：%v；type=once 请用 "2006-01-02 15:04"`, err)
		}
		if parsed.Before(now) {
			return "", fmt.Errorf("提醒时间已过，请用将来的时间")
		}
		r.Trigger = agentworkflow.ReminderTrigger{
			Type:     agentworkflow.ReminderOnce,
			At:       parsed.Format(time.RFC3339),
			Timezone: loc.String(),
		}
		whenLabel = parsed.Format("2006-01-02 15:04")
	case agentworkflow.ReminderDaily:
		hour, minute, err := parseClock(args.Time)
		if err != nil {
			return "", fmt.Errorf(`时间格式不对：%v；type=daily 请用 "15:04"`, err)
		}
		r.Trigger = agentworkflow.ReminderTrigger{
			Type:     agentworkflow.ReminderDaily,
			AtHour:   &hour,
			AtMinute: &minute,
			Timezone: loc.String(),
		}
		whenLabel = fmt.Sprintf("每天 %02d:%02d", hour, minute)
	default:
		return "", fmt.Errorf("不支持的 type：%q，可用 once 或 daily", args.Type)
	}

	if err := t.store.Add(r); err != nil {
		return "", fmt.Errorf("保存提醒失败：%v", err)
	}
	return fmt.Sprintf("已创建提醒 [%s]：%s（%s）", r.ID, args.Text, whenLabel), nil
}

type ReminderListTool struct {
	store *agentworkflow.ReminderStore
}

func NewReminderListTool(store *agentworkflow.ReminderStore) *ReminderListTool {
	return &ReminderListTool{store: store}
}

func (t *ReminderListTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "reminder_list",
		Desc:        `列出当前所有提醒，含每条的 ID、内容、触发时间和状态。当用户问"我有哪些提醒""我设了什么提醒"时调用；删除提醒前也先调用它拿到 ID。`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *ReminderListTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	reminders, err := t.store.Load()
	if err != nil {
		return "", fmt.Errorf("读取提醒失败：%v", err)
	}
	if len(reminders) == 0 {
		return "当前没有提醒。", nil
	}
	var b strings.Builder
	b.WriteString("提醒列表：")
	for _, r := range reminders {
		status := "启用"
		if !r.Enabled {
			status = "禁用"
		}
		if r.IsOnceFired() {
			status = "已完成"
		}
		fmt.Fprintf(&b, "\n- [%s] %s（%s）%s", r.ID, r.Text, reminderTriggerText(r.Trigger), status)
	}
	return b.String(), nil
}

type ReminderDeleteTool struct {
	store *agentworkflow.ReminderStore
}

func NewReminderDeleteTool(store *agentworkflow.ReminderStore) *ReminderDeleteTool {
	return &ReminderDeleteTool{store: store}
}

func (t *ReminderDeleteTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "reminder_delete",
		Desc: `删除一条提醒。调用前先调 reminder_list 查看提醒并确认 ID。当用户说"取消那个提醒""别提醒我了""删掉…的提醒"时调用。`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"id": {
				Type:     schema.String,
				Desc:     "要删除的提醒 ID（形如 rmd_xxx），从 reminder_list 获取",
				Required: true,
			},
		}),
	}, nil
}

func (t *ReminderDeleteTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	args.ID = strings.TrimSpace(args.ID)
	if args.ID == "" {
		return "", fmt.Errorf("id 是必填参数")
	}
	removed, err := t.store.Delete(args.ID)
	if err != nil {
		return "", fmt.Errorf("删除提醒失败：%v", err)
	}
	if !removed {
		return fmt.Sprintf("未找到提醒：%s", args.ID), nil
	}
	return fmt.Sprintf("已删除提醒：%s", args.ID), nil
}

// parseReminderAbsTime 解析绝对时间：优先 RFC3339，回退指定时区下的 "2006-01-02 15:04"。
func parseReminderAbsTime(s string, loc *time.Location) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, s); err == nil {
		return parsed, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, s, loc); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析 %q", s)
}

// parseClock 解析 "15:04" 形式的每日时刻。
func parseClock(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("无法解析 %q", s)
	}
	return t.Hour(), t.Minute(), nil
}

// reminderTriggerText 把触发配置渲染成中文说明，与 admin 的展示保持一致。
func reminderTriggerText(t agentworkflow.ReminderTrigger) string {
	switch t.Type {
	case agentworkflow.ReminderOnce:
		if parsed, err := time.Parse(time.RFC3339, t.At); err == nil {
			return parsed.Format("2006-01-02 15:04")
		}
		return t.At
	case agentworkflow.ReminderDaily:
		hour, minute := 0, 0
		if t.AtHour != nil {
			hour = *t.AtHour
		}
		if t.AtMinute != nil {
			minute = *t.AtMinute
		}
		return fmt.Sprintf("每天 %02d:%02d", hour, minute)
	case agentworkflow.ReminderInterval:
		return "每 " + t.Every
	default:
		return string(t.Type)
	}
}
