package tunnel

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	defaultVP8FPS       = 24
	defaultVP8Batch     = 30
	keepaliveIdlePeriod = 100 * time.Millisecond
	sendQueueDepth      = 1024
)

type VP8DataTunnel struct {
	track     *webrtc.TrackLocalStaticSample
	logFn     func(string, ...any)
	obf       *TunnelObfuscator
	stopCh    chan struct{}
	sendQueue chan []byte
	cfgChan   chan struct{}

	stopOnce sync.Once
	running  atomic.Bool

	cfgMu sync.Mutex
	fps   int
	batch int

	sentFrames atomic.Uint64
	recvFrames atomic.Uint64

	// Dynamic FPS and adaptive pacing fields
	isIdle      atomic.Bool
	scaleUpChan chan struct{}

	OnData  func([]byte)
	OnClose func()
}

func (t *VP8DataTunnel) SetOnData(fn func([]byte)) { t.OnData = fn }
func (t *VP8DataTunnel) SetOnClose(fn func())       { t.OnClose = fn }

func NewVP8DataTunnel(track *webrtc.TrackLocalStaticSample, obf *TunnelObfuscator, logFn func(string, ...any)) *VP8DataTunnel {
	return &VP8DataTunnel{
		track:       track,
		obf:         obf,
		logFn:       logFn,
		stopCh:      make(chan struct{}),
		sendQueue:   make(chan []byte, sendQueueDepth),
		cfgChan:     make(chan struct{}, 1),
		scaleUpChan: make(chan struct{}, 1),
		fps:         defaultVP8FPS,
		batch:       defaultVP8Batch,
	}
}

func (t *VP8DataTunnel) Reconfigure(fps, batch int) {
	if fps <= 0 && batch <= 0 {
		return
	}
	t.cfgMu.Lock()
	changed := false
	if fps > 0 && t.fps != fps {
		t.fps = fps
		changed = true
	}
	if batch > 0 && t.batch != batch {
		t.batch = batch
		changed = true
	}
	newFPS, newBatch := t.fps, t.batch
	t.cfgMu.Unlock()
	if !changed {
		return
	}
	t.logFn("vp8tunnel: reconfigure fps=%d batch=%d", newFPS, newBatch)
	select {
	case t.cfgChan <- struct{}{}:
	default:
	}
}

func (t *VP8DataTunnel) FPS() int {
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	return t.fps
}

func (t *VP8DataTunnel) Batch() int {
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	return t.batch
}

func (t *VP8DataTunnel) SendData(data []byte) {
	if len(data) == 0 {
		return
	}
	select {
	case t.sendQueue <- data:
		// If we are currently in idle state, scale up immediately
		if t.isIdle.Swap(false) {
			select {
			case t.scaleUpChan <- struct{}{}:
			default:
			}
		}
	case <-t.stopCh:
	}
}

func (t *VP8DataTunnel) Start(fps, batch int) {
	t.cfgMu.Lock()
	if fps > 0 {
		t.fps = fps
	}
	if batch > 0 {
		t.batch = batch
	}
	t.cfgMu.Unlock()
	if !t.running.CompareAndSwap(false, true) {
		return
	}
	go t.writerLoop()
}

func (t *VP8DataTunnel) Stop() {
	if !t.running.CompareAndSwap(true, false) {
		return
	}
	t.stopOnce.Do(func() { close(t.stopCh) })
	if t.OnClose != nil {
		t.OnClose()
	}
}

func (t *VP8DataTunnel) writerLoop() {
	var lastUserDataTime time.Time = time.Now()

	for {
		isCurrentlyIdle := t.isIdle.Load()

		var currentFPS, currentBatch int
		if isCurrentlyIdle {
			currentFPS = 1
			currentBatch = 1
		} else {
			currentFPS = t.FPS()
			currentBatch = t.Batch()
		}

		frameInterval := time.Second / time.Duration(currentFPS)
		sampleInterval := frameInterval
		if currentBatch > 1 {
			sampleInterval = frameInterval / time.Duration(currentBatch)
		}
		if sampleInterval <= 0 {
			sampleInterval = time.Millisecond
		}

		keepaliveEvery := int(keepaliveIdlePeriod / sampleInterval)
		if keepaliveEvery < 1 {
			keepaliveEvery = 1
		}

		t.logFn("vp8tunnel: writer (re)started fps=%d batch=%d sampleInterval=%s keepaliveEvery=%d idle=%t",
			currentFPS, currentBatch, sampleInterval, keepaliveEvery, isCurrentlyIdle)

		ticker := time.NewTicker(sampleInterval)
		idleTicks := 0
		reconfigure := false

		for !reconfigure {
			select {
			case <-t.stopCh:
				ticker.Stop()
				return
			case <-t.cfgChan:
				reconfigure = true
			case <-t.scaleUpChan:
				t.isIdle.Store(false)
				reconfigure = true
			case <-ticker.C:
				var sample []byte
				select {
				case data := <-t.sendQueue:
					sample = t.obf.EncodeData(data)
					idleTicks = 0
					lastUserDataTime = time.Now()
				default:
					// If not in idle state, check if we should switch to idle state (no user data for > 1.5 seconds)
					if !isCurrentlyIdle && time.Since(lastUserDataTime) > 1500*time.Millisecond {
						t.isIdle.Store(true)
						reconfigure = true
						break
					}

					idleTicks++
					if idleTicks < keepaliveEvery {
						continue
					}
					idleTicks = 0
					sample = t.obf.EncodeKeepalive()
				}

				if reconfigure {
					break
				}

				if sample == nil {
					continue
				}
				if err := t.track.WriteSample(media.Sample{Data: sample, Duration: sampleInterval}); err != nil {
					t.logFn("vp8tunnel: WriteSample error: %v", err)
					continue
				}
				n := t.sentFrames.Add(1)
				if n <= 5 || n%500 == 0 {
					t.logFn("vp8tunnel: sent frame #%d size=%d", n, len(sample))
				}
			}
		}
		ticker.Stop()
	}
}

func (t *VP8DataTunnel) HandleFrame(frame []byte) {
	res := t.obf.Decode(frame)
	if !res.HasFrame {
		return
	}
	if res.SelfEcho {
		return
	}
	if res.PeerRestart {
		t.logFn("vp8tunnel: peer restart detected, new epoch=0x%08x", res.PeerEpoch)
	}
	if res.Keepalive || len(res.Payload) == 0 {
		return
	}
	n := t.recvFrames.Add(1)
	if n <= 5 || n%500 == 0 {
		t.logFn("vp8tunnel: recv frame #%d size=%d", n, len(res.Payload))
	}
	if t.OnData != nil {
		t.OnData(res.Payload)
	}
}
