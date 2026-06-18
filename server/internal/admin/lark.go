package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/mnhkahn/gogogo/logger"

	agentlark "xiaoli/server/internal/agent/channel/lark"
)

type larkCallback = agentlark.Callback
type larkMessageEvent = agentlark.MessageEvent

func (s *AdminServer) handleLarkEvents(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.LarkEnabled() {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
	if err != nil {
		logger.Infof("[lark] event request read failed: %v", err)
		http.Error(w, "read event failed", http.StatusBadRequest)
		return
	}
	var callback larkCallback
	if err := json.Unmarshal(raw, &callback); err != nil {
		logger.Infof("[lark] event request invalid json bytes=%d: %v", len(raw), err)
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	eventType := callback.EventType()
	logger.Infof("[lark] event received type=%s event_id=%s app_id=%s tenant=%s schema=%s bytes=%d", eventType, callback.Header.EventID, callback.Header.AppID, callback.Header.TenantKey, callback.Schema, len(raw))
	if callback.Header.AppID != "" && callback.Header.AppID != s.cfg.LarkAppID {
		logger.Infof("[lark] event rejected: app_id mismatch got=%s want=%s type=%s event_id=%s", callback.Header.AppID, s.cfg.LarkAppID, eventType, callback.Header.EventID)
		http.Error(w, "app id mismatch", http.StatusForbidden)
		return
	}
	switch eventType {
	case agentlark.EventTypeURLVerification:
		challenge := callback.URLVerificationChallenge()
		logger.Infof("[lark] url verification received event_id=%s challenge_present=%v", callback.Header.EventID, challenge != "")
		writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge})
	case agentlark.EventTypeMessageReceive:
		if callback.Header.EventID != "" && s.larkEventSeen(callback.Header.EventID) {
			logger.Infof("[lark] event duplicate ignored type=%s event_id=%s", eventType, callback.Header.EventID)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "duplicate": true})
			return
		}
		if err := s.handleLarkTextMessage(r.Context(), callback); err != nil {
			logger.Infof("[lark] message handling failed event_id=%s: %v", callback.Header.EventID, err)
			writeJSON(w, http.StatusOK, map[string]any{"ok": false})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		logger.Infof("[lark] event ignored type=%s event_id=%s", eventType, callback.Header.EventID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
	}
}

var larkProcessingEmojis = []string{"Typing", "OnIt", "THINKING", "OneSecond"}

func pickLarkReaction() string {
	return larkProcessingEmojis[rand.Intn(len(larkProcessingEmojis))]
}

func (s *AdminServer) handleLarkTextMessage(ctx context.Context, callback larkCallback) error {
	var event larkMessageEvent
	if err := json.Unmarshal(callback.Event, &event); err != nil {
		return fmt.Errorf("decode message event: %w", err)
	}
	senderID := event.SenderID()
	logger.Infof("[lark] message received event_id=%s chat=%s message=%s sender_type=%s sender=%s message_type=%s content_bytes=%d", callback.Header.EventID, event.Message.ChatID, event.Message.MessageID, event.Sender.SenderType, senderID, event.Message.MessageType, len(event.Message.Content))
	if event.Sender.SenderType == "bot" {
		logger.Infof("[lark] message ignored event_id=%s reason=bot_sender message=%s", callback.Header.EventID, event.Message.MessageID)
		return nil
	}
	if event.Message.MessageType != "text" {
		logger.Infof("[lark] message ignored event_id=%s reason=non_text message=%s message_type=%s", callback.Header.EventID, event.Message.MessageID, event.Message.MessageType)
		return nil
	}
	text := event.Text()
	if text == "" {
		logger.Infof("[lark] message ignored event_id=%s reason=empty_text message=%s", callback.Header.EventID, event.Message.MessageID)
		return nil
	}
	if event.Message.ChatID == "" || senderID == "" || event.Message.MessageID == "" {
		return fmt.Errorf("message event missing chat, sender, or message id")
	}
	if reply, ok := s.handleBuiltinCommand(ctx, ChannelLarkText, text); ok {
		if err := s.newLarkClient().ReplyText(ctx, event.Message.MessageID, reply); err != nil {
			return err
		}
		logger.Infof("[lark] builtin command reply sent event_id=%s message=%s chat=%s", callback.Header.EventID, event.Message.MessageID, event.Message.ChatID)
		return nil
	}
	if s.conversation == nil {
		return fmt.Errorf("conversation pipeline is not configured")
	}

	lc := s.newLarkClient()
	emojiType := pickLarkReaction()
	reactionID, err := lc.AddReaction(ctx, event.Message.MessageID, emojiType)
	if err != nil {
		logger.Infof("[lark] add reaction failed event_id=%s message=%s emoji=%s err=%v", callback.Header.EventID, event.Message.MessageID, emojiType, err)
	}
	if reactionID != "" {
		defer func() {
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanCancel()
			if err := lc.RemoveReaction(cleanCtx, event.Message.MessageID, reactionID); err != nil {
				logger.Infof("[lark] remove reaction failed event_id=%s message=%s emoji=%s err=%v", callback.Header.EventID, event.Message.MessageID, emojiType, err)
			}
		}()
	}

	reply, err := s.conversation.Run(ctx, LarkTextFactory{}.Build(event.Message.ChatID, senderID, text))
	if err != nil {
		logger.Infof("[lark] conversation error: %v", err)
	}
	if reply.Text == "" {
		return fmt.Errorf("lark conversation returned empty reply")
	}
	if err := s.newLarkClient().ReplyText(ctx, event.Message.MessageID, reply.Text); err != nil {
		return err
	}
	logger.Infof("[lark] message reply sent event_id=%s message=%s chat=%s", callback.Header.EventID, event.Message.MessageID, event.Message.ChatID)
	return nil
}

func (s *AdminServer) larkEventSeen(eventID string) bool {
	if eventID == "" {
		return false
	}
	now := s.cfg.now()
	s.larkMu.Lock()
	defer s.larkMu.Unlock()
	for id, seenAt := range s.larkEvents {
		if now.Sub(seenAt) > time.Hour {
			delete(s.larkEvents, id)
		}
	}
	if _, ok := s.larkEvents[eventID]; ok {
		return true
	}
	s.larkEvents[eventID] = now
	return false
}

func (s *AdminServer) newLarkClient() *agentlark.Client {
	return agentlark.NewClient(agentlark.ClientConfig{
		AppID:      s.cfg.LarkAppID,
		AppToken:   s.cfg.LarkAppToken,
		HTTPClient: s.httpClient,
	})
}
