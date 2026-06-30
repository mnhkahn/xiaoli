package wechat

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultBaseURL    = "https://ilinkai.weixin.qq.com"
	DefaultCDNBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"
	BotType           = "3"
)

type UploadMediaType int

const (
	UploadMediaTypeImage UploadMediaType = 1
	UploadMediaTypeVideo UploadMediaType = 2
	UploadMediaTypeFile  UploadMediaType = 3
	UploadMediaTypeVoice UploadMediaType = 4
)

type MessageType int

const (
	MsgUser MessageType = 1
	MsgBot  MessageType = 2
)

type ItemType int

const (
	ItemText  ItemType = 1
	ItemImage ItemType = 2
	ItemVoice ItemType = 3
	ItemFile  ItemType = 4
	ItemVideo ItemType = 5
)

type Message struct {
	Seq          int         `json:"seq,omitempty"`
	MessageID    int64       `json:"message_id,omitempty"`
	FromUserID   string      `json:"from_user_id,omitempty"`
	ToUserID     string      `json:"to_user_id,omitempty"`
	ClientID     string      `json:"client_id,omitempty"`
	CreateTimeMs int64       `json:"create_time_ms,omitempty"`
	SessionID    string      `json:"session_id,omitempty"`
	MessageType  MessageType `json:"message_type"`
	MessageState int         `json:"message_state"`
	ContextToken string      `json:"context_token,omitempty"`
	ItemList     []MsgItem   `json:"item_list,omitempty"`
}

func (m *Message) Text() string {
	for _, item := range m.ItemList {
		if item.Type == ItemText && item.TextItem != nil {
			return item.TextItem.Text
		}
	}
	return ""
}

func (m *Message) Images() []*ImageItem {
	var images []*ImageItem
	for _, item := range m.ItemList {
		if item.Type == ItemImage && item.ImageItem != nil {
			images = append(images, item.ImageItem)
		}
	}
	return images
}

type MsgItem struct {
	Type      ItemType   `json:"type"`
	TextItem  *TextItem  `json:"text_item,omitempty"`
	ImageItem *ImageItem `json:"image_item,omitempty"`
	VoiceItem *VoiceItem `json:"voice_item,omitempty"`
	FileItem  *FileItem  `json:"file_item,omitempty"`
	VideoItem *VideoItem `json:"video_item,omitempty"`
}

type TextItem struct {
	Text string `json:"text"`
}

type ImageItem struct {
	Media     *CDNMedia `json:"media,omitempty"`
	AESKey    string    `json:"aes_key,omitempty"`
	AESKeyHex string    `json:"aeskey,omitempty"`
	MidSize   int       `json:"mid_size,omitempty"`
}

type VoiceItem struct {
	Media    *CDNMedia `json:"media,omitempty"`
	Duration int       `json:"duration,omitempty"`
	Text     string    `json:"text,omitempty"`
}

type FileItem struct {
	Media    *CDNMedia `json:"media,omitempty"`
	FileName string    `json:"file_name,omitempty"`
	Length   string    `json:"len,omitempty"`
}

type VideoItem struct {
	Media      *CDNMedia `json:"media,omitempty"`
	VideoSize  int       `json:"video_size,omitempty"`
	PlayLength int       `json:"play_length,omitempty"`
}

type CDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param,omitempty"`
	AESKey            string `json:"aes_key,omitempty"`
	EncryptType       int    `json:"encrypt_type,omitempty"`
	FullURL           string `json:"full_url,omitempty"`
}

type GetUpdatesRequest struct {
	GetUpdatesBuf string    `json:"get_updates_buf"`
	BaseInfo      *BaseInfo `json:"base_info,omitempty"`
}

type GetUpdatesResponse struct {
	Ret                int       `json:"ret"`
	ErrCode            int       `json:"errcode,omitempty"`
	ErrMsg             string    `json:"errmsg,omitempty"`
	Messages           []Message `json:"msgs"`
	GetUpdatesBuf      string    `json:"get_updates_buf"`
	LongPollingTimeout int       `json:"longpolling_timeout_ms"`
}

type SendMessageRequest struct {
	Msg      *Message  `json:"msg"`
	BaseInfo *BaseInfo `json:"base_info,omitempty"`
}

type SendMessageResponse struct {
	Ret     int    `json:"ret"`
	ErrCode int    `json:"errcode,omitempty"`
	ErrMsg  string `json:"errmsg,omitempty"`
}

