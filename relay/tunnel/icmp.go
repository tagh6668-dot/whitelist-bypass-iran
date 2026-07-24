package tunnel

import (
	"encoding/binary"
	"net"
)

// CalculateChecksum computes the 16-bit 1's complement checksum over data.
func CalculateChecksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i < len(data)-1; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for (sum >> 16) > 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// HandleICMPPacket inspects raw L3 IP packets. If the packet is an ICMP Echo Request (ping),
// it generates a valid ICMP Echo Reply and returns (replyPacket, true).
// Otherwise it returns (nil, false).
func HandleICMPPacket(pkt []byte) ([]byte, bool) {
	if len(pkt) < 20 {
		return nil, false
	}

	version := pkt[0] >> 4
	if version == 4 {
		return handleIPv4ICMP(pkt)
	} else if version == 6 {
		return handleIPv6ICMP(pkt)
	}

	return nil, false
}

func handleIPv4ICMP(pkt []byte) ([]byte, bool) {
	ihl := int(pkt[0]&0x0f) * 4
	if len(pkt) < ihl+8 {
		return nil, false
	}

	protocol := pkt[9]
	if protocol != 1 { // Protocol 1 = ICMP
		return nil, false
	}

	icmpOffset := ihl
	icmpType := pkt[icmpOffset]
	icmpCode := pkt[icmpOffset+1]

	// Check if ICMP Echo Request (Type 8, Code 0)
	if icmpType != 8 || icmpCode != 0 {
		return nil, false
	}

	reply := make([]byte, len(pkt))
	copy(reply, pkt)

	// Swap Source IP and Destination IP
	copy(reply[12:16], pkt[16:20])
	copy(reply[16:20], pkt[12:16])

	// Clear IPv4 checksum & recalculate
	reply[10] = 0
	reply[11] = 0
	ipCksum := CalculateChecksum(reply[:ihl])
	binary.BigEndian.PutUint16(reply[10:12], ipCksum)

	// Modify ICMP Type to Echo Reply (Type 0)
	reply[icmpOffset] = 0 // Type 0 = Echo Reply

	// Clear ICMP checksum & recalculate
	reply[icmpOffset+2] = 0
	reply[icmpOffset+3] = 0
	icmpCksum := CalculateChecksum(reply[icmpOffset:])
	binary.BigEndian.PutUint16(reply[icmpOffset+2:icmpOffset+4], icmpCksum)

	return reply, true
}

func handleIPv6ICMP(pkt []byte) ([]byte, bool) {
	if len(pkt) < 40+8 {
		return nil, false
	}

	nextHeader := pkt[6]
	if nextHeader != 58 { // Next Header 58 = ICMPv6
		return nil, false
	}

	icmpOffset := 40
	icmpType := pkt[icmpOffset]
	icmpCode := pkt[icmpOffset+1]

	// ICMPv6 Echo Request (Type 128, Code 0)
	if icmpType != 128 || icmpCode != 0 {
		return nil, false
	}

	reply := make([]byte, len(pkt))
	copy(reply, pkt)

	// Swap Source IPv6 and Destination IPv6
	copy(reply[8:24], pkt[24:40])
	copy(reply[24:40], pkt[8:24])

	// Modify ICMPv6 Type to Echo Reply (Type 129)
	reply[icmpOffset] = 129

	// Clear ICMPv6 checksum
	reply[icmpOffset+2] = 0
	reply[icmpOffset+3] = 0

	// Calculate ICMPv6 pseudo-header checksum
	var pHeader []byte
	pHeader = append(pHeader, reply[8:40]...) // Src + Dst IPv6 (32 bytes)
	pLen := make([]byte, 4)
	binary.BigEndian.PutUint32(pLen, uint32(len(reply)-40))
	pHeader = append(pHeader, pLen...)
	pHeader = append(pHeader, 0, 0, 0, 58) // 3 bytes zero + NextHeader 58
	pHeader = append(pHeader, reply[icmpOffset:]...)

	icmpCksum := CalculateChecksum(pHeader)
	binary.BigEndian.PutUint16(reply[icmpOffset+2:icmpOffset+4], icmpCksum)

	return reply, true
}

// BuildICMPPortUnreachable creates an IPv4 ICMP Destination/Port Unreachable packet (Type 3, Code 3).
func BuildICMPPortUnreachable(srcIP, dstIP net.IP, origUDPPayload []byte) []byte {
	srcV4 := srcIP.To4()
	if srcV4 == nil {
		srcV4 = net.IP{127, 0, 0, 1}
	}
	dstV4 := dstIP.To4()
	if dstV4 == nil {
		dstV4 = net.IP{127, 0, 0, 1}
	}

	buf := make([]byte, 56)
	buf[0] = 0x45                            // IPv4, IHL 20
	buf[1] = 0x00                            // TOS
	binary.BigEndian.PutUint16(buf[2:4], 56) // Total Length
	binary.BigEndian.PutUint16(buf[4:6], 0)  // ID
	binary.BigEndian.PutUint16(buf[6:8], 0)  // Flags
	buf[8] = 64                              // TTL
	buf[9] = 1                               // Protocol 1 = ICMP
	copy(buf[12:16], srcV4)
	copy(buf[16:20], dstV4)

	binary.BigEndian.PutUint16(buf[10:12], CalculateChecksum(buf[:20]))

	buf[20] = 3 // Type 3 = Dest Unreachable
	buf[21] = 3 // Code 3 = Port Unreachable

	// Embedded original packet header
	buf[28] = 0x45
	copy(buf[40:44], dstV4)
	copy(buf[44:48], srcV4)
	if len(origUDPPayload) >= 8 {
		copy(buf[48:56], origUDPPayload[:8])
	}

	binary.BigEndian.PutUint16(buf[22:24], CalculateChecksum(buf[20:56]))
	return buf
}
