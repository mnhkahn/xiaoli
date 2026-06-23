package admin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"xiaoli/server/internal/agent/session"
)

const (
	sessionCookie = "xiaoli_admin_session"
	stateCookie   = "xiaoli_admin_state"
)

var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"content-length":      {},
}

type AdminServer struct {
	cfg          Config
	signer       *signer
	bridge       *BridgeClient
	httpClient   *http.Client
	stream       *streamHub
	audioStore   *audioStore
	deviceHub    *DeviceHub
	conversation *ConversationPipeline
	memory       memoryReader
	agent        *EinoAgent
	imagesMu     sync.Mutex
	images       map[string]imageRecord
	imagesByDev  map[string][]string
	larkMu        sync.Mutex
	larkEvents    map[string]time.Time
	larkToken     string
	larkTokenExp  time.Time
	larkTokenMu   sync.Mutex
	oidcMu        sync.Mutex
	oidc         *oidcConfig
	oidcFetcher  func() (oidcConfig, error)
}

type imageRecord struct {
	ID          string
	DeviceID    string
	ContentType string
	Body        []byte
	CreatedAt   time.Time
}

type oidcConfig struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

func NewServer(cfg Config) *AdminServer {
	if cfg.SessionMaxAge == 0 {
		cfg.SessionMaxAge = 7 * 24 * time.Hour
	}
	client := &http.Client{Timeout: 125 * time.Second}
	stream := newStreamHub()
	audioStore := newAudioStore(cfg.now)
	asr := newOpenAITranscriber(cfg)
	agent := newEinoAgent(cfg)
	var memory memoryReader
	if agent != nil && agent.MemoryReader() != nil {
		memory = agent.MemoryReader()
	} else {
		memory = newRedisMemory(cfg)
	}
	vision := newGoVisionClient(cfg)
	tts := newHTTPSpeechSynthesizer(cfg, nil)
	deviceHub := NewDeviceHub(cfg, stream, audioStore, asr, agent, vision, tts)
	if agent != nil {
		agent.SetDeviceTools(deviceHub)
	}
	conversation := newConversationPipeline(agent, deviceHub)
	deviceHub.setConversation(conversation)
	s := &AdminServer{
		cfg:          cfg,
		signer:       newSigner(cfg.SessionSecret, cfg.now),
		httpClient:   client,
		bridge:       NewBridgeClient(cfg.BridgeBaseURL, client),
		stream:       stream,
		audioStore:   audioStore,
		agent:        agent,
		deviceHub:    deviceHub,
		conversation: conversation,
		memory:       memory,
		images:       map[string]imageRecord{},
		imagesByDev:  map[string][]string{},
		larkEvents:   map[string]time.Time{},
	}
	s.oidcFetcher = s.fetchOIDCConfig
	return s
}

func (s *AdminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/admin") {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
	}
	switch {
	case r.URL.Path == "/health":
		s.handleHealth(w, r)
	case r.URL.Path == "/xiaozhi/ota/" || r.URL.Path == "/xiaozhi/ota":
		s.handleXiaozhiOTA(w, r)
	case r.URL.Path == "/xiaozhi/v1/" || r.URL.Path == "/xiaozhi/v1":
		s.handleXiaozhiWebSocket(w, r)
	case r.URL.Path == "/lark/events":
		s.handleLarkEvents(w, r)
	case strings.HasPrefix(r.URL.Path, "/xiaoli/audio/"):
		s.handleDeviceAudio(w, r)
	case strings.HasPrefix(r.URL.Path, "/mcp/vision/"):
		s.handleVisionProxy(w, r)
	case r.URL.Path == "/admin/internal/stream/frame":
		s.handleInternalStreamFrame(w, r)
	case r.URL.Path == "/admin/internal/images/latest":
		s.handleInternalLatestImage(w, r)
	case r.URL.Path == "/admin" || r.URL.Path == "/admin/":
		s.handleIndex(w, r)
	case r.URL.Path == "/admin/memory":
		s.handleMemoryPage(w, r)
	case r.URL.Path == "/admin/login":
		s.handleLogin(w, r)
	case r.URL.Path == "/admin/callback":
		s.handleCallback(w, r)
	case r.URL.Path == "/admin/logout":
		s.handleLogout(w, r)
	case r.URL.Path == "/admin/api/me":
		s.withUser(w, r, s.handleMe)
	case r.URL.Path == "/admin/api/channels":
		s.withUser(w, r, s.handleChannels)
	case r.URL.Path == "/admin/api/devices":
		s.withUser(w, r, s.handleDevices)
	case r.URL.Path == "/admin/api/tools":
		s.withUser(w, r, s.handleTools)
	case r.URL.Path == "/admin/api/call":
		s.withUser(w, r, s.handleCall)
	case r.URL.Path == "/admin/api/schedules":
		s.withUser(w, r, s.handleSchedules)
	case r.URL.Path == "/admin/api/memory/channels":
		s.withUser(w, r, s.handleMemoryChannels)
	case r.URL.Path == "/admin/api/memory/sessions":
		s.withUser(w, r, s.handleMemorySessions)
	case r.URL.Path == "/admin/api/memory/session":
		s.withUser(w, r, s.handleMemorySessionByID)
	case r.URL.Path == "/admin/api/speak":
		s.withUser(w, r, s.handleSpeak)
	case r.URL.Path == "/admin/api/speak/stop":
		s.withUser(w, r, s.handleSpeakStop)
	case r.URL.Path == "/admin/api/snapshot":
		s.withUser(w, r, s.handleSnapshot)
	case r.URL.Path == "/admin/api/stream/start":
		s.withUser(w, r, s.handleStreamStart)
	case r.URL.Path == "/admin/api/stream/stop":
		s.withUser(w, r, s.handleStreamStop)
	case strings.HasPrefix(r.URL.Path, "/admin/api/images/"):
		s.withUser(w, r, s.handleImage)
	case r.URL.Path == "/admin/ws/stream":
		s.withUser(w, r, s.handleStreamWS)
	default:
		http.NotFound(w, r)
	}
}

