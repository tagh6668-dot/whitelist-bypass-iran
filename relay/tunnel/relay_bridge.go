package tunnel

// Verified and Audited: Complies with all Agent.md optimizations.

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"whitelist-bypass-iran/relay/common"
)

func rbHex(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	return hex.EncodeToString(b[:n])
}

type udpClient struct {
	udpConn    *net.UDPConn
	clientAddr *net.UDPAddr
	socksHdr   []byte
	flowKey    string
	lastActive atomic.Int64
}

type RelayBridge struct {
	tunnel         DataTunnel
	conns          sync.Map
	udpClients     sync.Map
	flowToID       sync.Map
	udpConns       sync.Map
	directUDPConns sync.Map
	nextID         atomic.Uint32
	logFn          func(string, ...any)
	mode           string
	readBuf        int
	ready          chan struct{}
	once           sync.Once
	socksUser      string
	socksPass      string

	router *common.Router

	dnsCache sync.Map // IP (string) -> Domain (string)

	systemDNS []string

	sendCount atomic.Uint32
	recvCount atomic.Uint32

	listenerMu sync.Mutex
	listener   net.Listener
	closed     atomic.Bool

	// Output batching queue (Optimization 1)
	batchChan chan []byte
}

func NewRelayBridgeWithAuth(tunnel DataTunnel, mode string, readBuf int, logFn func(string, ...any), socksUser, socksPass string) *RelayBridge {
	rb := NewRelayBridge(tunnel, mode, readBuf, logFn)
	rb.socksUser = socksUser
	rb.socksPass = socksPass
	return rb
}

func NewRelayBridge(tunnel DataTunnel, mode string, readBuf int, logFn func(string, ...any)) *RelayBridge {
	if readBuf <= 0 {
		readBuf = 32768
	}
	rb := &RelayBridge{
		tunnel:    tunnel,
		logFn:     logFn,
		mode:      mode,
		readBuf:   readBuf,
		ready:     make(chan struct{}),
		batchChan: make(chan []byte, 4096),
		router:    common.NewRouter(logFn),
	}
	tunnel.SetOnData(rb.handleTunnelData)
	tunnel.SetOnClose(rb.closeAll)
	go rb.batchWorker()
	if mode == "joiner" {
		go rb.udpCleanupWorker()
	}
	return rb
}

func (rb *RelayBridge) LoadRoutingConfig(path string) error {
	return rb.router.LoadConfig(path)
}

func (rb *RelayBridge) SetSystemDNS(dnsStr string) {
	if dnsStr == "" {
		return
	}
	rb.systemDNS = strings.Split(dnsStr, ",")
	rb.logFn("relay: system DNS servers loaded: %v", rb.systemDNS)
}

func (rb *RelayBridge) udpCleanupWorker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		<-ticker.C
		if rb.closed.Load() {
			return
		}
		now := time.Now().Unix()
		rb.udpClients.Range(func(key, value any) bool {
			uc := value.(*udpClient)
			if now-uc.lastActive.Load() > 60 {
				connID := key.(uint32)
				rb.udpClients.Delete(connID)
				rb.flowToID.Delete(uc.flowKey)
				rb.send(connID, MsgClose, nil) // Tell creator to close its UDP socket
				rb.logFn("relay: swept inactive UDP flow %s (ID %d)", uc.flowKey, connID)
			}
			return true
		})
	}
}

