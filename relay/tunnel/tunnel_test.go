package tunnel

import (
	"bytes"
	"io"
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

func TestObfuscatorPayloadXOR(t *testing.T) {
	secret := []byte("payload-xor-test-key")
	sender, err := NewTunnelObfuscator(secret)
	if err != nil {
		t.Fatalf("failed to create sender: %v", err)
	}
	receiver, err := NewTunnelObfuscator(secret)
	if err != nil {
		t.Fatalf("failed to create receiver: %v", err)
	}

	// 1. Test normal encrypt/decrypt
	payload := []byte("some application data")
	encrypted := sender.EncryptPayload(payload)
	decrypted, ok := receiver.DecryptPayload(encrypted)
	if !ok {
		t.Fatalf("decryption failed")
	}
	if !bytes.Equal(decrypted, payload) {
		t.Errorf("expected %s, got %s", payload, decrypted)
	}

	// 2. Test keepalive/empty payload
	keepaliveEnc := sender.EncryptPayload(nil)
	if len(keepaliveEnc) != 4 {
		t.Errorf("expected keepalive length of 4 (the sequence number), got %d", len(keepaliveEnc))
	}
	keepaliveDec, ok := receiver.DecryptPayload(keepaliveEnc)
	if !ok {
		t.Fatalf("decryption of keepalive failed")
	}
	if len(keepaliveDec) != 0 {
		t.Errorf("expected decrypted keepalive length 0, got %d", len(keepaliveDec))
	}

	// 3. Test robustness against skipped packet (packet loss emulation)
	_ = sender.EncryptPayload([]byte("skipped packet")) // sequence 3, skipped/lost
	packet4 := sender.EncryptPayload([]byte("packet 4")) // sequence 4

	// Receiver gets packet4. Because it contains the sequence number, receiver should decrypt it fine!
	decrypted4, ok := receiver.DecryptPayload(packet4)
	if !ok {
		t.Fatalf("decryption of out-of-order packet failed")
	}
	if !bytes.Equal(decrypted4, []byte("packet 4")) {
		t.Errorf("expected 'packet 4', got %s", decrypted4)
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

func TestObfuscatorKeyDerivation(t *testing.T) {
	testCases := []struct {
		link     string
		expected string
	}{
		{"https://meet.bale.ai/i/rbro-yljy2-z7di", "rbro-yljy2-z7di"},
		{"rbro-yljy2-z7di", "rbro-yljy2-z7di"},
		{"https://meet.bale.ai/i/rbro-yljy2-z7di?someparam=value", "rbro-yljy2-z7di"},
		{"https://meet.bale.ai/i/rbro-yljy2-z7di#somehash", "rbro-yljy2-z7di"},
		{"  https://meet.bale.ai/i/rbro-yljy2-z7di/ ", "rbro-yljy2-z7di"},
	}

	for _, tc := range testCases {
		secret := DeriveSecretFromJoinLink(tc.link)
		if string(secret) != tc.expected {
			t.Errorf("expected token %q for link %q, got %q", tc.expected, tc.link, string(secret))
		}
	}
}

// mockDataChannel implements datachannel.ReadWriteCloser for testing DCTunnel
type mockDataChannel struct {
	mu       sync.Mutex
	written  [][]byte
	readBuf  chan []byte
	isClosed bool
}

func (m *mockDataChannel) Write(b []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.written = append(m.written, append([]byte(nil), b...))
	return len(b), nil
}

func (m *mockDataChannel) Read(p []byte) (int, error) {
	n, _, err := m.ReadDataChannel(p)
	return n, err
}

func (m *mockDataChannel) ReadDataChannel(p []byte) (int, bool, error) {
	select {
	case data, ok := <-m.readBuf:
		if !ok {
			return 0, false, io.EOF
		}
		copy(p, data)
		return len(data), false, nil
	case <-time.After(500 * time.Millisecond):
		return 0, false, io.EOF
	}
}

func (m *mockDataChannel) WriteDataChannel(p []byte, isString bool) (int, error) {
	return m.Write(p)
}

func (m *mockDataChannel) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isClosed {
		m.isClosed = true
		close(m.readBuf)
	}
	return nil
}

func TestDCTunnelKeepaliveXOR(t *testing.T) {
	secret := []byte("secret")
	senderObf, err := NewTunnelObfuscator(secret)
	if err != nil {
		t.Fatalf("failed to create obfuscator: %v", err)
	}
	receiverObf, err := NewTunnelObfuscator(secret)
	if err != nil {
		t.Fatalf("failed to create obfuscator: %v", err)
	}

	// Ensure XOR mode is enabled (default)
	senderObf.useXorCipher = true
	receiverObf.useXorCipher = true

	writeCh := &mockDataChannel{readBuf: make(chan []byte, 10)}
	readCh := &mockDataChannel{readBuf: make(chan []byte, 10)}

	senderTunnel := NewDCTunnelFromRaw(readCh, writeCh, senderObf, 4096, func(string, ...any) {})
	defer senderTunnel.Close()

	receiverTunnel := NewDCTunnelFromRaw(writeCh, readCh, receiverObf, 4096, func(string, ...any) {})
	defer receiverTunnel.Close()

	// Trigger keepalive packet manually or wait. Since we don't want tests to hang, we can mock the write/read.
	// When sender sends a keepalive, it encrypts []byte{0x00}.
	keepalivePayload := []byte{0x00}
	encryptedKeepalive := senderObf.EncryptPayload(keepalivePayload)

	// Ensure keepalive is not empty in XOR mode
	if len(encryptedKeepalive) != 1 {
		t.Fatalf("expected encrypted keepalive length to be 1 in XOR mode, got %d", len(encryptedKeepalive))
	}

	// Simulate receiving keepalive on receiver
	received := make([]byte, len(encryptedKeepalive))
	copy(received, encryptedKeepalive)

	decrypted, ok := receiverObf.DecryptPayload(received)
	if !ok {
		t.Fatalf("receiver failed to decrypt keepalive")
	}

	if len(decrypted) != 1 || decrypted[0] != 0x00 {
		t.Errorf("decrypted keepalive mismatch, expected [0x00], got %v", decrypted)
	}

	// Verify that sequence counters are incremented and synchronized
	if senderObf.sendCounter.Load() != receiverObf.recvCounter.Load() {
		t.Errorf("counters out of sync: sender sendCounter=%d, receiver recvCounter=%d",
			senderObf.sendCounter.Load(), receiverObf.recvCounter.Load())
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