func (s *AdminServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	user := s.getUser(r)
	if user == nil {
		s.loginRedirect(w, r, safeReturnTo(r.URL.RequestURI()))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, dashboardHTML(user))
}

func (s *AdminServer) handleMemoryPage(w http.ResponseWriter, r *http.Request) {
	user := s.getUser(r)
	if user == nil {
		s.loginRedirect(w, r, safeReturnTo(r.URL.RequestURI()))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, memoryHTML(user))
}

func (s *AdminServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	returnTo := safeReturnTo(r.URL.Query().Get("return_to"))
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.loginRedirect(w, r, returnTo)
}

func (s *AdminServer) loginRedirect(w http.ResponseWriter, r *http.Request, returnTo string) {
	returnTo = safeReturnTo(returnTo)
	oidc, err := s.getOIDCConfig()
	if err != nil {
		http.Error(w, "load oidc config failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	state := randomToken(24)
	nonce := randomToken(24)
	codeVerifier := randomToken(48)
	challengeBytes := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	now := s.cfg.now().Unix()
	stateValue, err := s.signer.sign(map[string]any{
		"state":         state,
		"nonce":         nonce,
		"code_verifier": codeVerifier,
		"return_to":     returnTo,
		"iat":           now,
		"exp":           now + 600,
	})
	if err != nil {
		http.Error(w, "cannot create login state", http.StatusInternalServerError)
		return
	}
	query := url.Values{
		"client_id":             {s.cfg.LogtoAppID},
		"redirect_uri":          {s.cfg.PublicBaseURL + "/admin/callback"},
		"response_type":         {"code"},
		"scope":                 {"openid profile email"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	http.SetCookie(w, signedCookie(stateCookie, stateValue, 10*time.Minute))
	http.Redirect(w, r, oidc.AuthorizationEndpoint+"?"+query.Encode(), http.StatusFound)
}

func (s *AdminServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}
	stateCookieValue, err := r.Cookie(stateCookie)
	if err != nil {
		http.Error(w, "missing login state", http.StatusBadRequest)
		return
	}
	expected, err := s.signer.verify(stateCookieValue.Value, 10*time.Minute)
	if err != nil || !hmac.Equal([]byte(stringValue(expected["state"])), []byte(state)) {
		http.Error(w, "invalid login state", http.StatusBadRequest)
		return
	}
	user, err := s.exchangeLogtoUser(r.Context(), code, stringValue(expected["code_verifier"]))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.userAllowed(user) {
		http.Error(w, "user is not allowed", http.StatusForbidden)
		return
	}
	now := s.cfg.now().Unix()
	session, err := s.signer.sign(map[string]any{
		"user": user,
		"iat":  now,
		"exp":  now + int64(s.cfg.SessionMaxAge.Seconds()),
	})
	if err != nil {
		http.Error(w, "cannot create session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, signedCookie(sessionCookie, session, s.cfg.SessionMaxAge))
	clearCookie(w, stateCookie)
	http.Redirect(w, r, safeReturnTo(stringValue(expected["return_to"])), http.StatusFound)
}

func (s *AdminServer) exchangeLogtoUser(ctx context.Context, code string, verifier string) (map[string]any, error) {
	oidc, err := s.getOIDCConfig()
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.cfg.PublicBaseURL + "/admin/callback"},
		"code_verifier": {verifier},
	}
	tokenPayload, err := s.postToken(ctx, oidc.TokenEndpoint, form, true)
	if err != nil {
		tokenPayload, err = s.postToken(ctx, oidc.TokenEndpoint, form, false)
		if err != nil {
			return nil, err
		}
	}
	accessToken := stringValue(tokenPayload["access_token"])
	if accessToken == "" {
		return nil, errors.New("missing access token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, oidc.UserinfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("load userinfo failed: %s", string(body))
	}
	var userinfo map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&userinfo); err != nil {
		return nil, err
	}
	return map[string]any{
		"sub":      userinfo["sub"],
		"email":    userinfo["email"],
		"name":     userinfo["name"],
		"username": userinfo["username"],
	}, nil
}

func (s *AdminServer) postToken(ctx context.Context, endpoint string, form url.Values, basic bool) (map[string]any, error) {
	body := url.Values{}
	for key, values := range form {
		for _, value := range values {
			body.Add(key, value)
		}
	}
	if !basic {
		body.Set("client_id", s.cfg.LogtoAppID)
		body.Set("client_secret", s.cfg.LogtoAppSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basic {
		req.SetBasicAuth(s.cfg.LogtoAppID, s.cfg.LogtoAppSecret)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *AdminServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	location := s.cfg.PublicBaseURL + "/admin"
	if oidc, err := s.getOIDCConfig(); err == nil && oidc.EndSessionEndpoint != "" {
		query := url.Values{
			"client_id":                {s.cfg.LogtoAppID},
			"post_logout_redirect_uri": {s.cfg.PublicBaseURL + "/admin"},
		}
		location = oidc.EndSessionEndpoint + "?" + query.Encode()
	}
	clearCookie(w, sessionCookie)
	clearCookie(w, stateCookie)
	http.Redirect(w, r, location, http.StatusFound)
}

func (s *AdminServer) handleMe(w http.ResponseWriter, r *http.Request, user map[string]any) {
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *AdminServer) handleChannels(w http.ResponseWriter, r *http.Request, user map[string]any) {
	channels, err := s.channels(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (s *AdminServer) handleDevices(w http.ResponseWriter, r *http.Request, user map[string]any) {
	devices, err := s.deviceController().Devices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *AdminServer) handleTools(w http.ResponseWriter, r *http.Request, user map[string]any) {
	result, err := s.deviceController().Tools(r.Context(), r.URL.Query().Get("device_id"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	result.Tools = normalizeAdminTools(result.Tools)
	writeJSON(w, http.StatusOK, result)
}

func normalizeAdminTools(tools []map[string]any) []map[string]any {
	normalized := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(stringValue(tool["name"]))
		if name == "" {
			continue
		}
		parameters := tool["inputSchema"]
		if parameters == nil {
			parameters = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		normalized = append(normalized, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": stringValue(tool["description"]),
				"parameters":  parameters,
			},
		})
	}
	return normalized
}

func (s *AdminServer) handleCall(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request BridgeCallRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if request.DeviceID == "" || request.Tool == "" {
		http.Error(w, "device_id and tool are required", http.StatusBadRequest)
		return
	}
	request.Timeout = normalizeMCPTimeout(request.Tool, request.Timeout)
	started := s.cfg.now()
	result, err := s.deviceController().Call(r.Context(), request)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	preview := s.buildResultPreviewForCall(request.DeviceID, request.Tool, result.Result, started.Add(-2*time.Second))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         result.OK,
		"result":     result.Result,
		"raw":        result.Raw,
		"error":      result.Error,
		"elapsed_ms": result.ElapsedMS,
		"preview":    preview,
	})
}

func (s *AdminServer) handleSchedules(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": s.schedules()})
}

func (s *AdminServer) schedules() []map[string]any {
	var out []map[string]any
	for _, def := range s.workflowDefinitions() {
		if def.Trigger.Kind != "cron" {
			continue
		}
		item := map[string]any{
			"id":          def.ID,
			"name":        def.Name,
			"description": def.Description,
			"enabled":     def.Enabled,
			"agent":       def.Agent.Name,
			"mode":        def.Agent.Mode,
			"max_steps":   def.Agent.MaxSteps,
		}
		if def.Trigger.Cron != nil {
			spec := def.Trigger.Cron
			item["timezone"] = spec.Timezone
			if spec.Every > 0 {
				item["interval_seconds"] = int(spec.Every.Seconds())
				item["window"] = fmt.Sprintf("%02d:00-%02d:00", spec.StartHour, spec.EndHour)
			}
			if spec.AtHour != nil && spec.AtMinute != nil {
				item["time"] = fmt.Sprintf("%02d:%02d", *spec.AtHour, *spec.AtMinute)
			}
		}
		for key, value := range def.Metadata {
			if _, exists := item[key]; !exists {
				item[key] = value
			}
		}
		out = append(out, item)
	}
	return out
}

func (s *AdminServer) handleMemoryChannels(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	prefix := s.memoryPrefix()
	if s.agent == nil || s.agent.SessionManager() == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":  false,
			"prefix":   prefix,
			"channels": []session.ChannelEntry{},
		})
		return
	}
	entries, err := s.agent.SessionManager().ListChannels(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"enabled": true, "prefix": prefix, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":  true,
		"prefix":   prefix,
		"channels": entries,
	})
}

func (s *AdminServer) handleMemorySessions(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	chName := strings.TrimSpace(r.URL.Query().Get("channel_name"))
	chUser := strings.TrimSpace(r.URL.Query().Get("channel_user"))
	prefix := s.memoryPrefix()
	if s.agent == nil || s.agent.SessionManager() == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "prefix": prefix})
		return
	}
	sm := s.agent.SessionManager()

	currentID := sm.GetChannelSession(r.Context(), chName, chUser)
	sessions, err := sm.ListByChannel(r.Context(), chName, chUser)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current_session_id": currentID,
		"sessions":           sessions,
	})
}

