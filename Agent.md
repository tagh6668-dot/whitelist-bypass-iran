Role: Senior Go & WebRTC Performance Engineer
Task: Optimize network throughput, speed, and reduce traffic overhead in the cloned `whitelist-bypass-iran` repository.

Constraints & Requirements:
1. Preserve BOTH tunnel modes: Media/Video (VP8/Audio) and DataChannel (DC) to maintain DPI circumvention capabilities. Do not deprecate either mode.
2. The core objective is to minimize traffic overhead (bring the download ratio as close to 1:1 as possible, target < 8% overhead) and maximize speed.

Please implement the following four specific optimizations in the codebase:

---

### Optimization 1: Implement Smart Packet Batching (Coalescing / Nagling)
Location: `relay/tunnel/relay_bridge.go` & SOCKS5 handler.
- Problem: Sending many small TCP/UDP packets (like 40-byte TCP ACKs) individually incurs massive per-packet tunnel and network (UDP/IP/DTLS) header overhead.
- Solution:
  - Implement an output batching queue in `RelayBridge`.
  - Introduce a tiny batching window (flush interval: 3ms to 5ms, max size: 1250 bytes).
  - Buffer outgoing SOCKS frames into a single consolidated payload before encrypting and sending it through the active tunnel.
  - Ensure the receiver can seamlessly decode these concatenated frames using the existing `DecodeFrames` loop.

---

### Optimization 2: Optimize the Obfuscation Layer (Reduce AEAD Overhead)
Location: `relay/tunnel/obfuscator.go`
- Problem: The current `ChaCha20-Poly1305` AEAD adds a fixed 40-byte overhead (24-byte Nonce + 16-byte MAC) per packet. WebRTC already provides DTLS/SRTP encryption; a secondary heavy AEAD layer is redundant and hurts bandwidth.
- Solution:
  - Create a lightweight obfuscation option. Instead of AEAD on every single frame, implement a standard `ChaCha20` stream cipher (XOR-only) obfuscator.
  - Utilize an implicit, synchronized sequence counter for Nonces to avoid transmitting the 24-byte Nonce on every packet.
  - Keep the MAC tag (Poly1305) optional or remove it for data frames, as DTLS already guarantees data integrity. This will save exactly 40 bytes per packet.

---

### Optimization 3: Dynamic FPS & Adaptive Pacing for VP8/Audio Mode
Location: `relay/tunnel/vp8tunnel.go`
- Problem: VP8 mode sends continuous video frames/keepalives at a fixed high FPS (default 24 FPS, batch 30) even during idle periods, draining cellular data.
- Solution:
  - Implement a dynamic, traffic-aware pacing mechanism (Dynamic FPS).
  - If no user data is queued for transmission for more than 1.5 seconds, dynamically scale down the pacing rate to 1 FPS (idle state).
  - The moment any SOCKS data is queued for transmission, immediately scale the FPS back up to the default high-performance rate (e.g., 24 FPS) without latency.
  - Increase the keepalive interval in DC mode to 10 seconds.

---

### Optimization 4: Compress SOCKS Frame Headers
Location: `relay/tunnel/protocol.go`
- Problem: Each SOCKS frame has a static 9-byte header (`4-byte frame length + 4-byte connection ID + 1-byte message type`).
- Solution:
  - Optimize `EncodeFrame` and `DecodeFrames` to use variable-length integers (Varint) for the `frameLen` and `connID` fields, since active connection IDs are usually small integers.
  - This reduces the header overhead from 9 bytes to 3-5 bytes per frame.

---

Verify all changes compile successfully under Go and do not break the API bindings for Android (`gomobile`) or iOS proxy applications. Provide the exact code diffs and step-by-step file modifications.
