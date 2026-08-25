package runtime

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
)

// toolOutputBudgetMiddleware keeps a long tool chain from consuming the whole
// provider request. It only replaces old tool results; raw data remains in the
// output store and the newest result is kept intact.
type toolOutputBudgetMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	store    *toolOutputStore
	maxBytes int
}

func newToolOutputBudgetMiddleware(store *toolOutputStore, modelCfg LLMModelConfig) adk.ChatModelAgentMiddleware {
	maxBytes := 48 * 1024
	if modelCfg.ContextLength > 0 {
		availableTokens := modelCfg.ContextLength - modelCfg.MaxTokens - 8000
		if availableTokens > 0 && availableTokens*4 < maxBytes {
			maxBytes = availableTokens * 4
		}
	}
	if maxBytes < 8*1024 {
		maxBytes = 8 * 1024
	}
	return &toolOutputBudgetMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store, maxBytes: maxBytes}
}

func (m *toolOutputBudgetMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	used := 0
	for _, msg := range state.Messages {
		if msg != nil && msg.Role == schema.Tool {
			used += len(msg.Content)
		}
	}
	for i := 0; used > m.maxBytes && i < len(state.Messages); i++ {
		msg := state.Messages[i]
		if msg == nil || msg.Role != schema.Tool || len(msg.Content) == 0 {
			continue
		}
		id := outputIDFromProjection(msg.Content)
		if id == "" {
			id = m.store.idFor(msg.Content)
		}
		replacement := fmt.Sprintf("[较早的工具结果已从上下文压缩：原始 %d bytes", len(msg.Content))
		if id != "" {
			replacement += "; output_id=" + id
		}
		replacement += "]"
		copyMsg := *msg
		copyMsg.Content = replacement
		state.Messages[i] = &copyMsg
		used += len(replacement) - len(msg.Content)
	}
	if used > m.maxBytes {
		logger.Infof("tool output budget remains over limit: bytes=%d limit=%d", used, m.maxBytes)
	}
	return ctx, state, nil
}
