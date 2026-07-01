package builtin

import (
	"context"

	agentworkflow "github.com/mnhkahn/xiaoli-esp32/server/internal/agent/workflow"
)

type ToolFilter uint64

const (
	ToolWebFetch ToolFilter = 1 << iota
	ToolWebSearch
	ToolAskUserQuestion
	ToolTask
	ToolMemorySave
	ToolMemoryForget
	ToolMemoryList
	ToolBash
	ToolReminder
	ToolLog
	ToolInspectRecentImage
	ToolFileWrite
)

type ToolOptions struct {
	MemoryBackends *MemoryBackends
	WebSearchKey   string
	ShellConfig    *ShellConfig
	ReminderStore  *agentworkflow.ReminderStore
	Timezone       string
	LogDir         string
	VisionAnalyzer VisionAnalyzer
	RecentImages   RecentImageStore
	FileWriteRoots []string
}

type subAgentParentKeyType struct{}

var SubAgentParentKey = subAgentParentKeyType{}

type subAgentDeviceIDKeyType struct{}

var SubAgentDeviceIDKey = subAgentDeviceIDKeyType{}

type subAgentChannelKeyType struct{}

var SubAgentChannelKey = subAgentChannelKeyType{}

type recentImageConversationKeyType struct{}

var recentImageConversationKey = recentImageConversationKeyType{}

func WithRecentImageConversation(ctx context.Context, conversationID string) context.Context {
	return context.WithValue(ctx, recentImageConversationKey, conversationID)
}

func recentImageConversation(ctx context.Context) string {
	value, _ := ctx.Value(recentImageConversationKey).(string)
	return value
}
