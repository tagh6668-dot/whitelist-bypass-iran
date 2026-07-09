# Project Architecture Specification

This document provides a factual specification of the codebase structure, communication protocols, and data flow for the **whitelist-bypass-iran** project to assist incoming developers.

---

## 1. High-Level Overview

The primary goal of the project is to tunnel raw IP/TCP traffic over WebRTC connections utilizing trusted messaging platforms (specifically, the Iranian video calling platform **Bale Meet**). 

By simulating a standard WebRTC video call session, the project embeds encrypted and multiplexed network traffic inside video-encoded payloads (specifically fake **VP8** frames or via standard WebRTC **DataChannels**) to bypass whitelisted network restrictions.

---

## 2. Component Architecture

The codebase is organized in a modular structure:

```
+---------------------------------------------------------------------------------------------------------+
|                                        DEVELOPER PERSPECTIVE                                            |
|                                                                                                         |
|       [ SOCKS5 Local Client ]                                                                           |
|                  | (Plain SOCKS5 handshake)                                                             |
|                  v                                                                                      |
|       +---------------------+                                                                           |
|       |     RelayBridge     |  <-- Multiplexes SOCKS5 connections to/from virtual ConnIDs               |
|       +---------------------+                                                                           |
|                  | (Framed control/data payloads)                                                       |
|                  v                                                                                      |
|       +---------------------+                                                                           |
|       |  Tunnel Obfuscator  |  <-- XChaCha20-Poly1305 AEAD + Random Nonce + Local/Peer Epoch            |
|       +---------------------+                                                                           |
|                  | (Obfuscated & Encrypted packets)                                                     |
|                  v                                                                                      |
|       +---------------------+                                                                           |
|       |     DataTunnel      |  <-- Implements either:                                                   |
|       |                     |      1) VP8DataTunnel (Injected into fake VP8 video frames on a track)    |
|       |                     |      2) DCTunnel      (WebRTC DataChannel with optional packet masking)   |
|       +---------------------+                                                                           |
|                  | (Media sample / WebRTC packet stream)                                                |
|                  v                                                                                      |
|       +---------------------+                                                                           |
|       |     Pion WebRTC     |  <-- Standalone WebRTC peer without any heavy browser engine               |
|       +---------------------+                                                                           |
|                  | (WebRTC traffic whitelisted by ISP)                                                  |
|                  v                                                                                      |
|       ======================= [ Bale Meet SFU / Carrier Platform ] ==========================           |
|                                                                                                         |
+---------------------------------------------------------------------------------------------------------+
```

### A. Core Relay (Go - `relay/` directory)
The backend relay implementation contains the following sub-packages and source files:

1. **`relay/main.go`**: Program entry point. Parses command-line arguments (such as mode, SOCKS5 ports, and credentials) and initializes the client joiner.
2. **`relay/bale/`**: Implements communication protocols for the Bale messaging system based on a custom, lightweight Protobuf parser (`wire.go` / `meet.go`). It handles anonymous authentication, session signaling via WebSocket, and Livekit token retrieval.
3. **`relay/tunnel/`**:
   - **`protocol.go`**: Defines framing and control messages (`MsgConnect`, `MsgData`, `MsgClose`, `MsgUDP`, etc.) used to multiplex connections over a single transport tunnel.
   - **`obfuscator.go`**: Implements packet obfuscation and cryptography. It derives encryption keys using SHA-256 on the meeting URL path segment, encrypts payloads using **XChaCha20-Poly1305 (AEAD)**, and prepends epoch values to prevent replay attacks and detect peer restarts.
   - **`relay_bridge.go`**: Acts as a gateway between the local SOCKS5 proxy server and the obfuscated data tunnel, managing connection state and multiplexing virtual connections.
   - **`vp8tunnel.go`**: Encapsulates data payloads into fake VP8 video frame structures and delivers them dynamically based on configured frame rates (FPS) and batching sizes.
   - **`dctunnel.go`**: Implements tunneling over standard WebRTC DataChannels.
4. **`relay/pion/`**: Handles the underlying WebRTC connections utilizing the Pion Go WebRTC library. The headless client (`BaleHeadlessJoiner`) initiates PeerConnection signaling, SDP negotiation, and ICE candidate exchange.

### B. App Clients
*   **`android-app/`**: A Kotlin-based Android application that wraps the compiled Go mobile library (`mobile.aar`) to provide a local VPN (TUN interface) or proxy service.
*   **`ios-proxy-app/`**: A Swift-based iOS application configuring a local SOCKS5 proxy endpoint.
*   **`joiner-desktop-app/`**: An Electron-based desktop interface for Windows, macOS, and Linux that configures a virtual TUN network interface or a local SOCKS5 proxy.
*   **`creator-app/`**: A helper application to manage Bale accounts, automate meeting link generation, and handle WebSocket signaling configurations.
*   **`headless/`**: A command-line client optimized for server-side execution and automated deployments.

---

## 3. VP8 Packaging Protocol Specifications

To ensure whitelisted traffic characteristics, data packets mapped via the VP8 method adhere to a strict media frame structure:

