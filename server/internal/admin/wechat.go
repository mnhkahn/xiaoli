package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/mnhkahn/gogogo/logger"
)

const (
	wechatDefaultBaseURL = "https://ilinkai.weixin.qq.com"
	wechatBotType        = "3"
)

type wechatMessageType int

const (
	wechatMsgUser wechatMessageType = 1
	wechatMsgBot  wechatMessageType = 2
)

type wechatItemType int

const (
	wechatItemText  wechatItemType = 1
	wechatItemImage wechatItemType = 2
	wechatItemVoice wechatItemType = 3
	wechatItemFile  wechatItemType = 4
	wechatItemVideo wechatItemType = 5
)

type wechatMessage struct {
	Seq          int               `json:"seq,omitempty"`
	MessageID    int64             `json:"message_id,omitempty"`
	FromUserID   string            `json:"from_user_id,omitempty"`
	ToUserID     string            `json:"to_user_id,omitempty"`
	ClientID     string            `json:"client_id,omitempty"`
	CreateTimeMs int64             `json:"create_time_ms,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	MessageType  wechatMessageType `json:"message_type"`
	MessageState int               `json:"message_state"`
	ContextToken string            `json:"context_token,omitempty"`
	ItemList     []wechatMsgItem   `json:"item_list,omitempty"`
}

func (m *wechatMessage) Text() string {
	for _, item := range m.ItemList {
		if item.Type == wechatItemText && item.TextItem != nil {
			return item.TextItem.Text
		}
	}
	return ""
}

type wechatMsgItem struct {
	Type      wechatItemType   `json:"type"`
	TextItem  *wechatTextItem  `json:"text_item,omitempty"`
	ImageItem *wechatImageItem `json:"image_item,omitempty"`
	VoiceItem *wechatVoiceItem `json:"voice_item,omitempty"`
	FileItem  *wechatFileItem  `json:"file_item,omitempty"`
	VideoItem *wechatVideoItem `json:"video_item,omitempty"`
}

type wechatTextItem struct {
	Text string `json:"text"`
}

type wechatImageItem struct {
	Media   *wechatCDNMedia `json:"media,omitempty"`
	AESKey  string          `json:"aes_key,omitempty"`
	MidSize int             `json:"mid_size,omitempty"`
}

type wechatVoiceItem struct {
	Media    *wechatCDNMedia `json:"media,omitempty"`
	Duration int             `json:"duration,omitempty"`
	Text     string          `json:"text,omitempty"`
}

type wechatFileItem struct {
	Media    *wechatCDNMedia `json:"media,omitempty"`
	FileName string          `json:"file_name,omitempty"`
	Length   string          `json:"len,omitempty"`
}

type wechatVideoItem struct {
	Media      *wechatCDNMedia `json:"media,omitempty"`
	VideoSize  int             `json:"video_size,omitempty"`
	PlayLength int             `json:"play_length,omitempty"`
}

type wechatCDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param,omitempty"`
	AESKey            string `json:"aes_key,omitempty"`
	EncryptType       int    `json:"encrypt_type,omitempty"`
}

type wechatGetUpdatesReq struct {
	GetUpdatesBuf string          `json:"get_updates_buf"`
	BaseInfo      *wechatBaseInfo `json:"base_info,omitempty"`
}

type wechatGetUpdatesResp struct {
	Ret                int             `json:"ret"`
	ErrCode            int             `json:"errcode,omitempty"`
	ErrMsg             string          `json:"errmsg,omitempty"`
	Messages           []wechatMessage `json:"msgs"`
	GetUpdatesBuf      string          `json:"get_updates_buf"`
	LongPollingTimeout int             `json:"longpolling_timeout_ms"`
}

type wechatSendMsgReq struct {
	Msg      *wechatMessage  `json:"msg"`
	BaseInfo *wechatBaseInfo `json:"base_info,omitempty"`
}

type wechatSendMsgResp struct {
	Ret     int    `json:"ret"`
	ErrCode int    `json:"errcode,omitempty"`
	ErrMsg  string `json:"errmsg,omitempty"`
}

