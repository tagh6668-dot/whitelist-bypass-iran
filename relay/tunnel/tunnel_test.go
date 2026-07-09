package tunnel

import (
	"bytes"
	"testing"
)

func TestVarintProtocol(t *testing.T) {
	connID := uint32(42)
	msgType := MsgData
	payload := []byte("hello varint!")

	frame := EncodeFrame(connID, msgType, payload)

	var decodedConnID uint32
	var decodedMsgType byte
	var decodedPayload []byte
	calls := 0

	DecodeFrames(frame, func(cid uint32, mt byte, p []byte) {
		decodedConnID = cid
		decodedMsgType = mt
		decodedPayload = p
		calls++
	})

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if decodedConnID != connID {
		t.Errorf("expected connID %d, got %d", connID, decodedConnID)
	}
	if decodedMsgType != msgType {
		t.Errorf("expected msgType %d, got %d", msgType, decodedMsgType)
	}
	if !bytes.Equal(decodedPayload, payload) {
		t.Errorf("expected payload %s, got %s", payload, decodedPayload)
	}
}

func TestVarintMultiFrames(t *testing.T) {
	f1 := EncodeFrame(1, MsgConnect, []byte("f1"))
	f2 := EncodeFrame(1000, MsgData, []byte("f2_longer"))

	buf := append(f1, f2...)

	var results []struct {
		cid uint32
		mt  byte
		p   []byte
	}

	DecodeFrames(buf, func(cid uint32, mt byte, p []byte) {
		results = append(results, struct {
			cid uint32
			mt  byte
			p   []byte
		}{cid, mt, p})
	})

	if len(results) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(results))
	}
	if results[0].cid != 1 || results[0].mt != MsgConnect || !bytes.Equal(results[0].p, []byte("f1")) {
		t.Errorf("frame 1 mismatch")
	}
	if results[1].cid != 1000 || results[1].mt != MsgData || !bytes.Equal(results[1].p, []byte("f2_longer")) {
		t.Errorf("frame 2 mismatch")
	}
}

func TestObfuscatorLightweight(t *testing.T) {
	secret := []byte("super-secret-meeting-link")
	sender, err := NewTunnelObfuscator(secret)
	if err != nil {
		t.Fatalf("failed to create sender obfuscator: %v", err)
	}

	receiver, err := NewTunnelObfuscator(secret)
	if err != nil {
		t.Fatalf("failed to create receiver obfuscator: %v", err)
	}

	// Make sure the local epochs are different to avoid self-echo detection
	for receiver.localEpoch == sender.localEpoch {
		receiver.localEpoch++
	}

	payload := []byte("original packet content")
	encoded := sender.EncodeData(payload)

	res := receiver.Decode(encoded)
	if !res.HasFrame {
		t.Fatalf("decoding failed to find frame")
	}
	if res.SelfEcho {
		t.Fatalf("frame incorrectly flagged as self echo")
	}
	if !bytes.Equal(res.Payload, payload) {
		t.Errorf("decrypted payload mismatch, expected %s, got %s", payload, res.Payload)
	}
}

// BenchmarkEncodeFrame benchmarks the Varint-compressed frame encoder.
func BenchmarkEncodeFrame(b *testing.B) {
	connID := uint32(105)
	msgType := MsgData
	payload := []byte("highly-optimized-obfuscated-payload-bytes")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EncodeFrame(connID, msgType, payload)
	}
}

// BenchmarkDecodeFrames benchmarks decoding of compressed Varint frames.
func BenchmarkDecodeFrames(b *testing.B) {
	payload := []byte("highly-optimized-obfuscated-payload-bytes")
	frame := EncodeFrame(105, MsgData, payload)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DecodeFrames(frame, func(cid uint32, mt byte, p []byte) {})
	}
}

// BenchmarkObfuscatorLightweight measures the performance of the XOR-only ChaCha20 cipher.
func BenchmarkObfuscatorLightweight(b *testing.B) {
	secret := []byte("test-secret-pacing-link-key")
	o, _ := NewTunnelObfuscator(secret)
	payload := []byte("packet-payload-of-average-size-for-socks5")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = o.EncodeData(payload)
	}
}
