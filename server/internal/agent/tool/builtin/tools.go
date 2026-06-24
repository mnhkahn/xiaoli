package builtin

import (
	agentworkflow "xiaoli/server/internal/agent/workflow"
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
)

type ToolOptions struct {
	MemoryBackends *MemoryBackends
	WebSearchKey   string
	ShellConfig    *ShellConfig
	ReminderStore  *agentworkflow.ReminderStore
	Timezone       string
}

type subAgentParentKeyType struct{}

var SubAgentParentKey = subAgentParentKeyType{}

type subAgentDeviceIDKeyType struct{}

var SubAgentDeviceIDKey = subAgentDeviceIDKeyType{}

type subAgentChannelKeyType struct{}

var SubAgentChannelKey = subAgentChannelKeyType{}