func (s *AdminServer) handleMemorySessionByID(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("id"))
	if sessionID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	order := normalizeMemoryOrder(r.URL.Query().Get("order"))
	prefix := s.memoryPrefix()
	if s.agent == nil || s.agent.SessionManager() == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "prefix": prefix})
		return
	}
	sm := s.agent.SessionManager()

	info, err := sm.Get(r.Context(), sessionID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	msgs := sm.LoadMessages(r.Context(), sessionID)
	if order == "newest" {
		reverseMessages(msgs)
	}

	type msgItem struct {
		Index            int               `json:"index"`
		Role             string            `json:"role"`
		Content          string            `json:"content"`
		ReasoningContent string            `json:"reasoning_content,omitempty"`
		ToolCalls        []schema.ToolCall `json:"tool_calls,omitempty"`
		ToolCallID       string            `json:"tool_call_id,omitempty"`
		FinishReason     string            `json:"finish_reason,omitempty"`
	}
	items := make([]msgItem, 0, len(msgs))
	for i, m := range msgs {
		origIndex := i
		if order == "newest" {
			origIndex = len(msgs) - 1 - i
		}
		item := msgItem{
			Index:            origIndex,
			Role:             string(m.Role),
			Content:          m.Content,
			ReasoningContent: m.ReasoningContent,
			ToolCalls:        m.ToolCalls,
			ToolCallID:       m.ToolCallID,
		}
		if m.ResponseMeta != nil {
			item.FinishReason = m.ResponseMeta.FinishReason
		}
		items = append(items, item)
	}

	payload := map[string]any{
		"enabled":       true,
		"prefix":        prefix,
		"session_id":    sessionID,
		"info":          info,
		"message_count": len(items),
		"messages":      items,
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *AdminServer) memoryPrefix() string {
	if s.memory != nil && s.memory.Prefix() != "" {
		return s.memory.Prefix()
	}
	return s.cfg.RedisKeyPrefix
}

func normalizeMemoryOrder(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "oldest") {
		return "oldest"
	}
	return "newest"
}

