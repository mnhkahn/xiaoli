package esp32

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mnhkahn/gogogo/logger"

	esp32ws "github.com/mnhkahn/xiaoli-esp32/server/internal/esp32/ws"
)

const (
	directDeviceAudioSampleRate      = AudioSampleRate
	directDeviceAudioChannels        = AudioChannels
	directDeviceAudioFrameDurationMS = AudioFrameDurationMS
	assistantAudioPrebufferPacketNum = 5
)

type DeviceController interface {
	Devices(ctx context.Context) ([]Device, error)
	Tools(ctx context.Context, deviceID string) (ToolListResponse, error)
	Call(ctx context.Context, request BridgeCallRequest) (BridgeCallResult, error)
	Speak(ctx context.Context, deviceID string, text string) (map[string]any, error)
	StopSpeak(ctx context.Context, deviceID string) (map[string]any, error)
}

type HubConfig struct {
	PublicBaseURL     string
	DeviceAuthKey     string
	AllowedDeviceIDs  []string
	DeviceAuthEnabled bool
}

type StreamEvent struct {
	Type        string  `json:"type"`
	DeviceID    string  `json:"device_id"`
	ContentType string  `json:"content_type"`
	Image       string  `json:"image"`
	Size        int     `json:"size"`
	TS          float64 `json:"ts"`
	StreamID    string  `json:"stream_id"`
	Seq         string  `json:"seq"`
	TimestampMS string  `json:"timestamp_ms"`
}

type StreamPublisher interface {
	Publish(StreamEvent)
}

type SpeechRecognizer interface {
	Transcribe(ctx context.Context, oggOpus []byte) (string, error)
}

type SpeechSynthesizer interface {
	Synthesize(ctx context.Context, text string) (contentType string, body []byte, err error)
}

type Conversation interface {
	AnswerDeviceText(ctx context.Context, deviceID string, text string) (string, error)
}

type Dependencies struct {
	Stream                    StreamPublisher
	ASR                       SpeechRecognizer
	TTS                       SpeechSynthesizer
	Conversation              Conversation
	NewVoiceDetector          func() (VoiceDetector, error)
	BuildOggOpus              func(frames [][]byte, inputSampleRate int, channels int, frameDurationMS int) ([]byte, error)
	ExtractOpusPackets        func(body []byte) ([][]byte, time.Duration)
	ReencodeOpusFrames        func(packets [][]byte, sampleRate int, srcFrameDuration time.Duration, targetFrameDurationMs int) ([][]byte, time.Duration, error)
	NormalizeImageContentType func(contentType string, filename string) string
}

type Hub struct {
	cfg  HubConfig
	deps Dependencies

	mu       sync.Mutex
	sessions map[string]*Session
}

type Session struct {
	hub          *Hub
	deviceID     string
	sessionID    string
	clientIP     string
	connectedAt  time.Time
	lastActivity time.Time
	conn         net.Conn

	writeMu  sync.Mutex
	mu       sync.Mutex
	closed   bool
	mcpReady bool
	tools    []map[string]any
	nextID   int
	pending  map[int]chan MCPCallResult

	voice *VoiceRecorder
}

func NewHub(cfg HubConfig, deps Dependencies) *Hub {
	return &Hub{
		cfg:      cfg,
		deps:     deps,
		sessions: map[string]*Session{},
	}
}

