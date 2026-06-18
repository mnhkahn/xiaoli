package ws

import (
	"bufio"
	"net"
	"testing"
	"time"
)

func TestWriteAndReadFrameRoundTrip(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		done <- WriteFrame(server, OpcodeText, []byte("hello"))
	}()

	opcode, payload, err := ReadFrame(bufio.NewReader(client))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if opcode != OpcodeText {
		t.Fatalf("opcode = %d, want %d", opcode, OpcodeText)
	}
	if string(payload) != "hello" {
		t.Fatalf("payload = %q, want hello", payload)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WriteFrame() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WriteFrame() did not finish")
	}
}