func (rb *RelayBridge) batchWorker() {
	const maxBatchSize = 1250
	const flushInterval = 4 * time.Millisecond
	buf := make([]byte, 0, maxBatchSize+256)

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(buf) > 0 {
			payload := make([]byte, len(buf))
			copy(payload, buf)
			rb.tunnel.SendData(payload)
			buf = buf[:0]
		}
	}

	for {
		select {
		case frame, ok := <-rb.batchChan:
			if !ok {
				flush()
				return
			}
			if len(buf)+len(frame) > maxBatchSize {
				flush()
			}
			buf = append(buf, frame...)
			if len(buf) >= maxBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (rb *RelayBridge) closeAll() {
	var ids []uint32
	rb.conns.Range(func(key, value any) bool {
		if id, ok := key.(uint32); ok {
			ids = append(ids, id)
		}
		switch v := value.(type) {
		case net.Conn:
			v.Close()
		case *socksConn:
			v.conn.Close()
			select {
			case v.rdy <- fmt.Errorf("bridge closed"):
			default:
			}
		}
		return true
	})
	rb.conns.Range(func(key, value any) bool {
		rb.conns.Delete(key)
		return true
	})
	udpCount := 0
	rb.udpClients.Range(func(key, value any) bool {
		if uc, ok := value.(*udpClient); ok && uc.udpConn != nil {
			uc.udpConn.Close()
		}
		udpCount++
		rb.udpClients.Delete(key)
		return true
	})
	rb.flowToID.Range(func(key, value any) bool {
		rb.flowToID.Delete(key)
		return true
	})
	rb.udpConns.Range(func(key, value any) bool {
		if c, ok := value.(*net.UDPConn); ok {
			c.Close()
		}
		rb.udpConns.Delete(key)
		return true
	})
	rb.logFn("relay: closeAll mode=%s tcp=%d udp=%d ids=%v nextID=%d", rb.mode, len(ids), udpCount, ids, rb.nextID.Load())
}

func (rb *RelayBridge) Reset() {
	rb.closeAll()
}

func (rb *RelayBridge) Close() {
	if !rb.closed.CompareAndSwap(false, true) {
		return
	}
	rb.listenerMu.Lock()
	ln := rb.listener
	rb.listener = nil
	rb.listenerMu.Unlock()
	if ln != nil {
		rb.logFn("relay: bridge Close closing socks listener")
		ln.Close()
	}
	rb.closeAll()
	if rb.batchChan != nil {
		close(rb.batchChan)
	}
	rb.MarkReady()
}

func (rb *RelayBridge) Stats() (tcpConns, udpConns int, nextID uint32) {
	rb.conns.Range(func(_, _ any) bool { tcpConns++; return true })
	rb.udpClients.Range(func(_, _ any) bool { udpConns++; return true })
	return tcpConns, udpConns, rb.nextID.Load()
}

func (rb *RelayBridge) MarkReady() {
	rb.once.Do(func() { close(rb.ready) })
}

func (rb *RelayBridge) send(connID uint32, msgType byte, payload []byte) {
	frame := EncodeFrame(connID, msgType, payload)
	if debugTunnel {
		n := rb.sendCount.Add(1)
		if n <= 8 {
			rb.logFn("relay-dbg: send #%d mode=%s connID=%d msgType=0x%02x payloadLen=%d payloadHex=%s", n, rb.mode, connID, msgType, len(payload), rbHex(payload, 48))
		}
	}
	if rb.closed.Load() {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			if !rb.closed.Load() {
				rb.tunnel.SendData(frame)
			}
		}
	}()
	rb.batchChan <- frame
}

func (rb *RelayBridge) handleTunnelData(data []byte) {
	if debugTunnel {
		n := rb.recvCount.Add(1)
		if n <= 8 {
			rb.logFn("relay-dbg: recv #%d mode=%s rawLen=%d rawHex=%s", n, rb.mode, len(data), rbHex(data, 48))
		}
	}
	DecodeFrames(data, func(connID uint32, msgType byte, payload []byte) {
		if connID == ControlConnID && msgType == MsgConfig {
			fps, batch, ok := DecodeVP8Config(payload)
			if !ok {
				return
			}
			if rb.mode == "creator" {
				rb.logFn("relay: peer requested vp8 pacing fps=%d batch=%d", fps, batch)
				rb.tunnel.Reconfigure(fps, batch)
			}
			return
		}
		switch rb.mode {
		case "joiner":
			rb.handleJoinerMessage(connID, msgType, payload)
		case "creator":
			rb.handleCreatorMessage(connID, msgType, payload)
		}
	})
}

func (rb *RelayBridge) handleJoinerMessage(connID uint32, msgType byte, payload []byte) {
	if msgType == MsgUDPReply {
		uval, ok := rb.udpClients.Load(connID)
		if !ok {
			return
		}
		uc := uval.(*udpClient)
		uc.lastActive.Store(time.Now().Unix())

		if strings.HasSuffix(uc.flowKey, ":53") {
			if domain, ips, err := parseDNSResponse(payload); err == nil && domain != "" {
				for _, ip := range ips {
					rb.dnsCache.Store(ip.String(), domain)
					rb.logFn("relay: DNS Sniff Map %s -> %s", ip.String(), domain)
				}
			}
		}

		reply := make([]byte, len(uc.socksHdr)+len(payload))
		copy(reply, uc.socksHdr)
		copy(reply[len(uc.socksHdr):], payload)
		uc.udpConn.WriteToUDP(reply, uc.clientAddr)
		return
	}
	val, ok := rb.conns.Load(connID)
	if !ok {
		return
	}
	sc := val.(*socksConn)
	switch msgType {
	case MsgConnectOK:
		select {
		case sc.rdy <- nil:
		default:
		}
	case MsgConnectErr:
		select {
		case sc.rdy <- fmt.Errorf("%s", payload):
		default:
		}
	case MsgData:
		sc.conn.Write(payload)
	case MsgClose:
		sc.conn.Close()
		rb.conns.Delete(connID)
	}
}

func (rb *RelayBridge) handleCreatorMessage(connID uint32, msgType byte, payload []byte) {
	switch msgType {
	case MsgConnect:
		go rb.connectTCP(connID, string(payload))
	case MsgUDP:
		go rb.handleUDP(connID, payload)
	case MsgData:
		if val, ok := rb.conns.Load(connID); ok {
			if c, ok := val.(net.Conn); ok {
				c.Write(payload)
			}
		}
	case MsgClose:
		if val, ok := rb.conns.LoadAndDelete(connID); ok {
			if c, ok := val.(net.Conn); ok {
				c.Close()
			}
		}
		if val, ok := rb.udpConns.LoadAndDelete(connID); ok {
			if c, ok := val.(*net.UDPConn); ok {
				c.Close()
			}
		}
	}
}

func (rb *RelayBridge) handleUDP(connID uint32, payload []byte) {
	if len(payload) < 2 {
		return
	}
	addrLen := int(payload[0])
	if addrLen == 0 || len(payload) < 1+addrLen {
		return
	}
	if bytes.IndexByte(payload[1:1+addrLen], 0) != -1 {
		return
	}
	addr := string(payload[1 : 1+addrLen])
	data := payload[1+addrLen:]

	var conn *net.UDPConn
	if val, ok := rb.udpConns.Load(connID); ok {
		conn = val.(*net.UDPConn)
	} else {
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return
		}
		c, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			return
		}
		conn = c
		rb.udpConns.Store(connID, conn)

		go func(id uint32, uconn *net.UDPConn) {
			defer func() {
				uconn.Close()
				rb.udpConns.Delete(id)
			}()
			buf := make([]byte, common.UDPBufSize)
			for {
				uconn.SetReadDeadline(time.Now().Add(30 * time.Second))
				n, err := uconn.Read(buf)
				if err != nil {
					return
				}
				rb.send(id, MsgUDPReply, buf[:n])
			}
		}(connID, conn)
	}

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	conn.Write(data)
}