func reverseMessages(messages []*schema.Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}

func (s *AdminServer) handleSpeak(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		DeviceID string `json:"device_id"`
		Text     string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(request.DeviceID) == "" || strings.TrimSpace(request.Text) == "" {
		http.Error(w, "device_id and text are required", http.StatusBadRequest)
		return
	}
	result, err := s.deviceController().Speak(r.Context(), request.DeviceID, request.Text)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *AdminServer) handleSpeakStop(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(request.DeviceID) == "" {
		http.Error(w, "device_id is required", http.StatusBadRequest)
		return
	}
	result, err := s.deviceController().StopSpeak(r.Context(), request.DeviceID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *AdminServer) handleSnapshot(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		DeviceID   string `json:"device_id"`
		Resolution string `json:"resolution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(request.DeviceID) == "" {
		http.Error(w, "device_id is required", http.StatusBadRequest)
		return
	}
	resolution := normalizeSnapshotResolution(request.Resolution)
	started := s.cfg.now()
	result, err := s.deviceController().Call(r.Context(), BridgeCallRequest{
		DeviceID: request.DeviceID,
		Tool:     "self.camera.snapshot",
		Arguments: map[string]any{
			"resolution": resolution,
		},
		Timeout: 60,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	preview := s.buildResultPreviewForCall(request.DeviceID, "self.camera.snapshot", result.Result, started.Add(-2*time.Second))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         result.OK,
		"result":     result.Result,
		"raw":        result.Raw,
		"error":      result.Error,
		"elapsed_ms": result.ElapsedMS,
		"preview":    preview,
	})
}

func (s *AdminServer) handleStreamStart(w http.ResponseWriter, r *http.Request, user map[string]any) {
	s.handleCameraStreamTool(w, r, "self.camera.start_stream")
}

func (s *AdminServer) handleStreamStop(w http.ResponseWriter, r *http.Request, user map[string]any) {
	s.handleCameraStreamTool(w, r, "self.camera.stop_stream")
}

func (s *AdminServer) handleCameraStreamTool(w http.ResponseWriter, r *http.Request, tool string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	deviceID := stringValue(body["device_id"])
	if deviceID == "" {
		http.Error(w, "device_id is required", http.StatusBadRequest)
		return
	}
	args := map[string]any{}
	if tool == "self.camera.start_stream" {
		args["fps"] = clampInt(body["fps"], 1, 3, 1)
		args["duration_sec"] = clampInt(body["duration_sec"], 1, 60, 30)
		args["resolution"] = normalizeStreamResolution(stringValue(body["resolution"]))
		args["transport"] = normalizeStreamTransport(firstNonEmptyString(stringValue(body["transport"]), "lan"))
	}
	result, err := s.deviceController().Call(r.Context(), BridgeCallRequest{
		DeviceID:  deviceID,
		Tool:      tool,
		Arguments: args,
		Timeout:   10,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *AdminServer) handleImage(w http.ResponseWriter, r *http.Request, user map[string]any) {
	id := path.Base(r.URL.Path)
	s.imagesMu.Lock()
	record, ok := s.images[id]
	s.imagesMu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", record.ContentType)
	_, _ = w.Write(record.Body)
}

func (s *AdminServer) withUser(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request, map[string]any)) {
	user := s.getUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	next(w, r, user)
}

func (s *AdminServer) getUser(r *http.Request) map[string]any {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	payload, err := s.signer.verify(cookie.Value, 0)
	if err != nil {
		return nil
	}
	user, ok := payload["user"].(map[string]any)
	if !ok || !s.userAllowed(user) {
		return nil
	}
	return user
}

func (s *AdminServer) userAllowed(user map[string]any) bool {
	if len(s.cfg.AllowedUsers) == 0 {
		return true
	}
	allowed := map[string]struct{}{}
	for _, item := range s.cfg.AllowedUsers {
		allowed[item] = struct{}{}
	}
	if _, ok := allowed["*"]; ok {
		return true
	}
	for _, key := range []string{"sub", "email", "username", "name"} {
		if _, ok := allowed[stringValue(user[key])]; ok {
			return true
		}
	}
	return false
}

func (s *AdminServer) getOIDCConfig() (oidcConfig, error) {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	if s.oidc != nil {
		return *s.oidc, nil
	}
	cfg, err := s.oidcFetcher()
	if err != nil {
		return oidcConfig{}, err
	}
	s.oidc = &cfg
	return cfg, nil
}

func (s *AdminServer) fetchOIDCConfig() (oidcConfig, error) {
	if s.cfg.LogtoEndpoint == "/" || s.cfg.LogtoEndpoint == "" {
		return oidcConfig{}, errors.New("LOGTO_ENDPOINT is required")
	}
	discoveryURL := strings.TrimRight(s.cfg.LogtoEndpoint, "/") + "/oidc/.well-known/openid-configuration"
	req, err := http.NewRequest(http.MethodGet, discoveryURL, nil)
	if err != nil {
		return oidcConfig{}, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return oidcConfig{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return oidcConfig{}, fmt.Errorf("discovery failed: %s", string(body))
	}
	var cfg oidcConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return oidcConfig{}, err
	}
	return cfg, nil
}

func safeReturnTo(returnTo string) string {
	if !strings.HasPrefix(returnTo, "/admin") {
		return "/admin"
	}
	return returnTo
}

func signedCookie(name, value string, maxAge time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/admin",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}

func randomToken(bytesLen int) string {
	raw := make([]byte, bytesLen)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func clampInt(value any, min int, max int, fallback int) int {
	parsed, ok := int64Value(value)
	if !ok {
		return fallback
	}
	if int(parsed) < min {
		return min
	}
	if int(parsed) > max {
		return max
	}
	return int(parsed)
}

func normalizeMCPTimeout(toolName string, requested int) int {
	if requested <= 0 {
		requested = 30
	}
	if longRunningMCPTool(toolName) && requested < 120 {
		requested = 120
	}
	if requested > 120 {
		return 120
	}
	return requested
}

func longRunningMCPTool(toolName string) bool {
	normalized := strings.ToLower(toolName)
	for _, marker := range []string{"camera", "photo", "vision", "image", "snapshot", "拍照", "摄像"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func normalizeSnapshotResolution(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "qvga", "vga", "svga", "xga", "uxga", "legacy_vga":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "vga"
	}
}

func normalizeStreamResolution(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "qqvga", "qvga", "vga", "svga":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "qqvga"
	}
}

func normalizeStreamTransport(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "lan", "remote", "auto":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "lan"
	}
}

func (s *AdminServer) handleVisionProxy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Infof("[vision] read body failed: %v", err)
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	contentType := r.Header.Get("Content-Type")
	deviceID := r.Header.Get("device-id")
	logger.Infof("[vision] %s from device=%s content-type=%s body=%d bytes", r.URL.Path, deviceID, contentType, len(body))

	if r.URL.Path == "/mcp/vision/snapshot" {
		s.handleVisionSnapshot(w, r, body, contentType, deviceID)
		return
	}
	if r.URL.Path == "/mcp/vision/explain" && s.cfg.DirectDeviceServer {
		s.handleVisionExplain(w, r, body, contentType, deviceID)
		return
	}
	if r.URL.Path == "/mcp/vision/stream/frame" {
		s.handleStreamFrame(w, r, body, contentType, deviceID)
		return
	}
	if image, ok := s.extractVisionImage(contentType, body); ok && deviceID != "" {
		s.storeVisionImage(deviceID, image.ContentType, image.Body)
	}
	targetURL := s.cfg.VisionProxyBaseURL + r.URL.RequestURI()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		logger.Infof("[vision] create upstream request failed: %v", err)
		http.Error(w, "create upstream request failed", http.StatusInternalServerError)
		return
	}
	copyProxyHeaders(req.Header, r.Header)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		logger.Infof("[vision] upstream failed for %s: %v", targetURL, err)
		http.Error(w, "vision upstream failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	logger.Infof("[vision] upstream response for %s: status=%d", r.URL.Path, resp.StatusCode)
	for key, values := range resp.Header {
		if _, skip := hopByHopHeaders[strings.ToLower(key)]; skip {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *AdminServer) handleVisionSnapshot(w http.ResponseWriter, r *http.Request, body []byte, contentType string, deviceID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if deviceID == "" {
		logger.Infof("[vision] snapshot from empty device-id")
		http.Error(w, "missing device-id", http.StatusBadRequest)
		return
	}
	image, ok := s.extractVisionImage(contentType, body)
	if !ok {
		logger.Infof("[vision] snapshot from %s: no image data", deviceID)
		http.Error(w, "missing image snapshot", http.StatusBadRequest)
		return
	}
	if len(image.Body) > 2*1024*1024 {
		logger.Infof("[vision] snapshot from %s: image too large: %d bytes", deviceID, len(image.Body))
		http.Error(w, "image snapshot too large", http.StatusRequestEntityTooLarge)
		return
	}
	fields := multipartFields(contentType, body)
	resolution := normalizeSnapshotResolution(firstNonEmptyString(fields["resolution"], r.Header.Get("X-Xiaoli-Resolution")))
	width := intHeaderOrField(fields["width"], r.Header.Get("X-Xiaoli-Width"))
	height := intHeaderOrField(fields["height"], r.Header.Get("X-Xiaoli-Height"))
	imageID := s.storeVisionImage(deviceID, image.ContentType, image.Body)
	imageURL := "/admin/api/images/" + imageID
	event := s.publishFrame(deviceID, image.ContentType, base64.StdEncoding.EncodeToString(image.Body), map[string]string{
		"stream_id":    "snapshot-" + imageID,
		"seq":          "0",
		"timestamp_ms": fmt.Sprintf("%d", s.cfg.now().UnixMilli()),
	})
	logger.Infof("[vision] snapshot from %s: stored=%s bytes=%d resolution=%s", deviceID, imageID, len(image.Body), resolution)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"image_url":    imageURL,
		"content_type": image.ContentType,
		"bytes":        len(image.Body),
		"resolution":   resolution,
		"width":        width,
		"height":       height,
		"stream_id":    event.StreamID,
	})
}

func (s *AdminServer) handleStreamFrame(w http.ResponseWriter, r *http.Request, body []byte, contentType string, deviceID string) {
	if deviceID == "" {
		http.Error(w, "missing device-id", http.StatusBadRequest)
		return
	}
	image, ok := s.extractVisionImage(contentType, body)
	if !ok {
		http.Error(w, "missing image frame", http.StatusBadRequest)
		return
	}
	if len(image.Body) > 1024*1024 {
		http.Error(w, "image frame too large", http.StatusRequestEntityTooLarge)
		return
	}
	fields := multipartFields(contentType, body)
	event := s.publishFrame(deviceID, image.ContentType, base64.StdEncoding.EncodeToString(image.Body), map[string]string{
		"stream_id":    fields["stream_id"],
		"seq":          fields["seq"],
		"timestamp_ms": fields["timestamp_ms"],
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "seq": event.Seq, "stream_id": event.StreamID})
}

func (s *AdminServer) handleInternalStreamFrame(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.InternalStreamToken == "" || !hmac.Equal([]byte(r.Header.Get("X-Xiaoli-Internal-Token")), []byte(s.cfg.InternalStreamToken)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request struct {
		DeviceID    string `json:"device_id"`
		ContentType string `json:"content_type"`
		Data        string `json:"data"`
		StreamID    string `json:"stream_id"`
		Seq         string `json:"seq"`
		TimestampMS string `json:"timestamp_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if request.DeviceID == "" || request.Data == "" {
		http.Error(w, "device_id and data are required", http.StatusBadRequest)
		return
	}
	event := s.publishFrame(request.DeviceID, normalizeImageContentType(request.ContentType, ""), request.Data, map[string]string{
		"stream_id":    request.StreamID,
		"seq":          request.Seq,
		"timestamp_ms": request.TimestampMS,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "seq": event.Seq, "stream_id": event.StreamID})
}

