package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	agentchannel "xiaoli/server/internal/agent/channel"
)

// ChannelSendTool provides channel_send tool
type ChannelSendTool struct {
	senders struct {
		lark   agentchannel.Sender
		esp32  agentchannel.Sender
		wechat agentchannel.Sender
	}
	allowedRoots []string
}

// ChannelSendConfig configures the ChannelSendTool
type ChannelSendConfig struct {
	Lark         agentchannel.Sender
	ESP32        agentchannel.Sender
	WeChat       agentchannel.Sender
	AllowedRoots []string // Trusted directories for file operations
}

func NewChannelSendTool(cfg ChannelSendConfig) tool.InvokableTool {
	t := &ChannelSendTool{
		allowedRoots: cfg.AllowedRoots,
	}
	t.senders.lark = cfg.Lark
	t.senders.esp32 = cfg.ESP32
	t.senders.wechat = cfg.WeChat
	return t
}

func (t *ChannelSendTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "channel_send",
		Desc: `Send messages and files to the current conversation channel.

Use this tool when:
- You have generated a PDF/image/file locally and want to share it to the user
- You need to send a separate text message to the user
- You want to send an attachment with a caption

Target is always "current" for the active conversation.

For ESP32 devices: images are shown on display; PDFs and other files are announced via TTS.

For Lark: images and files are uploaded and sent as attachments.

For WeChat: only text works; files will send the caption but not the file.`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"target": {
				Type:     schema.String,
				Desc:     "Send target. Always use \"current\" to send to current conversation.",
				Enum:     []string{"current"},
				Required: true,
			},
			"text": {
				Type: schema.String,
				Desc: "Text content to send.",
			},
			"file_path": {
				Type: schema.String,
				Desc: "Local absolute path to file to send (PDF, image, etc.). Must be in /tmp or other trusted location.",
			},
			"file_display_name": {
				Type: schema.String,
				Desc: "User-friendly name for the file (defaults to basename of file_path).",
			},
			"display_name": {
				Type: schema.String,
				Desc: "Alias of file_display_name.",
			},
			"mime_type": {
				Type: schema.String,
				Desc: "Optional MIME type override, for example application/pdf or image/png.",
			},
			"caption": {
				Type: schema.String,
				Desc: "Text caption to display alongside the file attachment.",
			},
		}),
	}, nil
}

func (t *ChannelSendTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args struct {
		Target          string `json:"target"`
		Text            string `json:"text"`
		FilePath        string `json:"file_path"`
		FileDisplayName string `json:"file_display_name"`
		DisplayName     string `json:"display_name"`
		MIMEType        string `json:"mime_type"`
		Caption         string `json:"caption"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return errorResponse("invalid JSON arguments: %v", err), nil
	}

	if args.Target != "current" {
		return errorResponse("target must be \"current\""), nil
	}

	target, ok := agentchannel.SendTargetFromContext(ctx)
	if !ok {
		return errorResponse("unknown channel context, cannot send message"), nil
	}

	var sender agentchannel.Sender
	switch target.Channel {
	case agentchannel.TypeLark:
		sender = t.senders.lark
	case agentchannel.TypeESP32:
		sender = t.senders.esp32
	case agentchannel.TypeWechat:
		sender = t.senders.wechat
	default:
		return errorResponse("unsupported channel type: %s", target.Channel), nil
	}

	if sender == nil {
		return errorResponse("sender not configured for channel: %s", target.Channel), nil
	}

	// Send attachment if file path provided
	if args.FilePath != "" {
		displayName := args.FileDisplayName
		if displayName == "" {
			displayName = args.DisplayName
		}
		attachment, err := t.validateAndBuildAttachment(args.FilePath, displayName, args.MIMEType)
		if err != nil {
			return errorResponse("invalid file: %v", err), nil
		}
		caption := args.Caption
		if caption == "" {
			caption = args.Text
		}
		if err := sender.SendAttachment(ctx, target, attachment, caption); err != nil {
			return errorResponse("send attachment failed: %v", err), nil
		}
		return successResponse("file sent successfully"), nil
	}

	// Send text only
	if args.Text == "" && args.Caption != "" {
		args.Text = args.Caption
	}
	if args.Text != "" {
		if err := sender.SendText(ctx, target, args.Text); err != nil {
			return errorResponse("send text failed: %v", err), nil
		}
		return successResponse("text sent successfully"), nil
	}

	return errorResponse("either text or file_path must be provided"), nil
}

func (t *ChannelSendTool) validateAndBuildAttachment(filePath, displayName, mimeType string) (agentchannel.Attachment, error) {
	att := agentchannel.Attachment{
		Path: filePath,
	}

	if err := agentchannel.ValidatePath(filePath, t.allowedRoots); err != nil {
		return att, err
	}

	// Get file info
	info, err := os.Stat(filePath)
	if err != nil {
		return att, fmt.Errorf("cannot stat file: %w", err)
	}
	att.Size = info.Size()

	// Display name
	if displayName != "" {
		att.DisplayName = filepath.Base(displayName)
	} else {
		att.DisplayName = info.Name()
	}

	if mimeType != "" {
		att.MIMEType = mimeType
	} else {
		// Detect MIME type
		f, err := os.Open(filePath)
		if err == nil {
			buf := make([]byte, 512)
			n, _ := f.Read(buf)
			f.Close()
			if n > 0 {
				att.MIMEType = http.DetectContentType(buf[:n])
			}
		}
	}
	if att.MIMEType == "" {
		att.MIMEType = "application/octet-stream"
	}

	return att, nil
}

func successResponse(message string) string {
	raw, _ := json.Marshal(map[string]any{"ok": true, "message": message})
	return string(raw)
}

func errorResponse(format string, args ...interface{}) string {
	raw, _ := json.Marshal(map[string]any{"ok": false, "error": fmt.Sprintf(format, args...)})
	return string(raw)
}