type GetConfigRequest struct {
	ILinkUserID  string    `json:"ilink_user_id,omitempty"`
	ContextToken string    `json:"context_token,omitempty"`
	BaseInfo     *BaseInfo `json:"base_info,omitempty"`
}

type GetUploadURLRequest struct {
	FileKey     string          `json:"filekey,omitempty"`
	MediaType   UploadMediaType `json:"media_type,omitempty"`
	ToUserID    string          `json:"to_user_id,omitempty"`
	RawSize     int             `json:"rawsize,omitempty"`
	RawFileMD5  string          `json:"rawfilemd5,omitempty"`
	FileSize    int             `json:"filesize,omitempty"`
	NoNeedThumb bool            `json:"no_need_thumb,omitempty"`
	AESKey      string          `json:"aeskey,omitempty"`
	BaseInfo    *BaseInfo       `json:"base_info,omitempty"`
}

type GetUploadURLResponse struct {
	Ret           int    `json:"ret,omitempty"`
	ErrMsg        string `json:"errmsg,omitempty"`
	UploadParam   string `json:"upload_param,omitempty"`
	UploadFullURL string `json:"upload_full_url,omitempty"`
}

type UploadedMedia struct {
	FileKey            string
	DownloadQueryParam string
	AESKeyHex          string
	FileSize           int
	CiphertextSize     int
}

type GetConfigResponse struct {
	Ret          int    `json:"ret"`
	ErrCode      int    `json:"errcode,omitempty"`
	ErrMsg       string `json:"errmsg,omitempty"`
	TypingTicket string `json:"typing_ticket,omitempty"`
}

type SendTypingRequest struct {
	ILinkUserID  string    `json:"ilink_user_id,omitempty"`
	TypingTicket string    `json:"typing_ticket,omitempty"`
	Status       int       `json:"status,omitempty"`
	BaseInfo     *BaseInfo `json:"base_info,omitempty"`
}

type BaseInfo struct {
	ChannelVersion string `json:"channel_version"`
}

type QRCodeResponse struct {
	QRCode           string `json:"qrcode"`
	QRCodeImgURL     string `json:"qrcode_img_url,omitempty"`
	QRCodeImgContent string `json:"qrcode_img_content,omitempty"`
}

type QRCodeStatus struct {
	Status   string `json:"status"`
	BotToken string `json:"bot_token"`
	BaseURL  string `json:"baseurl,omitempty"`
}

type ClientConfig struct {
	BaseURL string
	Token   string
	HTTPDo  func(*http.Request) (*http.Response, error)
}

type Client struct {
	BaseURL string
	Token   string
	HTTPDo  func(*http.Request) (*http.Response, error)
}

func NewClient(cfg ClientConfig) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	httpDo := cfg.HTTPDo
	if httpDo == nil {
		httpDo = http.DefaultClient.Do
	}
	return &Client{
		BaseURL: baseURL,
		Token:   cfg.Token,
		HTTPDo:  httpDo,
	}
}

