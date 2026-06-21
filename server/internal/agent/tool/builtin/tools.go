package builtin

type ToolFilter uint64

const (
	ToolWebFetch ToolFilter = 1 << iota
	ToolWebSearch
	ToolAskUserQuestion
	ToolTask
	ToolMemorySave
	ToolMemoryForget
	ToolMemoryList
)

type ToolOptions struct {
	MemoryBackends *MemoryBackends
	WebSearchKey   string
}

type subAgentParentKeyType struct{}

var SubAgentParentKey = subAgentParentKeyType{}
