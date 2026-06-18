package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/mnhkahn/gogogo/logger"

	agentwechat "xiaoli/server/internal/agent/channel/wechat"
)

const (
	wechatDefaultBaseURL = agentwechat.DefaultBaseURL
)

type wechatMessage = agentwechat.Message
type wechatQRCodeStatus = agentwechat.QRCodeStatus
type wechatClient = agentwechat.Client

func newWechatClient() *wechatClient {
	return agentwechat.NewClient(agentwechat.ClientConfig{})
}

func (s *AdminServer) startWechatPolling(ctx context.Context) {
	token := s.cfg.WeChatBotToken
	if token == "" {
		logger.Infof("[wechat] WECHAT_BOT_TOKEN not set, skipping")
		return
	}

	c := newWechatClient()
	c.Token = token
	c.BaseURL = s.cfg.WeChatBaseURL

	agentwechat.PollMessages(ctx, c, func(ctx context.Context, msg *wechatMessage) {
		s.handleWechatMessage(ctx, c, msg)
	})
}

func (s *AdminServer) handleWechatMessage(ctx context.Context, c *wechatClient, msg *wechatMessage) {
	text := strings.TrimSpace(msg.Text())
	if text == "" {
		return
	}
	logger.Infof("[wechat] message from=%s text=%q", msg.FromUserID, text)

	if msg.ContextToken == "" || msg.FromUserID == "" || msg.ToUserID == "" {
		logger.Infof("[wechat] message missing context_token/from/to, ignored")
		return
	}

	if reply, ok := s.handleBuiltinCommand(ctx, ChannelWechatText, text); ok {
		if err := wechatSendText(ctx, c, msg.ToUserID, msg.FromUserID, msg.ContextToken, reply); err != nil {
			logger.Infof("[wechat] builtin command send error: %v", err)
		} else {
			logger.Infof("[wechat] builtin command send ok to=%s command=%q", msg.FromUserID, text)
		}
		return
	}

	if s.conversation == nil {
		logger.Infof("[wechat] conversation not configured")
		return
	}

	if err := wechatSendTyping(ctx, c, msg.ToUserID, msg.FromUserID, msg.ContextToken); err != nil {
		logger.Infof("[wechat] typing send error: %v", err)
	} else {
		logger.Infof("[wechat] typing send ok to=%s", msg.FromUserID)
	}

	reply, err := s.conversation.Run(ctx, WechatTextFactory{}.Build(msg.ContextToken, msg.FromUserID, text))
	if err != nil {
		logger.Infof("[wechat] conversation error: %v", err)
	}
	if reply.Text == "" {
		reply = ConversationReply{Text: "抱歉，我暂时无法回答。"}
	}

	if err := wechatSendText(ctx, c, msg.ToUserID, msg.FromUserID, msg.ContextToken, reply.Text); err != nil {
		logger.Infof("[wechat] send error: %v", err)
	} else {
		logger.Infof("[wechat] send ok to=%s text=%q", msg.FromUserID, reply.Text)
	}
}

func wechatSendTyping(ctx context.Context, c *wechatClient, fromUserID, toUserID, contextToken string) error {
	return agentwechat.SendTyping(ctx, c, fromUserID, toUserID, contextToken)
}

func wechatSendText(ctx context.Context, c *wechatClient, fromUserID, toUserID, contextToken, text string) error {
	return agentwechat.SendText(ctx, c, fromUserID, toUserID, contextToken, text)
}

func wechatLogin(ctx context.Context, onQRCode func(content string)) error {
	return agentwechat.Login(ctx, onQRCode, func(status wechatQRCodeStatus) {
		switch status.Status {
		case "confirmed":
			logger.Infof("[wechat] login successful!")
			fmt.Printf("\nWECHAT_BOT_TOKEN=%s\nWECHAT_BASE_URL=%s\n", status.BotToken, status.BaseURL)
		case "scaned":
			logger.Infof("[wechat] QR scanned, waiting for confirmation")
		}
	})
}

func WechatLoginCLI(ctx context.Context) error {
	fmt.Println("正在获取二维码，请用微信扫码登录...")
	return wechatLogin(ctx, func(content string) {
		fmt.Println("扫码内容:", content)
	})
}
