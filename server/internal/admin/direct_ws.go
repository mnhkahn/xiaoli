package admin

import (
	"bufio"
	"net"
	"net/http"

	esp32ws "xiaoli/server/internal/esp32/ws"
)

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