func (h *Hub) SetTTS(tts SpeechSynthesizer) {
	h.deps.TTS = tts
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimSpace(r.Header.Get("Device-Id"))
	if deviceID == "" {
		deviceID = strings.TrimSpace(r.Header.Get("Client-Id"))
	}
	if deviceID == "" {
		http.Error(w, "missing Device-Id", http.StatusBadRequest)
		return
	}
	if !h.deviceAllowed(deviceID) {
		http.Error(w, "device is not allowed", http.StatusForbidden)
		return
	}
	if !h.deviceAuthorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	peer, err := esp32ws.Accept(w, r)
	if err != nil {
		return
	}
	session := h.register(deviceID, peer.Conn, clientIP(r))
	defer h.unregister(session)
	defer peer.Conn.Close()

	for {
		opcode, payload, err := esp32ws.ReadFrame(peer.Reader)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				// The device reconnects on network errors; no response is possible here.
			}
			_ = esp32ws.WriteFrame(peer.Conn, esp32ws.OpcodeClose, nil)
			return
		}
		session.touch()
		switch opcode {
		case esp32ws.OpcodeText:
			logger.Infof("ws text from %s: %s", session.deviceID, string(payload))
			h.handleText(session, payload)
		case esp32ws.OpcodeBinary:
			h.handleAudio(session, payload)
		case esp32ws.OpcodePing:
			_ = session.writeFrame(esp32ws.OpcodePong, payload)
		case esp32ws.OpcodeClose:
			_ = session.writeFrame(esp32ws.OpcodeClose, nil)
			return
		}
	}
}

func (h *Hub) Devices(ctx context.Context) ([]Device, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	devices := make([]Device, 0, len(h.sessions))
	for _, session := range h.sessions {
		session.mu.Lock()
		devices = append(devices, Device{
			DeviceID:     session.deviceID,
			SessionID:    session.sessionID,
			ClientIP:     session.clientIP,
			MCPReady:     session.mcpReady,
			ToolCount:    len(session.tools),
			ConnectedAt:  float64(session.connectedAt.UnixNano()) / 1e9,
			LastActivity: float64(session.lastActivity.UnixNano()) / 1e9,
		})
		session.mu.Unlock()
	}
	return devices, nil
}

func (h *Hub) Tools(ctx context.Context, deviceID string) (ToolListResponse, error) {
	session := h.session(deviceID)
	if session == nil {
		return ToolListResponse{}, fmt.Errorf("device is not online")
	}
	tools, ready := session.toolSnapshot()
	return ToolListResponse{Tools: tools, Ready: ready}, nil
}

func (h *Hub) ToolSnapshot(deviceID string) ([]map[string]any, bool) {
	session := h.session(deviceID)
	if session == nil {
		return nil, false
	}
	return session.toolSnapshot()
}

func (h *Hub) Call(ctx context.Context, request BridgeCallRequest) (BridgeCallResult, error) {
	if request.Arguments == nil {
		request.Arguments = map[string]any{}
	}
	session := h.session(request.DeviceID)
	if session == nil {
		return BridgeCallResult{}, fmt.Errorf("device is not online")
	}
	timeout := time.Duration(request.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	result, err := session.callMCP(callCtx, "tools/call", BuildToolsCallParams(request.Tool, request.Arguments))
	elapsed := int(time.Since(started) / time.Millisecond)
	if err != nil {
		return BridgeCallResult{}, err
	}
	return BridgeCallResult{
		OK:        result.Error == "",
		Result:    result.Result,
		Raw:       result.Raw,
		Error:     result.Error,
		ElapsedMS: elapsed,
	}, nil
}

func (h *Hub) Speak(ctx context.Context, deviceID string, text string) (map[string]any, error) {
	session := h.session(deviceID)
	if session == nil {
		return nil, fmt.Errorf("device is not online")
	}
	if h.deps.TTS == nil {
		return nil, fmt.Errorf("Go TTS is not configured")
	}
	if err := h.playAssistantText(ctx, session, text); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":        true,
		"status":    "played",
		"device_id": deviceID,
	}, nil
}

func (h *Hub) StopSpeak(ctx context.Context, deviceID string) (map[string]any, error) {
	session := h.session(deviceID)
	if session == nil {
		return nil, fmt.Errorf("device is not online")
	}
	if err := session.writeJSON(map[string]any{"type": "tts", "state": "stop"}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "status": "stop_signal_sent", "device_id": deviceID}, nil
}

