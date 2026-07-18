package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestAndroidPairingIssuesPerDeviceTokenAndAuthorizesWebSocket(t *testing.T) {
	cfg := testConfig()
	cfg.DataDir = t.TempDir()
	cfg.DeviceAuthEnabled = true
	cfg.DeviceAuthKey = "legacy-esp32-token"
	cfg.AllowedDeviceIDs = []string{"legacy-board"}
	srv := NewServer(cfg)

	code, _, err := srv.deviceRegistry.CreatePairing("logto-user-1")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"code":"` + code + `","device_id":"homework-tablet-1234","device_name":"小明的学习平板","device_kind":"android"}`
	req := httptest.NewRequest(http.MethodPost, "/xiaozhi/pair", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pair status = %d body=%s", rr.Code, rr.Body.String())
	}
	var paired struct {
		WebSocket struct {
			URL   string `json:"url"`
			Token string `json:"token"`
		} `json:"websocket"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &paired); err != nil {
		t.Fatal(err)
	}
	if paired.WebSocket.Token == "" || paired.WebSocket.Token == cfg.DeviceAuthKey {
		t.Fatalf("pair token = %q, want a unique device token", paired.WebSocket.Token)
	}
	if !srv.deviceRegistry.Owns("logto-user-1", "homework-tablet-1234") {
		t.Fatal("paired device is not bound to the Logto user that created the code")
	}

	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	u, _ := url.Parse(httpSrv.URL)
	wsURL := "ws://" + u.Host + "/xiaozhi/v1/"
	headers := http.Header{"Device-Id": []string{"homework-tablet-1234"}, "Authorization": []string{paired.WebSocket.Token}}
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("dial paired device: %v response=%v", err, response)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello","features":{"mcp":true,"audio":false}}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read paired hello: %v", err)
	}

	badHeaders := http.Header{"Device-Id": []string{"homework-tablet-1234"}, "Authorization": []string{"wrong"}}
	bad, response, err := websocket.DefaultDialer.Dial(wsURL, badHeaders)
	if err == nil {
		bad.Close()
		t.Fatal("dial with bad device token succeeded")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token status = %v, want 401", response)
	}
}