func (s *AdminServer) handleInternalLatestImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.InternalStreamToken == "" || !hmac.Equal([]byte(r.Header.Get("X-Xiaoli-Internal-Token")), []byte(s.cfg.InternalStreamToken)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	if deviceID == "" {
		http.Error(w, "device_id is required", http.StatusBadRequest)
		return
	}
	record := s.recentDeviceImageRecord(deviceID, time.Unix(0, 0))
	if record == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", record.ContentType)
	_, _ = w.Write(record.Body)
}

type extractedImage struct {
	ContentType string
	Body        []byte
}

func (s *AdminServer) extractVisionImage(contentType string, body []byte) (extractedImage, bool) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "image/") {
		return extractedImage{ContentType: mediaType, Body: body}, true
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		return extractedImage{}, false
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
		payload, _ := io.ReadAll(part)
		partType := part.Header.Get("Content-Type")
		name := strings.ToLower(part.FormName())
		filename := strings.ToLower(part.FileName())
		imageField := name == "image" || name == "photo" || name == "picture" || name == "file"
		imageFile := strings.HasSuffix(filename, ".jpg") || strings.HasSuffix(filename, ".jpeg") || strings.HasSuffix(filename, ".png") || strings.HasSuffix(filename, ".webp") || strings.HasSuffix(filename, ".gif")
		if len(payload) > 0 && (strings.HasPrefix(strings.ToLower(partType), "image/") || imageField || imageFile) {
			return extractedImage{ContentType: normalizeImageContentType(partType, filename), Body: payload}, true
		}
	}
	return extractedImage{}, false
}

