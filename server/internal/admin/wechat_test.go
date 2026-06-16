package admin

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestWechatSendLocal(t *testing.T) {
	token := os.Getenv("WECHAT_BOT_TOKEN")
	toUserID := os.Getenv("WECHAT_TO_USER")
	contextToken := os.Getenv("WECHAT_CONTEXT_TOKEN")
	if token == "" || toUserID == "" {
		t.Skip("set WECHAT_BOT_TOKEN, WECHAT_TO_USER, WECHAT_CONTEXT_TOKEN")
	}

	baseURL := os.Getenv("WECHAT_BASE_URL")
	if baseURL == "" {
		baseURL = "https://ilinkai.weixin.qq.com"
	}

	c := &wechatClient{
		baseURL: baseURL,
		token:   token,
		httpDo:  http.DefaultClient.Do,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("send with empty FromUserID", func(t *testing.T) {
		err := wechatSendText(ctx, c, "", toUserID, contextToken, "本地测试消息（FromUserID为空）")
		if err != nil {
			t.Errorf("send: %v", err)
		}
	})

	t.Run("send with bot FromUserID", func(t *testing.T) {
		botID := os.Getenv("WECHAT_BOT_USER_ID")
		if botID == "" {
			t.Skip("set WECHAT_BOT_USER_ID to test with FromUserID")
		}
		err := wechatSendText(ctx, c, botID, toUserID, contextToken, "本地测试消息（FromUserID=botID）")
		if err != nil {
			t.Errorf("send: %v", err)
		}
	})

	t.Run("send typing indicator", func(t *testing.T) {
		wechatSendTyping(ctx, c, "", toUserID, "")
	})
}