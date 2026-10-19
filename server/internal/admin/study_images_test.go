package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentmedia "github.com/mnhkahn/xiaoli/internal/agent/media"
	agentworkflow "github.com/mnhkahn/xiaoli/internal/agent/workflow"
)

func TestStudyMonitorSendsBothImagesWithExistingTabletProtocol(t *testing.T) {
	for _, failScreen := range []bool{false, true} {
		name := "both uploaded"
		if failScreen {
			name = "screen upload rejected"
		}
		t.Run(name, func(t *testing.T) {
			var srv *AdminServer
			bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/bridge/devices":
					writeJSON(w, 200, map[string]any{"devices": []map[string]string{{"device_id": "tablet"}}})
				case "/bridge/call":
					var call BridgeCallRequest
					if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
						t.Error(err)
						return
					}
					if call.Tool != "self.camera.take_photo" {
						t.Errorf("tool=%s", call.Tool)
					}
					question, _ := call.Arguments["question"].(string)
					if !strings.Contains(question, "不得用屏幕截图替代") {
						t.Errorf("question=%s", question)
					}
					srv.storeVisionCapture("tablet", question, "", []agentmedia.VisionImage{{Label: "相机照片", ContentType: "image/jpeg", Body: []byte("camera-bytes")}, {Label: "平板当前屏幕截图", ContentType: "image/jpeg", Body: []byte("screen-bytes")}})
					writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": `{"need_reminder":false,"camera":"相机里有人","screen":"显示家长设置","summary":"无法判断学习状态"}`}}}})
				default:
					t.Errorf("unexpected bridge request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer bridge.Close()
			cfg := testConfig()
			cfg.BridgeBaseURL = bridge.URL
			cfg.LarkAppID = "app"
			cfg.LarkAppToken = "secret"
			cfg.LarkWebhookURL = "https://webhook.test/hook"
			srv = NewServer(cfg)
			srv.storeVisionCapture("tablet", "old", "", []agentmedia.VisionImage{{Label: "相机照片", ContentType: "image/jpeg", Body: []byte("stale")}})
			var uploads []string
			var webhook string
			srv.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/open-apis/auth/v3/tenant_access_token/internal":
					return jsonResponse(200, map[string]any{"code": 0, "tenant_access_token": "token", "expire": 7200}), nil
				case "/open-apis/im/v1/images":
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Fatal(err)
					}
					file, _, err := r.FormFile("image")
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					raw, _ := io.ReadAll(file)
					uploads = append(uploads, string(raw))
					if failScreen && string(raw) == "screen-bytes" {
						return jsonResponse(200, map[string]any{"code": 123, "msg": "screen rejected"}), nil
					}
					return jsonResponse(200, map[string]any{"code": 0, "data": map[string]string{"image_key": "key-" + string(raw)}}), nil
				case "/hook":
					raw, _ := io.ReadAll(r.Body)
					webhook = string(raw)
					return jsonResponse(200, map[string]any{"code": 0}), nil
				default:
					t.Errorf("unexpected HTTP request %s", r.URL)
					return jsonResponse(500, map[string]any{}), nil
				}
			})}
			err := srv.runStudyMonitorOnce(context.Background(), agentworkflow.Definition{ID: "study_monitor"}, cfg.now())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(uploads, ",") != "camera-bytes,screen-bytes" {
				t.Fatalf("uploads=%v", uploads)
			}
			for _, want := range []string{"相机照片", "平板当前屏幕截图", "key-camera-bytes", "信息不足", "检查问题："} {
				if !strings.Contains(webhook, want) {
					t.Fatalf("missing %q in %s", want, webhook)
				}
			}
			if failScreen {
				if strings.Contains(webhook, "key-screen-bytes") || !strings.Contains(webhook, "screen rejected") {
					t.Fatal(webhook)
				}
			} else if !strings.Contains(webhook, "key-screen-bytes") {
				t.Fatal(webhook)
			}
			if strings.Contains(webhook, "stale") || strings.Contains(webhook, "结论：状态正常") {
				t.Fatal(webhook)
			}
		})
	}
}

func TestStudyNotificationReportsWebhookRejection(t *testing.T) {
	cfg := testConfig()
	cfg.LarkWebhookURL = "https://webhook.test/hook"
	srv := NewServer(cfg)
	srv.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, map[string]any{"code": 9499, "msg": "unsupported content"}), nil
	})}
	err := srv.sendLarkStudyMessage(context.Background(), studyLarkPayloadInput{CheckedAt: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "unsupported content") {
		t.Fatalf("error=%v", err)
	}
}
