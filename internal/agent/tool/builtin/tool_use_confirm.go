package builtin

import (
	"context"
	"sync"
)

type toolUseConfirmKeyType struct{}

var ToolUseConfirmKey = toolUseConfirmKeyType{}

type ToolUseConfirmHolder struct {
	mu   sync.Mutex
	Data []*PendingToolUseConfirm
}

func (h *ToolUseConfirmHolder) Set(d *PendingToolUseConfirm) {
	h.Append(d)
}

func (h *ToolUseConfirmHolder) Append(d *PendingToolUseConfirm) {
	if d == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Data = append(h.Data, d)
}

func (h *ToolUseConfirmHolder) Get() *PendingToolUseConfirm {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.Data) == 0 {
		return nil
	}
	return h.Data[0]
}

func (h *ToolUseConfirmHolder) All() []*PendingToolUseConfirm {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*PendingToolUseConfirm(nil), h.Data...)
}

func NewToolUseConfirmHolder(ctx context.Context) (context.Context, *ToolUseConfirmHolder) {
	holder := &ToolUseConfirmHolder{}
	return context.WithValue(ctx, ToolUseConfirmKey, holder), holder
}

type PendingToolUseConfirm struct {
	RequestID      string
	SessionID      string
	ConversationID string
	ChannelName    string
	DeviceID       string
	ToolName       string
	ToolUseID      string
	Question       string
	Options        []string
	BashHash       string
	BashCommand    string
}
