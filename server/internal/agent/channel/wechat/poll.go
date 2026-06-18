package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mnhkahn/gogogo/logger"
)

func PollMessages(ctx context.Context, c *Client, onMessage func(context.Context, *Message)) {
	buf := ""
	logger.Infof("[wechat] polling started base_url=%s", c.BaseURL)

	for {
		select {
		case <-ctx.Done():
			logger.Infof("[wechat] polling stopped")
			return
		default:
		}

		pollCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		raw, err := c.PostJSON(pollCtx, "/ilink/bot/getupdates", &GetUpdatesRequest{
			GetUpdatesBuf: buf,
			BaseInfo:      &BaseInfo{ChannelVersion: "1.0.3"},
		})
		cancel()

		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				continue
			}
			logger.Infof("[wechat] poll error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		var resp GetUpdatesResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			logger.Infof("[wechat] poll decode error: %v", err)
			continue
		}
		if resp.Ret != 0 {
			if resp.Ret == -14 {
				logger.Infof("[wechat] session expired, need re-login")
				return
			}
			continue
		}

		if resp.GetUpdatesBuf != "" {
			buf = resp.GetUpdatesBuf
		}

		for i := range resp.Messages {
			msg := &resp.Messages[i]
			if msg.MessageType != MsgUser {
				continue
			}
			if onMessage != nil {
				onMessage(ctx, msg)
			}
		}
	}
}