func (h *Hub) register(deviceID string, conn net.Conn, clientIP string) *Session {
	now := time.Now()
	session := &Session{
		hub:          h,
		deviceID:     deviceID,
		sessionID:    randomToken(18),
		clientIP:     clientIP,
		connectedAt:  now,
		lastActivity: now,
		conn:         conn,
		pending:      map[int]chan MCPCallResult{},
		voice:        NewVoiceRecorder(),
	}
	h.mu.Lock()
	if old := h.sessions[deviceID]; old != nil {
		old.close()
	}
	h.sessions[deviceID] = session
	h.mu.Unlock()
	logger.Infof("device connected: %s from %s", deviceID, clientIP)
	go session.idleTimeoutWatcher()
	return session
}

func (h *Hub) unregister(session *Session) {
	h.mu.Lock()
	if h.sessions[session.deviceID] == session {
		delete(h.sessions, session.deviceID)
	}
	h.mu.Unlock()
	logger.Infof("device disconnected: %s", session.deviceID)
	session.close()
}

func (h *Hub) session(deviceID string) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	if deviceID == "" && len(h.sessions) == 1 {
		for _, session := range h.sessions {
			return session
		}
	}
	return h.sessions[deviceID]
}

func (h *Hub) handleText(session *Session, body []byte) {
	var message map[string]json.RawMessage
	if err := json.Unmarshal(body, &message); err != nil {
		return
	}
	var typ string
	_ = json.Unmarshal(message["type"], &typ)
	switch typ {
	case "listen":
		h.handleListenMessage(session, message)
	case "abort":
		session.stopVoiceRecording()
	case "hello":
		_ = session.writeJSON(BuildHelloResponse(session.sessionID))
		go h.bootstrapMCP(session)
	case "mcp":
		h.handleMCPMessage(session, message["payload"])
	}
}

func (h *Hub) handleMCPMessage(session *Session, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	if parsed, ok := ParseMCPResult(raw); ok {
		session.completeMCP(parsed.ID, parsed.Result)
		return
	}
	var method string
	_ = json.Unmarshal(payload["method"], &method)
	if method == "xiaoli/vision_frame" {
		h.handleVisionFrameNotification(session, payload["params"])
	}
}