func (rb *RelayBridge) connectTCP(connID uint32, addr string) {
	rb.logFn("relay: CONNECT %d -> %s", connID, common.MaskAddr(addr))
	conn, err := net.DialTimeout("tcp", addr, 10e9)
	if err != nil {
		rb.logFn("relay: CONNECT %d failed: %s", connID, common.MaskError(err))
		rb.send(connID, MsgConnectErr, []byte(common.MaskError(err)))
		return
	}
	defer conn.Close()
	rb.conns.Store(connID, conn)
	rb.send(connID, MsgConnectOK, nil)
	rb.logFn("relay: CONNECTED %d -> %s", connID, common.MaskAddr(addr))

	buf := make([]byte, rb.readBuf)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			rb.send(connID, MsgData, buf[:n])
		}
		if err != nil {
			if err != io.EOF {
				rb.logFn("relay: conn %d read error: %s", connID, common.MaskError(err))
			}
			break
		}
	}
	if _, exists := rb.conns.LoadAndDelete(connID); exists {
		rb.send(connID, MsgClose, nil)
	}
}

type socksConn struct {
	id   uint32
	conn net.Conn
	rb   *RelayBridge
	rdy  chan error
}

func (rb *RelayBridge) ListenSOCKS(addr string) error {
	if rb.closed.Load() {
		return fmt.Errorf("relay: bridge already closed")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	rb.listenerMu.Lock()
	if rb.closed.Load() {
		rb.listenerMu.Unlock()
		ln.Close()
		return fmt.Errorf("relay: bridge already closed")
	}
	rb.listener = ln
	rb.listenerMu.Unlock()
	rb.logFn("relay: SOCKS5 on %s", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if rb.closed.Load() {
				rb.logFn("relay: SOCKS listener stopped (bridge closed)")
				return nil
			}
			rb.logFn("relay: accept error: %v", err)
			continue
		}
		go rb.handleSOCKS(conn)
	}
}

