package tunnel

import (
	"bytes"
	"testing"
)

func TestVarintProtocolEncoding(t *testing.T) {
	connID := uint32(105)
	msgType := MsgData
	payload := []byte("hello varint encoding test")

	encoded := EncodeFrame(connID, msgType, payload)

	// Verify header length is compressed (less than old 9 bytes + payload)
	if len(encoded) >= 9+len(payload) {
		t.Errorf("Expected compressed frame length < %d, got %d", 9+len(payload), len(encoded))
	}

	var called bool
	DecodeFrames(encoded, func(id uint32, mType byte, p []byte) {
		called = true
		if id != connID {
			t.Errorf("Expected connID %d, got %d", connID, id)
		}
		if mType != msgType {
			t.Errorf("Expected msgType %d, got %d", msgType, mType)
		}
		if !bytes.Equal(p, payload) {
			t.Errorf("Expected payload %s, got %s", payload, p)
		}
	})

	if !called {
		t.Fatal("DecodeFrames callback was not called")
	}
}

func TestVarintMultipleFramesDecoding(t *testing.T) {
	f1 := EncodeFrame(1, MsgConnect, []byte("example.com:443"))
	f2 := EncodeFrame(2, MsgData, []byte("data payload 1"))
	f3 := EncodeFrame(2, MsgData, []byte("data payload 2"))

	concatenated := append(f1, append(f2, f3...)...)

	var count int
	DecodeFrames(concatenated, func(connID uint32, msgType byte, payload []byte) {
		count++
		switch count {
		case 1:
			if connID != 1 || msgType != MsgConnect || string(payload) != "example.com:443" {
				t.Fatalf("Frame 1 mismatch: connID=%d msgType=%d payload=%s", connID, msgType, payload)
			}
		case 2:
			if connID != 2 || msgType != MsgData || string(payload) != "data payload 1" {
				t.Fatalf("Frame 2 mismatch: connID=%d msgType=%d payload=%s", connID, msgType, payload)
			}
		case 3:
			if connID != 2 || msgType != MsgData || string(payload) != "data payload 2" {
				t.Fatalf("Frame 3 mismatch: connID=%d msgType=%d payload=%s", connID, msgType, payload)
			}
		}
	})

	if count != 3 {
		t.Fatalf("Expected 3 decoded frames, got %d", count)
	}
}