func multipartFields(contentType string, body []byte) map[string]string {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return nil
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	fields := map[string]string{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
		if part.FormName() == "" || part.FileName() != "" {
			continue
		}
		payload, _ := io.ReadAll(part)
		fields[part.FormName()] = strings.TrimSpace(string(payload))
	}
	return fields
}

func normalizeImageContentType(contentType string, filename string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if strings.HasPrefix(contentType, "image/") {
		return contentType
	}
	filename = strings.ToLower(filename)
	switch {
	case strings.HasSuffix(filename, ".png"):
		return "image/png"
	case strings.HasSuffix(filename, ".webp"):
		return "image/webp"
	case strings.HasSuffix(filename, ".gif"):
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func intHeaderOrField(values ...string) int {
	for _, value := range values {
		parsed, ok := int64Value(strings.TrimSpace(value))
		if ok && parsed > 0 {
			return int(parsed)
		}
	}
	return 0
}

func (s *AdminServer) storeVisionImage(deviceID string, contentType string, body []byte) string {
	s.imagesMu.Lock()
	defer s.imagesMu.Unlock()
	now := s.cfg.now()
	cutoff := now.Add(-10 * time.Minute)
	for id, record := range s.images {
		if record.CreatedAt.Before(cutoff) {
			delete(s.images, id)
		}
	}
	id := randomToken(16)
	record := imageRecord{
		ID:          id,
		DeviceID:    deviceID,
		ContentType: normalizeImageContentType(contentType, ""),
		Body:        append([]byte(nil), body...),
		CreatedAt:   now,
	}
	s.images[id] = record
	ids := append(s.imagesByDev[deviceID], id)
	for len(ids) > 8 {
		delete(s.images, ids[0])
		ids = ids[1:]
	}
	s.imagesByDev[deviceID] = ids
	return id
}

func (s *AdminServer) recentDeviceImageURLs(deviceID string, since time.Time) []string {
	s.imagesMu.Lock()
	defer s.imagesMu.Unlock()
	var urls []string
	ids := s.imagesByDev[deviceID]
	for i := len(ids) - 1; i >= 0; i-- {
		record, ok := s.images[ids[i]]
		if !ok {
			continue
		}
		if record.CreatedAt.Before(since) {
			break
		}
		urls = append(urls, "/admin/api/images/"+record.ID)
		if len(urls) >= 3 {
			break
		}
	}
	return urls
}

func (s *AdminServer) publishFrame(deviceID string, contentType string, encodedBody string, metadata map[string]string) StreamEvent {
	contentType = normalizeImageContentType(contentType, "")
	return s.stream.publish(StreamEvent{
		Type:        "frame",
		DeviceID:    deviceID,
		ContentType: contentType,
		Image:       "data:" + contentType + ";base64," + encodedBody,
		Size:        len(encodedBody) * 3 / 4,
		TS:          float64(s.cfg.now().UnixNano()) / 1e9,
		StreamID:    metadata["stream_id"],
		Seq:         metadata["seq"],
		TimestampMS: metadata["timestamp_ms"],
	})
}

func (s *AdminServer) buildResultPreviewForCall(deviceID string, toolName string, value any, since time.Time) map[string]any {
	var extra []string
	if strings.Contains(toolName, "camera") || strings.Contains(toolName, "photo") || strings.Contains(toolName, "拍照") {
		extra = s.recentDeviceImageURLs(deviceID, since)
	}
	preview := buildResultPreview(value, extra)
	if len(preview.Images) > 1 {
		preview.Images = preview.Images[:1]
	}
	return map[string]any{"images": preview.Images, "text": strings.Join(preview.Texts, "\n\n")}
}

type resultPreview struct {
	Images []string
	Texts  []string
}

func buildResultPreview(value any, extraImages []string) resultPreview {
	preview := resultPreview{}
	seenImages := map[string]struct{}{}
	seenTexts := map[string]struct{}{}
	addImage := func(src string) {
		if src == "" || len(preview.Images) >= 8 {
			return
		}
		if _, ok := seenImages[src]; ok {
			return
		}
		seenImages[src] = struct{}{}
		preview.Images = append(preview.Images, src)
	}
	addText := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" || len(preview.Texts) >= 8 {
			return
		}
		if _, ok := seenTexts[text]; ok {
			return
		}
		seenTexts[text] = struct{}{}
		preview.Texts = append(preview.Texts, text)
	}
	for _, src := range extraImages {
		addImage(src)
	}
	var walk func(any, string)
	walk = func(node any, key string) {
		switch typed := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for childKey := range typed {
				keys = append(keys, childKey)
			}
			sort.Strings(keys)
			for _, childKey := range keys {
				walk(typed[childKey], childKey)
			}
		case []any:
			for _, item := range typed {
				walk(item, key)
			}
		case string:
			normalized := normalizeKey(key)
			if src := imageSrc(normalized, typed); src != "" {
				addImage(src)
			} else if isTextKey(normalized) {
				addText(typed)
			}
		}
	}
	walk(value, "")
	return preview
}