type wechatGetConfigReq struct {
	ILinkUserID  string          `json:"ilink_user_id,omitempty"`
	ContextToken string          `json:"context_token,omitempty"`
	BaseInfo     *wechatBaseInfo `json:"base_info,omitempty"`
}

type wechatGetConfigResp struct {
	Ret          int    `json:"ret"`
	ErrCode      int    `json:"errcode,omitempty"`
	ErrMsg       string `json:"errmsg,omitempty"`
	TypingTicket string `json:"typing_ticket,omitempty"`
}

type wechatSendTypingReq struct {
	ILinkUserID  string          `json:"ilink_user_id,omitempty"`
	TypingTicket string          `json:"typing_ticket,omitempty"`
	Status       int             `json:"status,omitempty"`
	BaseInfo     *wechatBaseInfo `json:"base_info,omitempty"`
}

type wechatBaseInfo struct {
	ChannelVersion string `json:"channel_version"`
}

type wechatQRCodeResp struct {
	QRCode           string `json:"qrcode"`
	QRCodeImgURL     string `json:"qrcode_img_url,omitempty"`
	QRCodeImgContent string `json:"qrcode_img_content,omitempty"`
}

type wechatQRCodeStatus struct {
	Status   string `json:"status"`
	BotToken string `json:"bot_token"`
	BaseURL  string `json:"baseurl,omitempty"`
}

func generateUIN() string {
	n, _ := rand.Int(rand.Reader, new(big.Int).SetUint64(1<<32))
	return base64.StdEncoding.EncodeToString([]byte(n.String()))
}

func generateWechatClientID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("xiaoli-wechat-%d", time.Now().UnixNano())
	}
	return "xiaoli-wechat-" + hex.EncodeToString(b[:])
}

type wechatClient struct {
	baseURL string
	token   string
	httpDo  func(*http.Request) (*http.Response, error)
}

func newWechatClient() *wechatClient {
	return &wechatClient{
		baseURL: wechatDefaultBaseURL,
		httpDo:  http.DefaultClient.Do,
	}
}

