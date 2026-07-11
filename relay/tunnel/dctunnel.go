package tunnel

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/datachannel"
)

const dcSendQueueDepth = 1024

type DCTunnel struct {
	readRaw  datachannel.ReadWriteCloser
	writeRaw datachannel.ReadWriteCloser
	logFn    func(string, ...any)
	obf      *TunnelObfuscator
	readBuf  int

	sendCh chan []byte
	stopCh chan struct{}

	onMu    sync.Mutex
	onData  func([]byte)
	onClose func()
	pending [][]byte

	firstOnce      sync.Once
	OnFirstMessage func()

	closed atomic.Bool

	recvBytes atomic.Uint64
	sendBytes atomic.Uint64
	recvMsgs  atomic.Uint64
	sendMsgs  atomic.Uint64
}

func NewDCTunnelFromRaw(readRaw, writeRaw datachannel.ReadWriteCloser, obf *TunnelObfuscator, readBuf int, logFn func(string, ...any)) *DCTunnel {
	t := &DCTunnel{
		readRaw:  readRaw,
		writeRaw: writeRaw,
		obf:      obf,
		logFn:    logFn,
		readBuf:  readBuf,
		sendCh:   make(chan []byte, dcSendQueueDepth),
		stopCh:   make(chan struct{}),
	}
	go t.readLoop()
	go t.writerLoop()
	return t
}

func (t *DCTunnel) SetOnData(fn func([]byte)) {
	t.onMu.Lock()
	t.onData = fn
	pending := t.pending
	t.pending = nil
	t.onMu.Unlock()
	if fn != nil {
		for _, frame := range pending {
			fn(frame)
		}
	}
}

func (t *DCTunnel) OnData() func([]byte) {
	t.onMu.Lock()
	defer t.onMu.Unlock()
	return t.onData
}

func (t *DCTunnel) SetOnClose(fn func())     { t.onClose = fn }
func (t *DCTunnel) Reconfigure(_ int, _ int) {}

// SendData implements Optimization 1 by sending the raw consolidated (batched) payload
// directly over the WebRTC DataChannel without individual frame disassembly.
func (t *DCTunnel) SendData(data []byte) {
	if len(data) == 0 {
		return
	}
	wire := data
	if t.obf != nil {
		wire = t.obf.EncryptPayload(data)
		if wire == nil {
			return
		}
	}
	select {
	case t.sendCh <- wire:
	case <-t.stopCh:
	}
}

func (t *DCTunnel) writerLoop() {
	// Keepalive interval set to 10 seconds (Optimization 3)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-t.stopCh:
			return
		case msg := <-t.sendCh:
			if t.closed.Load() {
				return
			}
			if _, err := t.writeRaw.Write(msg); err != nil {
				t.logFn("dctunnel: write error: %v", err)
				return
			}
			t.sendBytes.Add(uint64(len(msg)))
			t.sendMsgs.Add(1)
			ticker.Reset(10 * time.Second) // Reset the keepalive timer
		case <-ticker.C:
			// Send a keepalive packet
			if t.closed.Load() {
				return
			}
			var keepalive []byte
			if t.obf != nil {
				// Send a 1-byte keepalive [0x00] in XOR mode to avoid 0-byte packet skip,
				// or let EncryptPayload encrypt a single byte keepalive payload.
				keepalive = t.obf.EncryptPayload([]byte{0x00})
			}
			if _, err := t.writeRaw.Write(keepalive); err != nil {
				t.logFn("dctunnel: write error sending keepalive: %v", err)
				return
			}
		}
	}
}

func (t *DCTunnel) readLoop() {
	bufSize := t.readBuf
	if bufSize == 0 {
		bufSize = 32768
	}
	buf := make([]byte, bufSize)
	for {
		n, isString, err := t.readRaw.ReadDataChannel(buf)
		if err != nil {
			if err != io.EOF && !t.closed.Load() {
				t.logFn("dctunnel: read error: %v", err)
			}
			if t.onClose != nil {
				t.onClose()
			}
			return
		}
		if isString || n == 0 {
			continue
		}
		t.recvBytes.Add(uint64(n))
		t.recvMsgs.Add(1)
		t.deliver(buf[:n])
	}
}

func (t *DCTunnel) deliver(wire []byte) {
	payload := wire
	if t.obf != nil {
		pt, ok := t.obf.DecryptPayload(wire)
		if !ok {
			t.logFn("dctunnel: decrypt failed (%d bytes)", len(wire))
			return
		}
		payload = pt
	}
	if len(payload) == 0 || (len(payload) == 1 && payload[0] == 0x00) {
		return
	}
	t.firstOnce.Do(func() {
		if t.OnFirstMessage != nil {
			t.OnFirstMessage()
		}
	})

	t.onMu.Lock()
	cb := t.onData
	if cb == nil {
		t.pending = append(t.pending, payload)
		t.onMu.Unlock()
		return
	}
	t.onMu.Unlock()
	cb(payload)
}

func (t *DCTunnel) Close() {
	if !t.closed.CompareAndSwap(false, true) {
		return
	}
	close(t.stopCh)
	if t.writeRaw != nil {
		_ = t.writeRaw.Close()
	}
	if t.readRaw != nil {
		_ = t.readRaw.Close()
	}
}