func (h *Hub) handleVisionFrameNotification(session *Session, raw json.RawMessage) {
	var params struct {
		StreamID    string `json:"stream_id"`
		Seq         any    `json:"seq"`
		TimestampMS any    `json:"timestamp_ms"`
		MimeType    string `json:"mime_type"`
		Data        string `json:"data"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.Data == "" {
		return
	}
	if h.deps.Stream == nil {
		return
	}
	normalize := h.deps.NormalizeImageContentType
	if normalize == nil {
		normalize = func(contentType string, filename string) string { return contentType }
	}
	contentType := normalize(params.MimeType, "image/jpeg")
	h.deps.Stream.Publish(StreamEvent{
		Type:        "frame",
		DeviceID:    session.deviceID,
		ContentType: contentType,
		Image:       "data:" + contentType + ";base64," + params.Data,
		Size:        base64DecodedSize(params.Data),
		TS:          float64(time.Now().UnixNano()) / 1e9,
		StreamID:    params.StreamID,
		Seq:         fmt.Sprint(params.Seq),
		TimestampMS: fmt.Sprint(params.TimestampMS),
	})
}

func (h *Hub) handleListenMessage(session *Session, message map[string]json.RawMessage) {
	var state string
	_ = json.Unmarshal(message["state"], &state)
	logger.Infof("listen %s from %s", state, session.deviceID)
	switch state {
	case "start":
		var mode string
		_ = json.Unmarshal(message["mode"], &mode)
		session.startVoiceRecording(mode)
	case "stop":
		frames := session.stopVoiceRecording()
		logger.Infof("listen stop from %s: %d frames", session.deviceID, len(frames))
		if len(frames) > 0 {
			go h.processVoiceTurn(session, frames)
		}
	case "detect":
		var text string
		_ = json.Unmarshal(message["text"], &text)
		if strings.TrimSpace(text) != "" {
			session.voice.Touch()
			_ = session.writeJSON(map[string]any{"type": "stt", "text": text})
		}
	}
}

func (h *Hub) handleAudio(session *Session, payload []byte) {
	recv, voice, total := session.appendVoiceFrame(payload)
	if recv > 0 && recv%100 == 0 {
		logger.Infof("audio recv from %s: recv=%d voice=%d buffered=%d lastSize=%d", session.deviceID, recv, voice, total, len(payload))
	}
}

func (h *Hub) processVoiceTurn(session *Session, frames [][]byte) {
	if !session.tryStartVoiceProcessing() {
		return
	}
	defer session.finishVoiceProcessing()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if h.deps.ASR == nil {
		_ = h.playAssistantText(ctx, session, "我现在还没有配置语音识别。")
		return
	}
	if h.deps.BuildOggOpus == nil {
		_ = h.playAssistantText(ctx, session, "这次没有听清楚。")
		return
	}
	ogg, err := h.deps.BuildOggOpus(frames, 16000, 1, 60)
	if err != nil {
		logger.Infof("voice turn ogg build failed for %s: %v", session.deviceID, err)
		_ = h.playAssistantText(ctx, session, "这次没有听清楚。")
		return
	}
	logger.Infof("voice turn ogg built for %s: bytes=%d frames=%d", session.deviceID, len(ogg), len(frames))
	text, err := h.deps.ASR.Transcribe(ctx, ogg)
	if err != nil || strings.TrimSpace(text) == "" {
		logger.Infof("voice turn ASR failed for %s: err=%v text=%q", session.deviceID, err, text)
		_ = h.playAssistantText(ctx, session, "这次没有听清楚。")
		return
	}
	logger.Infof("voice turn ASR ok for %s: text=%q", session.deviceID, text)
	_ = session.writeJSON(map[string]any{"type": "stt", "text": text})

	answer := h.answerUserText(ctx, session, text)
	logger.Infof("voice turn LLM answer for %s: %q", session.deviceID, answer)
	if strings.TrimSpace(answer) == "" {
		answer = "我现在还没想好怎么回答。"
	}
	_ = h.playAssistantText(ctx, session, answer)
}

func (h *Hub) answerUserText(ctx context.Context, session *Session, userText string) string {
	if h.deps.Conversation == nil {
		return "我现在还没有配置语言模型。"
	}
	reply, err := h.deps.Conversation.AnswerDeviceText(ctx, session.deviceID, userText)
	if err != nil {
		logger.Infof("conversation failed for %s: %v", session.deviceID, err)
	}
	if reply == "" {
		return "我现在还没想好怎么回答。"
	}
	return reply
}

func (h *Hub) playAssistantText(ctx context.Context, session *Session, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	_ = session.writeJSON(map[string]any{"type": "llm", "emotion": "neutral"})
	_ = session.writeJSON(map[string]any{"type": "tts", "state": "start", "session_id": session.sessionID})
	_ = session.writeJSON(map[string]any{"type": "tts", "state": "sentence_start", "text": text, "session_id": session.sessionID})
	defer func() {
		_ = session.writeJSON(map[string]any{"type": "tts", "state": "stop", "session_id": session.sessionID})
	}()
	if h.deps.TTS == nil {
		return fmt.Errorf("TTS is not configured")
	}
	started := time.Now()
	synthStarted := time.Now()
	contentType, body, err := h.deps.TTS.Synthesize(ctx, text)
	if err != nil {
		logger.Infof("tts synth failed for %s: text=%q err=%v", session.deviceID, text, err)
		return err
	}
	synthElapsed := time.Since(synthStarted)
	logger.Infof("tts synth ok for %s: text=%q contentType=%s bytes=%d synthMS=%d", session.deviceID, text, contentType, len(body), synthElapsed.Milliseconds())

	if h.deps.ExtractOpusPackets == nil {
		return errors.New("opus extractor is not configured")
	}
	packets, frameDuration := h.deps.ExtractOpusPackets(body)
	if len(packets) == 0 {
		logger.Infof("tts no opus packets extracted for %s", session.deviceID)
		return errors.New("no opus packets")
	}
	if frameDuration <= 0 || frameDuration > 100*time.Millisecond {
		frameDuration = 20 * time.Millisecond
	}

	// Re-encode at 60ms to match device decoder's frame_duration.
	reencodeStarted := time.Now()
	var reencoded [][]byte
	var targetFrameDuration time.Duration
	if h.deps.ReencodeOpusFrames == nil {
		reencoded = packets
		targetFrameDuration = frameDuration
	} else {
		reencoded, targetFrameDuration, err = h.deps.ReencodeOpusFrames(packets, directDeviceAudioSampleRate, frameDuration, directDeviceAudioFrameDurationMS)
	}
	reencodeElapsed := time.Since(reencodeStarted)
	if err != nil || len(reencoded) == 0 {
		logger.Infof("tts reencode failed for %s: err=%v reencoded=%d reencodeMS=%d, falling back to raw packets", session.deviceID, err, len(reencoded), reencodeElapsed.Milliseconds())
		reencoded = packets
		targetFrameDuration = frameDuration
	}

	sourceAudioDuration := time.Duration(len(packets)) * frameDuration
	targetAudioDuration := time.Duration(len(reencoded)) * targetFrameDuration
	logger.Infof("tts stream start for %s: packets=%d reencoded=%d srcFrameDur=%s targetFrameDur=%s srcAudioDur=%s targetAudioDur=%s prebuffer=%d reencodeMS=%d", session.deviceID, len(packets), len(reencoded), frameDuration, targetFrameDuration, sourceAudioDuration, targetAudioDuration, assistantAudioPrebufferPacketNum, reencodeElapsed.Milliseconds())

	streamStarted := time.Now()
	var pacedStart time.Time
	for i, pkt := range reencoded {
		if i == assistantAudioPrebufferPacketNum {
			pacedStart = time.Now()
		} else if deadline := assistantAudioSendDeadline(pacedStart, i, targetFrameDuration); !deadline.IsZero() {
			if err := waitUntil(ctx, deadline); err != nil {
				return err
			}
		}
		if err := session.writeFrame(esp32ws.OpcodeBinary, pkt); err != nil {
			logger.Infof("tts stream send failed for %s at packet %d/%d: %v", session.deviceID, i+1, len(reencoded), err)
			return err
		}
	}
	logger.Infof("tts stream done for %s: sent=%d streamMS=%d totalMS=%d", session.deviceID, len(reencoded), time.Since(streamStarted).Milliseconds(), time.Since(started).Milliseconds())
	return nil
}

func assistantAudioSendDeadline(pacedStart time.Time, packetIndex int, frameDuration time.Duration) time.Time {
	return AssistantAudioSendDeadline(pacedStart, packetIndex, frameDuration)
}

func AssistantAudioSendDeadline(pacedStart time.Time, packetIndex int, frameDuration time.Duration) time.Time {
	if pacedStart.IsZero() || frameDuration <= 0 {
		return time.Time{}
	}
	pacedPacketIndex := packetIndex - assistantAudioPrebufferPacketNum
	if pacedPacketIndex <= 0 {
		return time.Time{}
	}
	return pacedStart.Add(time.Duration(pacedPacketIndex) * frameDuration)
}

func waitUntil(ctx context.Context, deadline time.Time) error {
	delay := time.Until(deadline)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (h *Hub) bootstrapMCP(session *Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_, _ = session.callMCP(ctx, "initialize", BuildInitializeParams(strings.TrimRight(h.cfg.PublicBaseURL, "/")+"/mcp/vision/explain", h.cfg.DeviceAuthKey))
	var allTools []map[string]any
	cursor := ""
	for i := 0; i < 8; i++ {
		result, err := session.callMCP(ctx, "tools/list", BuildToolsListParams(cursor))
		if err != nil || result.Error != "" {
			break
		}
		payload, ok := result.Result.(map[string]any)
		if !ok {
			break
		}
		for _, item := range AnySlice(payload["tools"]) {
			if tool, ok := item.(map[string]any); ok {
				allTools = append(allTools, tool)
			}
		}
		cursor = stringValue(payload["nextCursor"])
		if cursor == "" {
			break
		}
	}
	session.mu.Lock()
	session.tools = allTools
	session.mcpReady = len(allTools) > 0
	session.mu.Unlock()
	logger.Infof("device MCP ready: %s tools=%d", session.deviceID, len(allTools))
}

func (s *Session) callMCP(ctx context.Context, method string, params map[string]any) (MCPCallResult, error) {
	id, ch := s.prepareMCPCall()
	defer s.removeMCPCall(id)
	envelope := BuildMCPEnvelope(s.sessionID, id, method, params)
	if err := s.writeJSON(envelope); err != nil {
		logger.Infof("mcp call write failed for %s id=%d method=%s: %v", s.deviceID, id, method, err)
		return MCPCallResult{}, err
	}
	logger.Infof("mcp call sent to %s id=%d method=%s", s.deviceID, id, method)
	s.voice.Touch()
	select {
	case result, ok := <-ch:
		if !ok {
			logger.Infof("mcp call channel closed for %s id=%d method=%s", s.deviceID, id, method)
			return MCPCallResult{}, errors.New("device connection closed")
		}
		logger.Infof("mcp call result for %s id=%d method=%s error=%q", s.deviceID, id, method, result.Error)
		return result, nil
	case <-ctx.Done():
		logger.Infof("mcp call timeout for %s id=%d method=%s err=%v", s.deviceID, id, method, ctx.Err())
		return MCPCallResult{}, ctx.Err()
	}
}

func (s *Session) prepareMCPCall() (int, chan MCPCallResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	ch := make(chan MCPCallResult, 1)
	s.pending[id] = ch
	return id, ch
}

func (s *Session) removeMCPCall(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}

func (s *Session) completeMCP(id int, result MCPCallResult) {
	s.mu.Lock()
	ch := s.pending[id]
	s.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- result:
	default:
	}
}

func (s *Session) toolSnapshot() ([]map[string]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools := append([]map[string]any(nil), s.tools...)
	return tools, s.mcpReady
}

func (s *Session) writeJSON(value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.writeFrame(esp32ws.OpcodeText, body)
}

func (s *Session) writeFrame(opcode byte, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	conn := s.conn
	s.mu.Unlock()
	if closed {
		return errors.New("device connection closed")
	}
	return esp32ws.WriteFrame(conn, opcode, payload)
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
}

func (s *Session) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		_ = esp32ws.WriteFrame(conn, esp32ws.OpcodeClose, nil)
		_ = conn.Close()
	}
	s.voice.Close()
}

func (s *Session) startVoiceRecording(mode string) {
	if !s.voice.HasDetector() {
		if s.hub.deps.NewVoiceDetector == nil {
			s.voice.Start(mode, nil)
		} else {
			v, err := s.hub.deps.NewVoiceDetector()
			if err != nil {
				logger.Infof("vad init failed for %s: %v", s.deviceID, err)
				s.voice.Start(mode, nil)
			} else {
				s.voice.Start(mode, v)
			}
		}
	} else {
		s.voice.Start(mode, nil)
	}
	if s.voice.HasDetector() {
		logger.Infof("listen start %s mode=%s vad=silero", s.deviceID, mode)
	} else {
		logger.Infof("listen start %s mode=%s vad=FALLBACK (silero unavailable)", s.deviceID, mode)
	}
	if mode == "auto" {
		go s.autoStopWatcher()
	}
}

func (s *Session) appendVoiceFrame(payload []byte) (recv, voice, total int) {
	stats := s.voice.AppendStats(payload)
	if stats.VADRan && stats.Received%25 == 0 {
		logger.Infof("vad sample %s: prob=%.2f isVoice=%v voiceCnt=%d/%d", s.deviceID, stats.VADProb, stats.VADIsVoice, stats.Voice, stats.Received)
	}
	return stats.Received, stats.Voice, stats.Buffered
}

func (s *Session) idleTimeoutWatcher() {
	const idleTimeout = 180 * time.Second
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		lastVoice := s.voice.Snapshot().LastVoiceActivity
		idle := time.Since(lastVoice)
		if idle > idleTimeout {
			logger.Infof("idle timeout for %s: %.0fs without voice, closing connection", s.deviceID, idle.Seconds())
			s.close()
			return
		}
	}
}

func (s *Session) autoStopWatcher() {
	const silenceTimeout = 500 * time.Millisecond
	const maxDuration = 30 * time.Second
	started := time.Now()
	for {
		snapshot := s.voice.Snapshot()
		listening := snapshot.Listening && snapshot.ListenMode == "auto"
		hasVoice := snapshot.HasVoice
		lastVoice := snapshot.LastVoiceAt
		if !listening {
			return
		}
		if hasVoice && time.Since(lastVoice) > silenceTimeout {
			frames := s.stopVoiceRecording()
			logger.Infof("auto-stop from %s: %d frames (silence %.1fs)", s.deviceID, len(frames), time.Since(lastVoice).Seconds())
			if len(frames) > 0 {
				go s.hub.processVoiceTurn(s, frames)
			}
			return
		}
		if time.Since(started) > maxDuration {
			frames := s.stopVoiceRecording()
			if hasVoice {
				logger.Infof("auto-stop from %s: %d frames (max duration, has voice)", s.deviceID, len(frames))
				if len(frames) > 0 {
					go s.hub.processVoiceTurn(s, frames)
				}
			} else {
				logger.Infof("auto-stop from %s: %d frames discarded (max duration, no voice)", s.deviceID, len(frames))
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *Session) stopVoiceRecording() [][]byte {
	return s.voice.Stop()
}

func (s *Session) tryStartVoiceProcessing() bool {
	return s.voice.TryStartProcessing()
}

func (s *Session) finishVoiceProcessing() {
	s.voice.FinishProcessing()
}

func needsVision(text string) bool {
	text = strings.ToLower(text)
	for _, marker := range []string{"看", "看看", "照片", "图片", "图像", "摄像头", "画面", "拍", "坐姿", "学习状态", "我现在"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func extractMCPText(value any) string {
	switch v := value.(type) {
	case string:
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			if text := extractMCPText(parsed); text != "" {
				return text
			}
		}
		return v
	case map[string]any:
		if content, ok := v["content"].([]any); ok {
			var parts []string
			for _, item := range content {
				if m, ok := item.(map[string]any); ok {
					if text := strings.TrimSpace(stringValue(m["text"])); text != "" {
						parts = append(parts, extractMCPText(text))
					}
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
		for _, key := range []string{"response", "answer", "text", "message", "summary", "analysis", "result"} {
			if text := strings.TrimSpace(stringValue(v[key])); text != "" && text != "<nil>" {
				return text
			}
		}
	case []any:
		var parts []string
		for _, item := range v {
			if text := strings.TrimSpace(extractMCPText(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func (h *Hub) deviceAllowed(deviceID string) bool {
	return h.devicePolicy().AllowDevice(deviceID)
}

func (h *Hub) DeviceAllowed(deviceID string) bool {
	return h.deviceAllowed(deviceID)
}

func (h *Hub) deviceAuthorized(r *http.Request) bool {
	return h.devicePolicy().Authorize(r)
}

func (h *Hub) DeviceAuthorized(r *http.Request) bool {
	return h.deviceAuthorized(r)
}

func (h *Hub) devicePolicy() DevicePolicy {
	return DevicePolicy{
		AllowedDeviceIDs: h.cfg.AllowedDeviceIDs,
		AuthEnabled:      h.cfg.DeviceAuthEnabled,
		AuthKey:          h.cfg.DeviceAuthKey,
	}
}

func clientIP(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); value != "" {
		return strings.TrimSpace(strings.Split(value, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func randomToken(bytesLen int) string {
	if bytesLen <= 0 {
		bytesLen = 18
	}
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func base64DecodedSize(value string) int {
	if value == "" {
		return 0
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return len(decoded)
	}
	return len(value) * 3 / 4
}
