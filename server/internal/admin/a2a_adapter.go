package admin

import (
	"context"
	"errors"
	"strings"

	a2a "xiaoli/server/internal/a2a"
)

// a2aPipeline adapts the EinoAgent's named subagent invocation to the
// a2a.ConversationPipeline interface. It routes A2A requests to the
// a2a_public_assistant subagent, never to the main agent. The internal
// session ID (a2a:<key_id>:<context_id>) is used as the sessionKey so the
// subagent's memory is scoped to the calling partner and context, never
// to a personal device or Lark/WeChat session.
type a2aPipeline struct {
	agent *EinoAgent
}

var _ a2a.ConversationPipeline = (*a2aPipeline)(nil)

func newA2APipeline(agent *EinoAgent) *a2aPipeline {
	return &a2aPipeline{agent: agent}
}

func (p *a2aPipeline) Run(ctx context.Context, turn a2a.ConversationTurn) (a2a.ConversationReply, error) {
	if p.agent == nil {
		return a2a.ConversationReply{}, errors.New("agent not available")
	}
	text := strings.TrimSpace(turn.Text)
	if text == "" {
		return a2a.ConversationReply{}, errors.New("empty text")
	}
	reply, err := p.agent.RunNamedSubAgent(ctx, "a2a_public_assistant", text, turn.ConversationID, turn.Channel)
	if err != nil {
		return a2a.ConversationReply{}, err
	}
	return a2a.ConversationReply{Text: reply}, nil
}