func imageSrc(normalizedKey string, raw string) string {
	raw = strings.TrimSpace(raw)
	imageLikeKey := strings.Contains(normalizedKey, "image") || strings.Contains(normalizedKey, "photo") || strings.Contains(normalizedKey, "picture") || strings.Contains(normalizedKey, "thumbnail") || normalizedKey == "url" || normalizedKey == "base64"
	if strings.HasPrefix(raw, "data:image/") {
		return raw
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		clean := strings.ToLower(strings.SplitN(raw, "?", 2)[0])
		if imageLikeKey || strings.HasSuffix(clean, ".jpg") || strings.HasSuffix(clean, ".jpeg") || strings.HasSuffix(clean, ".png") || strings.HasSuffix(clean, ".webp") || strings.HasSuffix(clean, ".gif") {
			return raw
		}
	}
	if imageLikeKey && looksBase64(raw) {
		return "data:image/jpeg;base64," + strings.Join(strings.Fields(raw), "")
	}
	return ""
}

func isTextKey(key string) bool {
	for _, item := range []string{"description", "explain", "analysis", "text", "message", "answer", "caption", "summary", "response"} {
		if strings.Contains(key, item) {
			return true
		}
	}
	return false
}

func normalizeKey(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func looksBase64(raw string) bool {
	raw = strings.Join(strings.Fields(raw), "")
	if len(raw) < 16 {
		return false
	}
	for _, r := range raw {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=') {
			return false
		}
	}
	return true
}

func copyProxyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if _, skip := hopByHopHeaders[strings.ToLower(key)]; skip {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
