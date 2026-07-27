package tunnel

import "encoding/binary"

// SOCKS Frame Message Type Definitions
const (
	MsgConnect    byte = 0x01
	MsgConnectOK  byte = 0x02
	MsgConnectErr byte = 0x03
	MsgData       byte = 0x04
	MsgClose      byte = 0x05
	MsgUDP        byte = 0x06
	MsgUDPReply   byte = 0x07
	MsgConfig     byte = 0x08
)

const ControlConnID uint32 = 0

type DataTunnel interface {
	SendData(data []byte)
	SetOnData(fn func([]byte))
	SetOnClose(fn func())
	Reconfigure(fps, batch int)
}

func EncodeVP8Config(fps, batch int) []byte {
	if fps < 1 {
		fps = 1
	}
	if batch < 1 {
		batch = 1
	}
	if fps > 0xFFFF {
		fps = 0xFFFF
	}
	if batch > 0xFFFF {
		batch = 0xFFFF
	}
	var payload [4]byte
	binary.BigEndian.PutUint16(payload[0:2], uint16(fps))
	binary.BigEndian.PutUint16(payload[2:4], uint16(batch))
	return EncodeFrame(ControlConnID, MsgConfig, payload[:])
}

func DecodeVP8Config(payload []byte) (fps, batch int, ok bool) {
	if len(payload) < 4 {
		return 0, 0, false
	}
	fps = int(binary.BigEndian.Uint16(payload[0:2]))
	batch = int(binary.BigEndian.Uint16(payload[2:4]))
	return fps, batch, true
}

// EncodeFrame implements Optimization 4 (Varint Frame Header Compression).
// It compresses connID and frame length using variable-length integers (Varint),
// reducing header overhead from 9 bytes to 3-5 bytes.
func EncodeFrame(connID uint32, msgType byte, payload []byte) []byte {
	// Encode connID as Varint
	var connBuf [binary.MaxVarintLen64]byte
	connLen := binary.PutUvarint(connBuf[:], uint64(connID))

	// The remaining length after frameLen is: connLen + 1 (msgType) + len(payload)
	remLen := connLen + 1 + len(payload)

	var lenBuf [binary.MaxVarintLen64]byte
	lenLen := binary.PutUvarint(lenBuf[:], uint64(remLen))

	buf := make([]byte, lenLen+remLen)
	copy(buf[0:], lenBuf[:lenLen])
	copy(buf[lenLen:], connBuf[:connLen])
	buf[lenLen+connLen] = msgType
	copy(buf[lenLen+connLen+1:], payload)
	return buf
}

// DecodeFrames decodes concatenated frames compressed via Varint headers.
func DecodeFrames(data []byte, cb func(connID uint32, msgType byte, payload []byte)) {
	for {
		if len(data) == 0 {
			return
		}
		remLen, n := binary.Uvarint(data)
		if n <= 0 {
			return // Not enough bytes to decode frameLen or invalid uvarint
		}
		if remLen > uint64(len(data)-n) {
			return // Incomplete frame
		}
		frameData := data[n : n+int(remLen)]

		// Decode connID
		connID64, m := binary.Uvarint(frameData)
		if m <= 0 {
			return
		}
		if m+1 > len(frameData) {
			return
		}
		msgType := frameData[m]
		payload := frameData[m+1:]

		cb(uint32(connID64), msgType, payload)

		data = data[n+int(remLen):]
	}
}