func (c *Client) PostJSON(ctx context.Context, path string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("wechat marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("wechat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("X-WECHAT-UIN", generateUIN())
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPDo(req)
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

func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("wechat request: %w", err)
	}
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("X-WECHAT-UIN", generateUIN())
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPDo(req)
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

func (c *Client) DownloadImage(ctx context.Context, image *ImageItem) (string, []byte, error) {
	if image == nil || image.Media == nil {
		return "", nil, fmt.Errorf("wechat image missing media URL")
	}
	ref := strings.TrimSpace(image.Media.EncryptQueryParam)
	fullURL := strings.TrimSpace(image.Media.FullURL)
	if ref == "" && fullURL == "" {
		return "", nil, fmt.Errorf("wechat image missing media URL")
	}
	endpoint := ""
	useBotAuth := false
	switch {
	case fullURL != "":
		endpoint = fullURL
	case strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://"):
		endpoint = ref
	case strings.HasPrefix(ref, "/"):
		endpoint = strings.TrimRight(c.BaseURL, "/") + ref
		useBotAuth = true
	case ref != "":
		endpoint = strings.TrimRight(DefaultCDNBaseURL, "/") + "/download?encrypted_query_param=" + url.QueryEscape(ref)
	default:
		return "", nil, fmt.Errorf("wechat image media format is not supported yet")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", nil, fmt.Errorf("wechat image request: %w", err)
	}
	if useBotAuth {
		req.Header.Set("AuthorizationType", "ilink_bot_token")
		req.Header.Set("X-WECHAT-UIN", generateUIN())
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
	}
	resp, err := c.HTTPDo(req)
	if err != nil {
		return "", nil, fmt.Errorf("wechat image http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 3*1024*1024))
	if err != nil {
		return "", nil, fmt.Errorf("wechat image read: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", nil, fmt.Errorf("wechat image http %d: %s", resp.StatusCode, string(raw))
	}
	if key, ok, err := imageAESKey(image); err != nil {
		return "", nil, err
	} else if ok {
		decrypted, err := decryptAESECBPKCS7(raw, key)
		if err != nil {
			return "", nil, fmt.Errorf("wechat image decrypt: %w", err)
		}
		raw = decrypted
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}
	return contentType, raw, nil
}

func (c *Client) UploadMedia(ctx context.Context, path, toUserID string, mediaType UploadMediaType) (UploadedMedia, error) {
	plaintext, err := os.ReadFile(path)
	if err != nil {
		return UploadedMedia{}, fmt.Errorf("wechat read upload file: %w", err)
	}
	fileKeyBytes := make([]byte, 16)
	if _, err := rand.Read(fileKeyBytes); err != nil {
		return UploadedMedia{}, fmt.Errorf("wechat generate filekey: %w", err)
	}
	aesKey := make([]byte, aes.BlockSize)
	if _, err := rand.Read(aesKey); err != nil {
		return UploadedMedia{}, fmt.Errorf("wechat generate aeskey: %w", err)
	}
	fileKey := hex.EncodeToString(fileKeyBytes)
	aesKeyHex := hex.EncodeToString(aesKey)
	rawMD5 := md5.Sum(plaintext)
	encrypted := encryptAESECBPKCS7(plaintext, aesKey)

	uploadURL, err := c.getUploadURL(ctx, &GetUploadURLRequest{
		FileKey:     fileKey,
		MediaType:   mediaType,
		ToUserID:    toUserID,
		RawSize:     len(plaintext),
		RawFileMD5:  hex.EncodeToString(rawMD5[:]),
		FileSize:    len(encrypted),
		NoNeedThumb: true,
		AESKey:      aesKeyHex,
		BaseInfo:    &BaseInfo{ChannelVersion: "1.0.3"},
	})
	if err != nil {
		return UploadedMedia{}, err
	}
	downloadParam, err := c.uploadEncryptedMedia(ctx, uploadURL, fileKey, encrypted)
	if err != nil {
		return UploadedMedia{}, err
	}
	return UploadedMedia{
		FileKey:            fileKey,
		DownloadQueryParam: downloadParam,
		AESKeyHex:          aesKeyHex,
		FileSize:           len(plaintext),
		CiphertextSize:     len(encrypted),
	}, nil
}

func (c *Client) getUploadURL(ctx context.Context, req *GetUploadURLRequest) (*GetUploadURLResponse, error) {
	raw, err := c.PostJSON(ctx, "/ilink/bot/getuploadurl", req)
	if err != nil {
		return nil, err
	}
	var resp GetUploadURLResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("wechat getuploadurl decode: %w", err)
	}
	if resp.Ret != 0 {
		return nil, fmt.Errorf("wechat getuploadurl ret=%d err=%s", resp.Ret, resp.ErrMsg)
	}
	if strings.TrimSpace(resp.UploadFullURL) == "" && strings.TrimSpace(resp.UploadParam) == "" {
		return nil, fmt.Errorf("wechat getuploadurl missing upload URL")
	}
	return &resp, nil
}

func (c *Client) uploadEncryptedMedia(ctx context.Context, uploadURL *GetUploadURLResponse, fileKey string, encrypted []byte) (string, error) {
	endpoint := strings.TrimSpace(uploadURL.UploadFullURL)
	if endpoint == "" {
		endpoint = strings.TrimRight(DefaultCDNBaseURL, "/") + "/upload?encrypted_query_param=" + url.QueryEscape(uploadURL.UploadParam) + "&filekey=" + url.QueryEscape(fileKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encrypted))
	if err != nil {
		return "", fmt.Errorf("wechat cdn upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.HTTPDo(req)
	if err != nil {
		return "", fmt.Errorf("wechat cdn upload http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("wechat cdn upload http %d: %s", resp.StatusCode, string(raw))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wechat cdn upload status %d: %s", resp.StatusCode, string(raw))
	}
	downloadParam := strings.TrimSpace(resp.Header.Get("X-Encrypted-Param"))
	if downloadParam == "" {
		return "", fmt.Errorf("wechat cdn upload missing x-encrypted-param")
	}
	return downloadParam, nil
}

func (c *Client) SendImageAttachment(ctx context.Context, toUserID, contextToken, path, caption string) error {
	uploaded, err := c.UploadMedia(ctx, path, toUserID, UploadMediaTypeImage)
	if err != nil {
		return err
	}
	item := MsgItem{
		Type: ItemImage,
		ImageItem: &ImageItem{
			Media: &CDNMedia{
				EncryptQueryParam: uploaded.DownloadQueryParam,
				AESKey:            base64.StdEncoding.EncodeToString([]byte(uploaded.AESKeyHex)),
				EncryptType:       1,
			},
			MidSize: uploaded.CiphertextSize,
		},
	}
	return c.sendMediaItems(ctx, toUserID, contextToken, caption, item)
}

func (c *Client) SendFileAttachment(ctx context.Context, toUserID, contextToken, path, fileName, caption string) error {
	uploaded, err := c.UploadMedia(ctx, path, toUserID, UploadMediaTypeFile)
	if err != nil {
		return err
	}
	item := MsgItem{
		Type: ItemFile,
		FileItem: &FileItem{
			Media: &CDNMedia{
				EncryptQueryParam: uploaded.DownloadQueryParam,
				AESKey:            base64.StdEncoding.EncodeToString([]byte(uploaded.AESKeyHex)),
				EncryptType:       1,
			},
			FileName: fileName,
			Length:   fmt.Sprintf("%d", uploaded.FileSize),
		},
	}
	return c.sendMediaItems(ctx, toUserID, contextToken, caption, item)
}

func (c *Client) sendMediaItems(ctx context.Context, toUserID, contextToken, caption string, item MsgItem) error {
	if caption != "" {
		if err := SendText(ctx, c, "", toUserID, contextToken, caption); err != nil {
			return err
		}
	}
	msg := &Message{
		ToUserID:     toUserID,
		ClientID:     GenerateClientID(),
		MessageType:  MsgBot,
		MessageState: 2,
		ContextToken: contextToken,
		ItemList:     []MsgItem{item},
	}
	raw, err := c.PostJSON(ctx, "/ilink/bot/sendmessage", &SendMessageRequest{
		Msg:      msg,
		BaseInfo: &BaseInfo{ChannelVersion: "1.0.3"},
	})
	if err != nil {
		return err
	}
	var resp SendMessageResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.Ret != 0 {
		return fmt.Errorf("wechat sendmessage ret=%d err=%s", resp.Ret, resp.ErrMsg)
	}
	return nil
}

func imageAESKey(image *ImageItem) ([]byte, bool, error) {
	if image == nil {
		return nil, false, nil
	}
	if keyHex := strings.TrimSpace(image.AESKeyHex); keyHex != "" {
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, false, fmt.Errorf("wechat image aeskey hex: %w", err)
		}
		if len(key) != aes.BlockSize {
			return nil, false, fmt.Errorf("wechat image aeskey hex length = %d, want 16", len(key))
		}
		return key, true, nil
	}
	keyBase64 := ""
	if image.Media != nil {
		keyBase64 = strings.TrimSpace(image.Media.AESKey)
	}
	if keyBase64 == "" {
		keyBase64 = strings.TrimSpace(image.AESKey)
	}
	if keyBase64 == "" {
		return nil, false, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, false, fmt.Errorf("wechat image aes_key base64: %w", err)
	}
	if len(decoded) == aes.BlockSize {
		return decoded, true, nil
	}
	if len(decoded) == 32 {
		hexText := string(decoded)
		if isHexString(hexText) {
			key, err := hex.DecodeString(hexText)
			if err != nil {
				return nil, false, fmt.Errorf("wechat image aes_key hex: %w", err)
			}
			if len(key) == aes.BlockSize {
				return key, true, nil
			}
		}
	}
	return nil, false, fmt.Errorf("wechat image aes_key decoded length = %d, want 16 raw bytes or 32 hex chars", len(decoded))
}

func decryptAESECBPKCS7(ciphertext, key []byte) ([]byte, error) {
	if len(key) != aes.BlockSize {
		return nil, fmt.Errorf("key length = %d, want 16", len(key))
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length = %d, want positive multiple of 16", len(ciphertext))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	for offset := 0; offset < len(ciphertext); offset += aes.BlockSize {
		block.Decrypt(plain[offset:offset+aes.BlockSize], ciphertext[offset:offset+aes.BlockSize])
	}
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(plain) {
		return nil, fmt.Errorf("invalid pkcs7 padding")
	}
	for i := len(plain) - padding; i < len(plain); i++ {
		if int(plain[i]) != padding {
			return nil, fmt.Errorf("invalid pkcs7 padding")
		}
	}
	return plain[:len(plain)-padding], nil
}

func encryptAESECBPKCS7(plaintext, key []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(append([]byte{}, plaintext...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		block.Encrypt(encrypted[offset:offset+aes.BlockSize], padded[offset:offset+aes.BlockSize])
	}
	return encrypted
}

func isHexString(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func GetTypingTicket(ctx context.Context, c *Client, toUserID, contextToken string) (string, error) {
	req := &GetConfigRequest{
		ILinkUserID:  toUserID,
		ContextToken: contextToken,
		BaseInfo:     &BaseInfo{ChannelVersion: "1.0.3"},
	}
	raw, err := c.PostJSON(ctx, "/ilink/bot/getconfig", req)
	if err != nil {
		return "", err
	}
	var resp GetConfigResponse
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

func SendTyping(ctx context.Context, c *Client, _ string, toUserID, contextToken string) error {
	ticket, err := GetTypingTicket(ctx, c, toUserID, contextToken)
	if err != nil {
		return err
	}
	req := &SendTypingRequest{
		ILinkUserID:  toUserID,
		TypingTicket: ticket,
		Status:       1,
		BaseInfo:     &BaseInfo{ChannelVersion: "1.0.3"},
	}
	raw, err := c.PostJSON(ctx, "/ilink/bot/sendtyping", req)
	if err != nil {
		return err
	}
	return decodeSendResponse(raw)
}

func SendText(ctx context.Context, c *Client, _ string, toUserID, contextToken, text string) error {
	msg := &Message{
		FromUserID:   "",
		ToUserID:     toUserID,
		ClientID:     GenerateClientID(),
		MessageType:  MsgBot,
		MessageState: 2,
		ContextToken: contextToken,
		ItemList: []MsgItem{
			{Type: ItemText, TextItem: &TextItem{Text: text}},
		},
	}
	req := &SendMessageRequest{
		Msg:      msg,
		BaseInfo: &BaseInfo{ChannelVersion: "1.0.3"},
	}
	raw, err := c.PostJSON(ctx, "/ilink/bot/sendmessage", req)
	if err != nil {
		return err
	}
	return decodeSendResponse(raw)
}

func Login(ctx context.Context, onQRCode func(content string), onStatus func(status QRCodeStatus)) error {
	c := NewClient(ClientConfig{})

	for attempt := 0; attempt < 3; attempt++ {
		raw, err := c.Get(ctx, "/ilink/bot/get_bot_qrcode?bot_type="+BotType)
		if err != nil {
			return fmt.Errorf("get QR code: %w", err)
		}
		var qrResp QRCodeResponse
		if err := json.Unmarshal(raw, &qrResp); err != nil {
			return fmt.Errorf("decode QR: %w", err)
		}

		if onQRCode != nil && qrResp.QRCodeImgContent != "" {
			onQRCode(qrResp.QRCodeImgContent)
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
				break loop
			case <-ticker.C:
				statusRaw, err := c.Get(ctx, "/ilink/bot/get_qrcode_status?qrcode="+qrResp.QRCode)
				if err != nil {
					continue
				}
				var status QRCodeStatus
				if err := json.Unmarshal(statusRaw, &status); err != nil {
					continue
				}
				if onStatus != nil {
					onStatus(status)
				}
				switch status.Status {
				case "confirmed":
					return nil
				case "expired":
					break loop
				}
			}
		}
	}
	return fmt.Errorf("wechat login failed after 3 attempts")
}

func decodeSendResponse(raw []byte) error {
	var resp SendMessageResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.Ret != 0 {
		return fmt.Errorf("wechat send ret=%d err=%s", resp.Ret, resp.ErrMsg)
	}
	return nil
}

func GenerateClientID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("xiaoli-wechat-%d", time.Now().UnixNano())
	}
	return "xiaoli-wechat-" + hex.EncodeToString(b[:])
}

func generateUIN() string {
	n, _ := rand.Int(rand.Reader, new(big.Int).SetUint64(1<<32))
	return base64.StdEncoding.EncodeToString([]byte(n.String()))
}
