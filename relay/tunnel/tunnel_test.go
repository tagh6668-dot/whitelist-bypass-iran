package tunnel

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
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

type mockDataTunnel struct {
	sentData [][]byte
	mu       sync.Mutex
	onData   func([]byte)
	onClose  func()
	fps      int
	batch    int
}

func (m *mockDataTunnel) SendData(data []byte) {
	m.mu.Lock()
	m.sentData = append(m.sentData, append([]byte(nil), data...))
	m.mu.Unlock()
}
func (m *mockDataTunnel) SetOnData(fn func([]byte)) { m.onData = fn }
func (m *mockDataTunnel) SetOnClose(fn func())      { m.onClose = fn }
func (m *mockDataTunnel) Reconfigure(fps, batch int) {
	m.mu.Lock()
	m.fps = fps
	m.batch = batch
	m.mu.Unlock()
}

func TestRelayBridgeBatching(t *testing.T) {
	mockTunnel := &mockDataTunnel{}
	rb := NewRelayBridge(mockTunnel, "joiner", 4096, func(s string, a ...any) {})
	defer rb.Close()

	// Send three small frames
	rb.send(1, MsgData, []byte("part1"))
	rb.send(1, MsgData, []byte("part2"))
	rb.send(1, MsgData, []byte("part3"))

	// Wait longer than flushInterval (4ms)
	time.Sleep(20 * time.Millisecond)

	mockTunnel.mu.Lock()
	defer mockTunnel.mu.Unlock()

	if len(mockTunnel.sentData) == 0 {
		t.Fatalf("expected data to be sent, but got none")
	}

	// The coalesced data should contain all 3 frames parsed seamlessly by DecodeFrames
	var decoded []string
	for _, chunk := range mockTunnel.sentData {
		DecodeFrames(chunk, func(cid uint32, mt byte, p []byte) {
			decoded = append(decoded, string(p))
		})
	}

	if len(decoded) != 3 {
		t.Errorf("expected 3 decoded frames, got %d: %v", len(decoded), decoded)
	} else {
		if decoded[0] != "part1" || decoded[1] != "part2" || decoded[2] != "part3" {
			t.Errorf("coalesced payload mismatch, got: %v", decoded)
		}
	}
}

func TestVP8DataTunnelAdaptivePacing(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	track, err := webrtc.NewTrackLocalStaticSample(codec, "video", "pion")
	if err != nil {
		t.Fatalf("failed to create static track: %v", err)
	}

	obf, err := NewTunnelObfuscator([]byte("pacing-secret-key"))
	if err != nil {
		t.Fatalf("failed to create obfuscator: %v", err)
	}

	vt := NewVP8DataTunnel(track, obf, func(s string, a ...any) {})
	vt.Start(24, 30)
	defer vt.Stop()

	// Initially, it should not be idle
	if vt.isIdle.Load() {
		t.Errorf("expected VP8 tunnel to start in active state, but got idle")
	}

	// Force transition to idle state for testing
	vt.isIdle.Store(true)

	// SendData should immediately scale back up to active (non-idle)
	vt.SendData([]byte("active-burst-payload"))

	// Check if we are active now
	if vt.isIdle.Load() {
		t.Errorf("expected VP8 tunnel to transition back to active state immediately on SendData")
	}
}

func TestVarintEdgeCases(t *testing.T) {
	// Test empty / nil data
	DecodeFrames(nil, func(cid uint32, mt byte, p []byte) {
		t.Errorf("did not expect callback on nil data")
	})

	// Test incomplete header data
	incomplete := []byte{0x80} // Invalid/incomplete uvarint
	DecodeFrames(incomplete, func(cid uint32, mt byte, p []byte) {
		t.Errorf("did not expect callback on incomplete header")
	})

	// Test incomplete payload length
	incompletePayload := []byte{10, 1} // Frame len is 10, but only 1 byte follows
	DecodeFrames(incompletePayload, func(cid uint32, mt byte, p []byte) {
		t.Errorf("did not expect callback on incomplete payload")
	})
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
