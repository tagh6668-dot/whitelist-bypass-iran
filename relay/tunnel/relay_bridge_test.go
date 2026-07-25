package tunnel

import (
	"net"
	"sync"
	"testing"
	"time"

	"whitelist-bypass-iran/relay/common"
)

// MockDataTunnel is a dummy DataTunnel for testing
type MockDataTunnel struct {
	onData  func([]byte)
	onClose func()
	sent    [][]byte
	mu      sync.Mutex
}

func (m *MockDataTunnel) SetOnData(fn func([]byte)) {
	m.onData = fn
}

func (m *MockDataTunnel) SetOnClose(fn func()) {
	m.onClose = fn
}

func (m *MockDataTunnel) SendData(data []byte) {
	m.mu.Lock()
	m.sent = append(m.sent, data)
	m.mu.Unlock()
}

func (m *MockDataTunnel) Reconfigure(fps, batch int) {}

func TestRelayBridgeUDPPersistence(t *testing.T) {
	tunnel := &MockDataTunnel{}
	rb := NewRelayBridge(tunnel, "joiner", 4096, func(format string, args ...any) {})
	defer rb.Close()

	// Expose a dummy UDP port for testing UDP SOCKS Associate
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to resolve: %v", err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer udpConn.Close()

	clientAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("failed to resolve client: %v", err)
	}

	// Craft a mock SOCKS5 UDP Associate packet
	header := []byte{0x00, 0x00, 0x00, common.AtypIPv4, 8, 8, 8, 8, 0, 53}

	flowKey := "127.0.0.1:12345->8.8.8.8:53"

	// First packet
	id := rb.nextID.Add(1)
	rb.flowToID.Store(flowKey, id)
	uc := &udpClient{
		udpConn:    udpConn,
		clientAddr: clientAddr,
		socksHdr:   header,
		flowKey:    flowKey,
	}
	uc.lastActive.Store(time.Now().Unix())
	rb.udpClients.Store(id, uc)

	// Verify it exists
	val, ok := rb.flowToID.Load(flowKey)
	if !ok || val.(uint32) != id {
		t.Fatalf("failed to store flow mapping")
	}

	// Verify that getting the flow again updates lastActive
	val2, ok2 := rb.flowToID.Load(flowKey)
	if !ok2 {
		t.Fatalf("flow not found")
	}
	retrievedID := val2.(uint32)
	if retrievedID != id {
		t.Errorf("expected ID %d, got %d", id, retrievedID)
	}

	uval, uok := rb.udpClients.Load(retrievedID)
	if !uok {
		t.Fatalf("udpClient not found")
	}
	retrievedUC := uval.(*udpClient)
	retrievedUC.lastActive.Store(time.Now().Unix() - 10) // simulate aging

	// Verify cleanup worker sweeping logic
	// Set lastActive to 100 seconds ago
	retrievedUC.lastActive.Store(time.Now().Unix() - 100)

	// Run swept logic manually to avoid waiting 30 seconds
	swept := false
	rb.udpClients.Range(func(key, value any) bool {
		uc := value.(*udpClient)
		if time.Now().Unix()-uc.lastActive.Load() > 60 {
			connID := key.(uint32)
			rb.udpClients.Delete(connID)
			rb.flowToID.Delete(uc.flowKey)
			swept = true
		}
		return true
	})

	if !swept {
		t.Fatalf("expected flow to be swept as inactive")
	}

	_, stillExists := rb.flowToID.Load(flowKey)
	if stillExists {
		t.Fatalf("swept flow should be deleted from flowToID")
	}
}

func TestIsLoopingDNS(t *testing.T) {
	loopingIPs := []string{
		"127.0.0.1",
		"127.0.0.53",
		"10.0.0.2",
		"0.0.0.0",
		"::1",
	}

	for _, ipStr := range loopingIPs {
		ip := net.ParseIP(ipStr)
		if !isLoopingDNS(ip) {
			t.Errorf("expected isLoopingDNS(%s) = true, got false", ipStr)
		}
	}

	safeSystemDNSIPs := []string{
		"1.1.1.1",
		"8.8.8.8",
		"9.9.9.9",
		"1.0.0.1",
		"192.168.1.1",
		"172.16.0.1",
	}

	for _, ipStr := range safeSystemDNSIPs {
		ip := net.ParseIP(ipStr)
		if isLoopingDNS(ip) {
			t.Errorf("expected isLoopingDNS(%s) = false, got true", ipStr)
		}
	}
}