1. **VP8 Header**: The initial 2 or 3 bytes contain hardcoded VP8 frame payload descriptors so that intermediary Selective Forwarding Units (SFUs) treat them as valid video frames.
2. **Local Epoch Field**: A 4-byte random field generated at connection initialization to block packet replays and detect restarts.
3. **XChaCha20 Nonce**: A 24-byte random nonce ensuring unique cipher states per frame.
4. **AEAD Ciphertext**: The encrypted network payload carrying multiplexed channel traffic, appended with a 16-byte Poly1305 authentication tag.

---

## 4. Build Scripts

Compilation is managed through predefined shell scripts situated in the root folder:

*   `build-go.sh`: Compiles Go bindings for Android (`mobile.aar`).
*   `build-ios.sh`: Compiles Swift/Go framework bindings for iOS.
*   `build-desktop-joiner.sh`: Bundles the Electron desktop client.
*   `build-headless.sh`: Builds the non-GUI CLI client binary.
*   `make-release.sh`: Bundles and packages compiled assets for release distribution.

---

## 5. Encryption Modes & Configuration

The tunnel obfuscator (`relay/tunnel/obfuscator.go`) supports two encryption modes:

| Mode | Algorithm | Overhead | Authentication | Default |
|---|---|---|---|---|
| **XOR (ChaCha20)** | ChaCha20 unauthenticated stream cipher | Minimal (4-byte sequence counter only) | None (relies on VP8 framing + epoch for integrity) | ✅ Yes |
| **AEAD** | XChaCha20-Poly1305 | 24-byte nonce + 16-byte auth tag per frame | Full authenticated encryption | No |

### Environment Variables

| Variable | Values | Description |
|---|---|---|
| `USE_AEAD` | `true` / unset | Set to `true` to force AEAD mode instead of XOR. Both creator and joiner **must** use the same mode. |
| `DEBUG_TUNNEL` | any non-empty value | Enable verbose tunnel debug logging (frame hex dumps, obfuscator encode/decode traces). |

> **⚠️ Critical**: Both sides of the tunnel (creator and joiner) **must** use the same encryption mode. A mismatch will cause silent data corruption — frames will be received but decrypted as garbage, and no error will be logged unless `DEBUG_TUNNEL` is enabled.

### Key Derivation

The encryption key is derived from the Bale meeting join link:
1. The join code is extracted from the URL path (e.g., `rbro-yljy2-z7di` from `https://meet.bale.ai/i/rbro-yljy2-z7di`)
2. `SHA-256(join_code)` produces the 32-byte key used for both XOR and AEAD modes
3. Both sides must use the same join link to derive matching keys

---

## 6. Troubleshooting

### Tunnel connects but no traffic flows

**Symptoms**: SOCKS5 connections are accepted (`SOCKS CONNECT N -> IP:port`) but never complete (`SOCKS CONNECTED` never appears). Only keepalive VP8 frames (24 bytes) are received.

**Diagnostic checklist**:

1. **Creator not running**: The most common cause. Verify the creator process is running and has joined the same Bale meeting room. Check creator logs for `TUNNEL CONNECTED`.

2. **Cipher mode mismatch**: If one side uses XOR and the other uses AEAD, frames decrypt to garbage. Enable `DEBUG_TUNNEL=1` on both sides and check:
   - `obf: enc-xor` vs `obf: dec-aead` (or vice versa) indicates a mismatch
   - Both sides should show `useXorCipher=true` (or both `false`)

3. **Key mismatch**: Both sides must derive the key from the same join link. If the creator generated a new meeting link after the joiner connected, the keys will differ.

4. **SFU not relaying video track**: Check that the creator's VP8 track is being published and the joiner's subscriber PeerConnection receives a remote video track (`sub remote track: video/VP8` in the logs).

### Received frames are all keepalives

If `[lk-video] recv vp8 frame #N 24 bytes` entries appear but no larger frames are seen, the remote peer is sending only keepalives (no data). This means the creator has the tunnel open but is not receiving any SOCKS traffic to relay back, or it is not running at all and only the SFU's echo/relay of the joiner's own keepalives is being received (which would be filtered out by the `SelfEcho` check in the obfuscator).

---

## 7. Known Issues & Fixes

### Fix: Inverted cipher toggle logic (July 2026)

**Bug**: The original environment variable `DISABLE_AEAD` used inverted logic:
```go
// OLD (buggy):
useXorCipher := os.Getenv("DISABLE_AEAD") != "false"
```
This was always `true` when the variable was unset (correct default), but semantically confusing. Setting `DISABLE_AEAD=false` would activate AEAD, which is the **opposite** of what the variable name suggests. This could cause cipher mismatches between creator and joiner if configured inconsistently.

**Fix**: Replaced with a clearer `USE_AEAD` variable:
```go
// NEW (fixed):
useXorCipher := os.Getenv("USE_AEAD") != "true"
```
Now XOR is always the default. Set `USE_AEAD=true` on **both** sides to switch to authenticated encryption.
