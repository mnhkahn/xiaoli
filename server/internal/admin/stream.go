package admin

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	esp32ws "github.com/mnhkahn/xiaoli/server/internal/esp32/ws"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

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

type streamHub struct {
	mu         sync.Mutex
	latestByID map[string]StreamEvent
	subs       map[string]map[chan StreamEvent]struct{}
}

func newStreamHub() *streamHub {
	return &streamHub{
		latestByID: map[string]StreamEvent{},
		subs:       map[string]map[chan StreamEvent]struct{}{},
	}
}

func (h *streamHub) latest(deviceID string) *StreamEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	event, ok := h.latestByID[deviceID]
	if !ok {
		return nil
	}
	return &event
}

func (h *streamHub) publish(event StreamEvent) StreamEvent {
	if event.Type == "" {
		event.Type = "frame"
	}
	if event.TS == 0 {
		event.TS = float64(time.Now().UnixNano()) / 1e9
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.latestByID[event.DeviceID] = event
	for ch := range h.subs[event.DeviceID] {
		select {
		case ch <- event:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- event:
			default:
			}
		}
	}
	return event
}

func (h *streamHub) subscribe(deviceID string) (chan StreamEvent, func()) {
	ch := make(chan StreamEvent, 1)
	h.mu.Lock()
	if h.subs[deviceID] == nil {
		h.subs[deviceID] = map[chan StreamEvent]struct{}{}
	}
	h.subs[deviceID][ch] = struct{}{}
	if latest, ok := h.latestByID[deviceID]; ok {
		ch <- latest
	}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.subs[deviceID] != nil {
			delete(h.subs[deviceID], ch)
			if len(h.subs[deviceID]) == 0 {
				delete(h.subs, deviceID)
			}
		}
		close(ch)
	}
}

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func (s *AdminServer) handleStreamWS(w http.ResponseWriter, r *http.Request, user map[string]any) {
	deviceID := r.URL.Query().Get("device_id")
	if deviceID == "" {
		http.Error(w, "device_id is required", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket unsupported", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	acceptBytes := sha1.Sum([]byte(key + websocketGUID))
	accept := base64.StdEncoding.EncodeToString(acceptBytes[:])
	_, _ = bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	_, _ = bufrw.WriteString("Upgrade: websocket\r\n")
	_, _ = bufrw.WriteString("Connection: Upgrade\r\n")
	_, _ = bufrw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n\r\n")
	if err := bufrw.Flush(); err != nil {
		return
	}

	ch, unsubscribe := s.stream.subscribe(deviceID)
	defer unsubscribe()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event, ok := <-ch:
			if !ok || writeWebSocketJSON(conn, event) != nil {
				return
			}
		case <-ticker.C:
			if writeWebSocketJSON(conn, map[string]any{"type": "heartbeat", "ts": s.cfg.now().Unix()}) != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func writeWebSocketJSON(conn net.Conn, value any) error {
	return esp32ws.WriteJSON(conn, value)
}

const (
	wsOpcodeText   = esp32ws.OpcodeText
	wsOpcodeBinary = esp32ws.OpcodeBinary
	wsOpcodeClose  = esp32ws.OpcodeClose
	wsOpcodePing   = esp32ws.OpcodePing
	wsOpcodePong   = esp32ws.OpcodePong
)

type websocketPeer struct {
	conn   net.Conn
	reader *bufio.Reader
}

func acceptWebSocket(w http.ResponseWriter, r *http.Request) (*websocketPeer, error) {
	peer, err := esp32ws.Accept(w, r)
	if err != nil {
		return nil, err
	}
	return &websocketPeer{conn: peer.Conn, reader: peer.Reader}, nil
}

func readWebSocketFrame(reader *bufio.Reader) (byte, []byte, error) {
	return esp32ws.ReadFrame(reader)
}

func writeWebSocketFrame(conn net.Conn, opcode byte, payload []byte) error {
	return esp32ws.WriteFrame(conn, opcode, payload)
}
