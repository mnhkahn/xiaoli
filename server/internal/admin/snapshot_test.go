package admin

import (
	"bytes"
	"encoding/json"
	agentmedia "github.com/mnhkahn/xiaoli/internal/agent/media"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Model the existing tablet: upload two images over HTTP, return only analysis text over MCP.
func TestSnapshotButtonPreviewsTwoImagesWithTextOnlyTabletResult(t *testing.T) {
	for _, question := range []string{"请看看在做什么", ""} {
		t.Run(question, func(t *testing.T) {
			var srv *AdminServer
			bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var call BridgeCallRequest
				if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
					t.Error(err)
					return
				}
				if call.Tool != "self.camera.take_photo" || call.DeviceID != "tablet" || call.Timeout != 120 {
					t.Errorf("call=%+v", call)
				}
				q, _ := call.Arguments["question"].(string)
				if q == "" || (question != "" && q != question) {
					t.Errorf("question=%q", q)
				}
				body, contentType := visionPairBody(t, "image", "screenshot")
				upload := httptest.NewRequest(http.MethodPost, "/mcp/vision/explain", bytes.NewReader(body))
				upload.Header.Set("Device-Id", "tablet")
				upload.Header.Set("Content-Type", contentType)
				rr := httptest.NewRecorder()
				srv.handleVisionExplain(rr, upload, body, contentType, "tablet")
				if rr.Code != 200 {
					t.Errorf("upload status=%d body=%s", rr.Code, rr.Body)
				}
				writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": "正在看微信"}}}})
			}))
			defer bridge.Close()
			cfg := testConfig()
			cfg.DirectDeviceServer = false
			cfg.BridgeBaseURL = bridge.URL
			cfg.DeviceAuthEnabled = false
			srv = NewServer(cfg)
			srv.deviceHub.vision = &pairedVisionAnalyzer{}
			srv.storeVisionImage("tablet", "image/jpeg", []byte("unrelated"))
			request, _ := json.Marshal(map[string]string{"device_id": "tablet", "question": question})
			rr := httptest.NewRecorder()
			srv.handleSnapshot(rr, httptest.NewRequest(http.MethodPost, "/admin/api/snapshot", bytes.NewReader(request)), nil)
			if rr.Code != 200 {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
			var response struct {
				Question string `json:"question"`
				Tool     string `json:"tool"`
				Preview  struct {
					Images  []string            `json:"images"`
					Text    string              `json:"text"`
					Details []map[string]string `json:"image_details"`
				} `json:"preview"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Tool != "self.camera.take_photo" || response.Question == "" || len(response.Preview.Images) != 2 || len(response.Preview.Details) != 2 || response.Preview.Text != "正在看微信" {
				t.Fatalf("response=%s", rr.Body)
			}
			for i, src := range response.Preview.Images {
				img := httptest.NewRecorder()
				srv.handleImage(img, httptest.NewRequest(http.MethodGet, src, nil), nil)
				expected := []string{"image-bytes", "screenshot-bytes"}[i]
				if img.Code != 200 || img.Body.String() != expected {
					t.Fatalf("image=%s body=%s", src, img.Body)
				}
			}
		})
	}
}

func TestCapturePreviewNeverMixesOlderImagesAndKeepsScreenshotError(t *testing.T) {
	srv := NewServer(testConfig())
	srv.storeVisionCapture("tablet", "旧问题", "", []agentmedia.VisionImage{{Label: "旧照片", ContentType: "image/jpeg", Body: []byte("old-photo")}, {Label: "旧截图", ContentType: "image/jpeg", Body: []byte("old-screen")}})
	srv.storeVisionCapture("tablet", "本次问题", "未授权", []agentmedia.VisionImage{{Label: "相机照片", ContentType: "image/jpeg", Body: []byte("new-photo")}})
	preview := srv.buildResultPreviewForCall("tablet", "self.camera.take_photo", map[string]any{"content": []any{map[string]any{"text": "本次分析", "type": "text"}}}, time.Time{})
	if len(preview["images"].([]string)) != 1 || preview["screenshot_error"] != "未授权" || preview["question"] != "本次问题" {
		t.Fatalf("preview=%v", preview)
	}
	empty := srv.buildResultPreviewForCall("tablet", "self.camera.take_photo", nil, srv.cfg.now().Add(time.Second))
	if len(empty["images"].([]string)) != 0 {
		t.Fatalf("displayed stale images: %v", empty)
	}
}
