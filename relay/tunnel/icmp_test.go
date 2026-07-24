package tunnel

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestHandleIPv4ICMPEchoRequest(t *testing.T) {
	// Construct a valid IPv4 ICMP Echo Request packet
	pkt := make([]byte, 28)
	pkt[0] = 0x45 // IPv4, IHL=20
	binary.BigEndian.PutUint16(pkt[2:4], 28)
	pkt[8] = 64 // TTL
	pkt[9] = 1  // ICMP

	srcIP := net.ParseIP("10.0.0.2").To4()
	dstIP := net.ParseIP("8.8.8.8").To4()
	copy(pkt[12:16], srcIP)
	copy(pkt[16:20], dstIP)

	// IPv4 checksum
	binary.BigEndian.PutUint16(pkt[10:12], CalculateChecksum(pkt[:20]))

	// ICMP Header
	pkt[20] = 8 // Echo Request
	pkt[21] = 0 // Code
	// Checksum
	binary.BigEndian.PutUint16(pkt[22:24], CalculateChecksum(pkt[20:]))

	reply, ok := HandleICMPPacket(pkt)
	if !ok {
		t.Fatalf("expected HandleICMPPacket to return true for IPv4 ICMP Echo Request")
	}

	if len(reply) != len(pkt) {
		t.Fatalf("reply length mismatch: got %d, want %d", len(reply), len(pkt))
	}

	// Verify swapped IPs
	if !net.IP(reply[12:16]).Equal(dstIP) {
		t.Errorf("expected source IP to be %s, got %s", dstIP, net.IP(reply[12:16]))
	}
	if !net.IP(reply[16:20]).Equal(srcIP) {
		t.Errorf("expected dest IP to be %s, got %s", srcIP, net.IP(reply[16:20]))
	}

	// Verify ICMP Type is 0 (Echo Reply)
	if reply[20] != 0 {
		t.Errorf("expected ICMP Type 0, got %d", reply[20])
	}

	// Verify checksums
	if CalculateChecksum(reply[:20]) != 0 {
		t.Errorf("invalid IPv4 header checksum in reply")
	}
	if CalculateChecksum(reply[20:]) != 0 {
		t.Errorf("invalid ICMP header checksum in reply")
	}
}

func TestBuildICMPPortUnreachable(t *testing.T) {
	srcIP := net.ParseIP("142.250.1.1")
	dstIP := net.ParseIP("10.0.0.2")
	pkt := BuildICMPPortUnreachable(srcIP, dstIP, []byte{1, 2, 3, 4, 5, 6, 7, 8})

	if len(pkt) != 56 {
		t.Fatalf("expected packet length 56, got %d", len(pkt))
	}
	if pkt[20] != 3 || pkt[21] != 3 {
		t.Fatalf("expected ICMP Type 3 Code 3, got Type %d Code %d", pkt[20], pkt[21])
	}
	if CalculateChecksum(pkt[:20]) != 0 {
		t.Errorf("invalid IPv4 header checksum")
	}
	if CalculateChecksum(pkt[20:]) != 0 {
		t.Errorf("invalid ICMP header checksum")
	}
}