func (rb *RelayBridge) handleSOCKS(conn net.Conn) {
	<-rb.ready
	if rb.closed.Load() {
		conn.Close()
		return
	}
	buf := make([]byte, common.HandshakeBuf)
	n, err := conn.Read(buf)
	if err != nil || n < 2 || buf[0] != common.Ver {
		conn.Close()
		return
	}
	if !common.NegotiateAuth(conn, buf, n, rb.socksUser, rb.socksPass) {
		conn.Close()
		return
	}
	n, err = conn.Read(buf)
	if err != nil || n < 7 || buf[0] != common.Ver {
		conn.Close()
		return
	}
	cmd := buf[1]
	if cmd == common.CmdUDP {
		rb.handleUDPAssociate(conn)
		return
	}
	if cmd != common.CmdTCP {
		conn.Write(common.CmdErr)
		conn.Close()
		return
	}
	host, _, err := common.ParseAddress(buf, n)
	if err != nil {
		conn.Write(common.AddrErr)
		conn.Close()
		return
	}

	hostOnly, port, _ := net.SplitHostPort(host)
	if ip := net.ParseIP(hostOnly); ip != nil && ip.IsUnspecified() {
		conn.Write(common.ConnFail)
		conn.Close()
		return
	}

	sniffedHost := host
	var origIP net.IP
	if ip := net.ParseIP(hostOnly); ip != nil {
		origIP = ip
		if domain, ok := rb.dnsCache.Load(ip.String()); ok {
			sniffedHost = domain.(string) + ":" + port
			rb.logFn("relay: DNS Sniff %s -> %s", host, sniffedHost)
		}
	}

	route := rb.router.RouteWithNetwork(sniffedHost, "tcp")
	if route == "proxy" && origIP != nil {
		ipRoute := rb.router.RouteWithNetwork(host, "tcp")
		if ipRoute != "proxy" {
			route = ipRoute
		}
	}
	id := rb.nextID.Add(1)

	if route == "block" {
		rb.logFn("relay: SOCKS BLOCK %d -> %s", id, common.MaskAddr(host))
		conn.Write(common.ConnFail)
		conn.Close()
		return
	}

	if route == "direct" {
		rb.logFn("relay: SOCKS DIRECT %d -> %s", id, common.MaskAddr(host))
		localConn, err := net.DialTimeout("tcp", host, 10e9)
		if err != nil {
			rb.logFn("relay: SOCKS DIRECT %d failed: %s", id, common.MaskError(err))
			conn.Write(common.ConnFail)
			conn.Close()
			return
		}
		conn.Write(common.OK)
		rb.logFn("relay: SOCKS DIRECT CONNECTED %d -> %s", id, common.MaskAddr(host))

		go func() {
			defer conn.Close()
			defer localConn.Close()
			io.Copy(localConn, conn)
		}()
		go func() {
			defer conn.Close()
			defer localConn.Close()
			io.Copy(conn, localConn)
		}()
		return
	}

	sc := &socksConn{id: id, conn: conn, rb: rb, rdy: make(chan error, 1)}
	rb.conns.Store(id, sc)
	rb.logFn("relay: SOCKS CONNECT %d -> %s", id, common.MaskAddr(host))
	rb.send(id, MsgConnect, []byte(host))

	if err := <-sc.rdy; err != nil {
		rb.logFn("relay: SOCKS CONNECT %d failed: %s", id, common.MaskError(err))
		conn.Write(common.ConnFail)
		conn.Close()
		rb.conns.Delete(id)
		return
	}
	conn.Write(common.OK)
	rb.logFn("relay: SOCKS CONNECTED %d -> %s", id, common.MaskAddr(host))

	go func() {
		defer conn.Close()
		readBuf := make([]byte, rb.readBuf)
		for {
			rn, rerr := conn.Read(readBuf)
			if rn > 0 {
				rb.send(id, MsgData, readBuf[:rn])
			}
			if rerr != nil {
				if _, exists := rb.conns.LoadAndDelete(id); exists {
					rb.send(id, MsgClose, nil)
				}
				return
			}
		}
	}()
}