func (c *wechatClient) postJSON(ctx context.Context, path string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("wechat marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("wechat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("X-WECHAT-UIN", generateUIN())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return nil, fmt.Errorf("wechat http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("wechat read: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("wechat http %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

func (c *wechatClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("wechat request: %w", err)
	}
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("X-WECHAT-UIN", generateUIN())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return nil, fmt.Errorf("wechat http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("wechat read: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("wechat http %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

func (s *AdminServer) startWechatPolling(ctx context.Context) {
	token := s.cfg.WeChatBotToken
	if token == "" {
		logger.Infof("[wechat] WECHAT_BOT_TOKEN not set, skipping")
		return
	}

	c := newWechatClient()
	c.token = token
	c.baseURL = s.cfg.WeChatBaseURL

	buf := ""
	logger.Infof("[wechat] polling started base_url=%s", c.baseURL)

	for {
		select {
		case <-ctx.Done():
			logger.Infof("[wechat] polling stopped")
			return
		default:
		}

		pollCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		raw, err := c.postJSON(pollCtx, "/ilink/bot/getupdates", &wechatGetUpdatesReq{
			GetUpdatesBuf: buf,
			BaseInfo:      &wechatBaseInfo{ChannelVersion: "1.0.3"},
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

		var resp wechatGetUpdatesResp
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
			if msg.MessageType != wechatMsgUser {
				continue
			}
			s.handleWechatMessage(ctx, c, msg)
		}
	}
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

func wechatGetTypingTicket(ctx context.Context, c *wechatClient, toUserID, contextToken string) (string, error) {
	req := &wechatGetConfigReq{
		ILinkUserID:  toUserID,
		ContextToken: contextToken,
		BaseInfo:     &wechatBaseInfo{ChannelVersion: "1.0.3"},
	}
	raw, err := c.postJSON(ctx, "/ilink/bot/getconfig", req)
	if err != nil {
		return "", err
	}
	var resp wechatGetConfigResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if resp.Ret != 0 {
		return "", fmt.Errorf("wechat getconfig ret=%d err=%s", resp.Ret, resp.ErrMsg)
	}
	if resp.TypingTicket == "" {
		return "", fmt.Errorf("wechat getconfig missing typing_ticket")
	}
	return resp.TypingTicket, nil
}

func wechatSendTyping(ctx context.Context, c *wechatClient, _ string, toUserID, contextToken string) error {
	ticket, err := wechatGetTypingTicket(ctx, c, toUserID, contextToken)
	if err != nil {
		return err
	}
	req := &wechatSendTypingReq{
		ILinkUserID:  toUserID,
		TypingTicket: ticket,
		Status:       1,
		BaseInfo:     &wechatBaseInfo{ChannelVersion: "1.0.3"},
	}
	raw, err := c.postJSON(ctx, "/ilink/bot/sendtyping", req)
	if err != nil {
		return err
	}
	return decodeWechatSendResponse(raw)
}

func wechatSendText(ctx context.Context, c *wechatClient, _ string, toUserID, contextToken, text string) error {
	msg := &wechatMessage{
		FromUserID:   "",
		ToUserID:     toUserID,
		ClientID:     generateWechatClientID(),
		MessageType:  wechatMsgBot,
		MessageState: 2,
		ContextToken: contextToken,
		ItemList: []wechatMsgItem{
			{Type: wechatItemText, TextItem: &wechatTextItem{Text: text}},
		},
	}
	req := &wechatSendMsgReq{
		Msg:      msg,
		BaseInfo: &wechatBaseInfo{ChannelVersion: "1.0.3"},
	}
	raw, err := c.postJSON(ctx, "/ilink/bot/sendmessage", req)
	if err != nil {
		return err
	}
	return decodeWechatSendResponse(raw)
}

func decodeWechatSendResponse(raw []byte) error {
	var resp wechatSendMsgResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.Ret != 0 {
		return fmt.Errorf("wechat send ret=%d err=%s", resp.Ret, resp.ErrMsg)
	}
	return nil
}

func wechatLogin(ctx context.Context, onQRCode func(content string)) error {
	c := newWechatClient()

	for attempt := 0; attempt < 3; attempt++ {
		raw, err := c.get(ctx, "/ilink/bot/get_bot_qrcode?bot_type="+wechatBotType)
		if err != nil {
			return fmt.Errorf("get QR code: %w", err)
		}
		var qrResp wechatQRCodeResp
		if err := json.Unmarshal(raw, &qrResp); err != nil {
			return fmt.Errorf("decode QR: %w", err)
		}

		if onQRCode != nil && qrResp.QRCodeImgContent != "" {
			onQRCode(qrResp.QRCodeImgContent)
		} else if qrResp.QRCodeImgURL != "" {
			logger.Infof("[wechat] scan QR to login: %s", qrResp.QRCodeImgURL)
		}

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		timeout := time.After(480 * time.Second)

	loop:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timeout:
				logger.Infof("[wechat] QR expired, retrying (%d/3)", attempt+1)
				break loop
			case <-ticker.C:
				statusRaw, err := c.get(ctx, "/ilink/bot/get_qrcode_status?qrcode="+qrResp.QRCode)
				if err != nil {
					continue
				}
				var status wechatQRCodeStatus
				if err := json.Unmarshal(statusRaw, &status); err != nil {
					continue
				}
				switch status.Status {
				case "confirmed":
					logger.Infof("[wechat] login successful!")
					fmt.Printf("\nWECHAT_BOT_TOKEN=%s\nWECHAT_BASE_URL=%s\n", status.BotToken, status.BaseURL)
					return nil
				case "expired":
					break loop
				case "scaned":
					logger.Infof("[wechat] QR scanned, waiting for confirmation")
				}
			}
		}
	}
	return fmt.Errorf("wechat login failed after 3 attempts")
}

func WechatLoginCLI(ctx context.Context) error {
	fmt.Println("正在获取二维码，请用微信扫码登录...")
	return wechatLogin(ctx, func(content string) {
		fmt.Println("扫码内容:", content)
	})
}