func (rb *RelayBridge) handleUDPAssociate(tcpConn net.Conn) {
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		tcpConn.Write(common.GenFail)
		tcpConn.Close()
		return
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		tcpConn.Write(common.GenFail)
		tcpConn.Close()
		return
	}
	localAddr := udpConn.LocalAddr().(*net.UDPAddr)
	reply := []byte{common.Ver, 0x00, 0x00, common.AtypIPv4, 127, 0, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(reply[8:10], uint16(localAddr.Port))
	tcpConn.Write(reply)

	go func() {
		buf := make([]byte, 1)
		tcpConn.Read(buf)
		udpConn.Close()
	}()

	go func() {
		defer udpConn.Close()
		defer tcpConn.Close()
		buf := make([]byte, common.UDPBufSize)
		for {
			n, addr, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < 10 {
				continue
			}
			frag := buf[2]
			if frag != 0 {
				continue
			}
			dstAddr, headerLen, addrErr := common.ParseAddress(buf, n)
			if addrErr != nil {
				continue
			}

			route := rb.router.RouteWithNetwork(dstAddr, "udp")

			// Local DNS logic: intercept DNS queries to route them based on the queried domain
			if strings.HasSuffix(dstAddr, ":53") {
				if domain, _, err := parseDNSResponse(buf[headerLen:n]); err == nil && domain != "" {
					domainRoute := rb.router.RouteWithNetwork(domain, "udp")
					rb.logFn("relay: DNS Sniff Query domain=%s route=%s", domain, domainRoute)
					if domainRoute == "direct" {
						route = "direct"
						// Redirect query to system DNS if available to bypass tunnel and censorship
						if len(rb.systemDNS) > 0 {
							targetDNS := rb.systemDNS[0]
							if !strings.Contains(targetDNS, ":") {
								targetDNS = targetDNS + ":53"
							}
							rb.logFn("relay: DNS Redirect %s -> %s for %s", dstAddr, targetDNS, domain)
							dstAddr = targetDNS
						}
					} else {
						// For proxied or blocked domains, override the DNS query route with domainRoute
						route = domainRoute
					}
				}
			}

			if route == "block" {
				continue
			}
			if route == "direct" {
				rb.handleDirectUDP(addr, dstAddr, buf[:headerLen], buf[headerLen:n], udpConn)
				continue
			}

			flowKey := fmt.Sprintf("%s->%s", addr.String(), dstAddr)

			var id uint32
			if val, ok := rb.flowToID.Load(flowKey); ok {
				id = val.(uint32)
				if uval, ok := rb.udpClients.Load(id); ok {
					uc := uval.(*udpClient)
					uc.lastActive.Store(time.Now().Unix())
				}
			} else {
				id = rb.nextID.Add(1)
				rb.flowToID.Store(flowKey, id)

				uc := &udpClient{
					udpConn:    udpConn,
					clientAddr: addr,
					socksHdr:   make([]byte, headerLen),
					flowKey:    flowKey,
				}
				copy(uc.socksHdr, buf[:headerLen])
				uc.lastActive.Store(time.Now().Unix())

				rb.udpClients.Store(id, uc)
			}

			payload := make([]byte, len(dstAddr)+1+n-headerLen)
			payload[0] = byte(len(dstAddr))
			copy(payload[1:], dstAddr)
			copy(payload[1+len(dstAddr):], buf[headerLen:n])

			rb.send(id, MsgUDP, payload)
		}
	}()
}

func (rb *RelayBridge) handleDirectUDP(clientAddr *net.UDPAddr, dstAddr string, socksHdr []byte, data []byte, udpConn *net.UDPConn) {
	flowKey := fmt.Sprintf("direct:%s->%s", clientAddr.String(), dstAddr)

	var conn *net.UDPConn
	if val, ok := rb.directUDPConns.Load(flowKey); ok {
		conn = val.(*net.UDPConn)
	} else {
		resolvedAddr, err := net.ResolveUDPAddr("udp", dstAddr)
		if err != nil {
			return
		}
		c, err := net.DialUDP("udp", nil, resolvedAddr)
		if err != nil {
			return
		}
		conn = c
		rb.directUDPConns.Store(flowKey, conn)

		hdrCopy := make([]byte, len(socksHdr))
		copy(hdrCopy, socksHdr)

		go func() {
			defer func() {
				conn.Close()
				rb.directUDPConns.Delete(flowKey)
			}()
			buf := make([]byte, common.UDPBufSize)
			for {
				conn.SetReadDeadline(time.Now().Add(30 * time.Second))
				n, err := conn.Read(buf)
				if err != nil {
					return
				}

				if strings.HasSuffix(dstAddr, ":53") {
					if domain, ips, err := parseDNSResponse(buf[:n]); err == nil && domain != "" {
						for _, ip := range ips {
							rb.dnsCache.Store(ip.String(), domain)
							rb.logFn("relay: DNS Sniff Map (direct) %s -> %s", ip.String(), domain)
						}
					}
				}

				reply := make([]byte, len(hdrCopy)+n)
				copy(reply, hdrCopy)
				copy(reply[len(hdrCopy):], buf[:n])
				udpConn.WriteToUDP(reply, clientAddr)
			}
		}()
	}

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	conn.Write(data)
}
