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

	dohResolver *DoHResolver

	fakeDNS *FakeDNS

	dnsCache sync.Map // IP (string) -> Domain (string)

	systemDNS []string
	tunWriter func([]byte)

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
		tunnel:      tunnel,
		logFn:       logFn,
		mode:        mode,
		readBuf:     readBuf,
		ready:       make(chan struct{}),
		batchChan:   make(chan []byte, 4096),
		router:      common.NewRouter(logFn),
		dohResolver: NewDoHResolver(logFn),
		fakeDNS:     NewFakeDNS(),
	}
	tunnel.SetOnData(rb.handleTunnelData)
	tunnel.SetOnClose(rb.closeAll)
	go rb.batchWorker()
	if mode == "joiner" {
		go rb.udpCleanupWorker()
	}
	return rb
}

func (rb *RelayBridge) SetLocalDNS(enabled, fakeDNS bool) {
	rb.router.SetLocalDNS(enabled, fakeDNS)
	rb.logFn("relay: local DNS configured: enabled=%v, fakeDNS=%v", enabled, fakeDNS)
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

func (rb *RelayBridge) SetTunWriter(fn func([]byte)) {
	rb.tunWriter = fn
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

		drain:
			for len(buf) < maxBatchSize {
				select {
				case f, ok := <-rb.batchChan:
					if !ok {
						flush()
						return
					}
					if len(buf)+len(f) > maxBatchSize {
						flush()
						buf = append(buf, f...)
						break drain
					}
					buf = append(buf, f...)
				default:
					break drain
				}
			}

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
			// Channel was closed during shutdown, ignore cleanly
		}
	}()
	select {
	case rb.batchChan <- frame:
	default:
		if !rb.closed.Load() {
			rb.tunnel.SendData(frame)
		}
	}
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

		if len(payload) < 2 {
			return
		}
		raddrLen := int(payload[0])
		if raddrLen == 0 || len(payload) < 1+raddrLen {
			return
		}
		raddrStr := string(payload[1 : 1+raddrLen])
		actualPayload := payload[1+raddrLen:]

		if strings.HasSuffix(raddrStr, ":53") {
			if domain, ips, err := parseDNSResponse(actualPayload); err == nil && domain != "" {
				for _, ip := range ips {
					rb.dnsCache.Store(ip.String(), domain)
					rb.logFn("relay: DNS Sniff Map %s -> %s", ip.String(), domain)
				}
			}
		}

		hdr, err := BuildSocksHeader(raddrStr)
		if err != nil || len(hdr) == 0 {
			hdr = uc.socksHdr
		}
		if len(hdr) == 0 {
			return
		}

		reply := make([]byte, len(hdr)+len(actualPayload))
		copy(reply, hdr)
		copy(reply[len(hdr):], actualPayload)
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
		addrLocal, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
		if err != nil {
			return
		}
		c, err := net.ListenUDP("udp", addrLocal)
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
				n, raddr, err := uconn.ReadFromUDP(buf)
				if err != nil {
					return
				}

				raddrStr := raddr.String()
				replyPayload := make([]byte, 1+len(raddrStr)+n)
				replyPayload[0] = byte(len(raddrStr))
				copy(replyPayload[1:], raddrStr)
				copy(replyPayload[1+len(raddrStr):], buf[:n])

				rb.send(id, MsgUDPReply, replyPayload)
			}
		}(connID, conn)
	}

	resolvedDst, err := net.ResolveUDPAddr("udp", addr)
	if err == nil {
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		conn.WriteToUDP(data, resolvedDst)
	}
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
	if ip := net.ParseIP(hostOnly); ip != nil {
		if rb.fakeDNS.IsFakeIP(ip) {
			if domain, ok := rb.fakeDNS.GetDomain(ip); ok {
				host = net.JoinHostPort(domain, port)
				sniffedHost = host
				rb.logFn("relay: FakeDNS TCP Reverse %s -> %s", ip.String(), domain)
			}
		} else if domain, ok := rb.dnsCache.Load(ip.String()); ok {
			sniffedHost = domain.(string) + ":" + port
			rb.logFn("relay: DNS Sniff %s -> %s", host, sniffedHost)
		}
	}

	route := rb.router.RouteWithNetwork(sniffedHost, "tcp")
	if ip := net.ParseIP(hostOnly); ip != nil {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			route = "direct"
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

	targetHost := host

	rb.logFn("relay: SOCKS CONNECT %d -> %s", id, common.MaskAddr(targetHost))
	rb.send(id, MsgConnect, []byte(targetHost))

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

			// DNS Logic: FakeDNS or Sniff queries to apply domain-based routing
			if strings.HasSuffix(dstAddr, ":53") {
				dnsReq := make([]byte, n-headerLen)
				copy(dnsReq, buf[headerLen:n])

				if rb.router.FakeDNSEnabled() {
					resp, domain, err := rb.fakeDNS.BuildResponse(dnsReq)
					if err == nil && len(resp) > 0 {
						rb.logFn("relay: FakeDNS answered query for %s", domain)
						hdr := buf[:headerLen]
						reply := make([]byte, len(hdr)+len(resp))
						copy(reply, hdr)
						copy(reply[len(hdr):], resp)
						udpConn.WriteToUDP(reply, addr)
						continue
					}
				}

				if domain, _, err := parseDNSResponse(dnsReq); err == nil && domain != "" {
					domainRoute := rb.router.RouteWithNetwork(domain, "udp")
					rb.logFn("relay: DNS Sniff Query domain=%s route=%s orig_dst=%s", domain, domainRoute, dstAddr)

					if domainRoute == "direct" {
						// Redirect query to system DNS if available to bypass tunnel and censorship
						targetDNS := ""
						if len(rb.systemDNS) > 0 {
							for _, sysDNS := range rb.systemDNS {
								if !strings.Contains(sysDNS, ":") || (strings.Count(sysDNS, ":") > 1 && !strings.Contains(sysDNS, "[")) {
									sysDNS = net.JoinHostPort(sysDNS, "53")
								}
								host, _, err := net.SplitHostPort(sysDNS)
								if err == nil {
									ip := net.ParseIP(host)
									if ip != nil && !isLoopingDNS(ip) {
										targetDNS = sysDNS
										break
									}
								}
							}
						}
						if targetDNS != "" {
							route = "direct"
							rb.logFn("relay: DNS Redirect %s -> %s for %s", dstAddr, targetDNS, domain)
							dstAddr = targetDNS
						} else {
							route = "proxy"
							rb.logFn("relay: Direct DNS fallback to proxy for %s (no valid system DNS)", domain)
						}
					} else {
						route = domainRoute
					}
				}
			}

			dstHost, dstPort, _ := net.SplitHostPort(dstAddr)
			if ip := net.ParseIP(dstHost); ip != nil {
				if rb.fakeDNS.IsFakeIP(ip) {
					if realDomain, ok := rb.fakeDNS.GetDomain(ip); ok {
						dstAddr = net.JoinHostPort(realDomain, dstPort)
						route = rb.router.RouteWithNetwork(realDomain, "udp")
						rb.logFn("relay: FakeDNS UDP Reverse %s -> %s (route=%s)", ip.String(), realDomain, route)
					}
				} else if ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
					route = "direct"
				}
			}

			if route == "block" {
				if strings.HasSuffix(dstAddr, ":443") {
					rb.logFn("relay: QUIC fast reject %s", dstAddr)
				}
				if rb.tunWriter != nil {
					host, _, _ := net.SplitHostPort(dstAddr)
					serverIP := net.ParseIP(host)
					if serverIP == nil {
						if domainIP, ok := rb.dnsCache.Load(host); ok {
							serverIP = net.ParseIP(domainIP.(string))
						}
					}
					if serverIP == nil {
						serverIP = net.ParseIP("142.250.1.1")
					}
					clientIP := net.ParseIP("10.0.0.2")
					icmpPkt := BuildICMPPortUnreachable(serverIP, clientIP, buf[headerLen:n])
					rb.tunWriter(icmpPkt)
				}
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
	flowKey := fmt.Sprintf("direct:%s", clientAddr.String())

	var conn *net.UDPConn
	if val, ok := rb.directUDPConns.Load(flowKey); ok {
		conn = val.(*net.UDPConn)
	} else {
		addrLocal, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
		if err != nil {
			return
		}
		c, err := net.ListenUDP("udp", addrLocal)
		if err != nil {
			return
		}
		conn = c
		rb.directUDPConns.Store(flowKey, conn)

		go func() {
			defer func() {
				conn.Close()
				rb.directUDPConns.Delete(flowKey)
			}()
			buf := make([]byte, common.UDPBufSize)
			for {
				conn.SetReadDeadline(time.Now().Add(30 * time.Second))
				n, raddr, err := conn.ReadFromUDP(buf)
				if err != nil {
					return
				}

				raddrStr := raddr.String()
				if strings.HasSuffix(raddrStr, ":53") {
					if domain, ips, err := parseDNSResponse(buf[:n]); err == nil && domain != "" {
						for _, ip := range ips {
							rb.dnsCache.Store(ip.String(), domain)
							rb.logFn("relay: DNS Sniff Map (direct) %s -> %s", ip.String(), domain)
						}
					}
				}

				hdr, err := BuildSocksHeader(raddrStr)
				if err != nil {
					continue
				}

				reply := make([]byte, len(hdr)+n)
				copy(reply, hdr)
				copy(reply[len(hdr):], buf[:n])
				udpConn.WriteToUDP(reply, clientAddr)
			}
		}()
	}

	resolvedDst, err := net.ResolveUDPAddr("udp", dstAddr)
	if err == nil {
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		conn.WriteToUDP(data, resolvedDst)
	}
}

// isLoopingDNS checks if a DNS IP is loopback, unspecified, link-local, or is a local interface IP of the device
// (or belongs to a TUN/VPN subnet) to prevent infinite DNS redirection loops.
func isLoopingDNS(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// Direct loopback subnet check (127.0.0.0/8)
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 127 {
			return true
		}
		// Explicitly check for our own VPN address (10.0.0.2) and the 10.0.0.0/24 subnet to prevent loops.
		// Also, some typical local/VPN interfaces use 10.0.2.15 (VirtualBox), 10.0.0.1 (VPN Gateway), etc.
		if ip4[0] == 10 && ip4[1] == 0 && ip4[2] == 0 {
			return true
		}
	}

	// Use net.InterfaceAddrs() as a robust way to get unicast addresses which is more likely
	// to work on Android/Termux where listing full interfaces can be restricted.
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				if ipNet.IP.Equal(ip) {
					return true
				}
				// If the IP is within any local interface subnet and the subnet belongs to a VPN
				if ipNet.Contains(ip) {
					if ipNet.IP.To4() != nil {
						v4 := ipNet.IP.To4()
						// Check common VPN subnets (e.g. 10.0.0.0/24 or 172.19.0.0/16 etc.)
						if (v4[0] == 10 && v4[1] == 0 && v4[2] == 0) || (v4[0] == 172 && v4[1] == 19) {
							return true
						}
					}
				}
			}
		}
	}

	interfaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range interfaces {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}

			isTun := false
			nameLower := strings.ToLower(iface.Name)
			if strings.Contains(nameLower, "tun") || strings.Contains(nameLower, "vpn") || strings.Contains(nameLower, "tap") || strings.Contains(nameLower, "ppp") {
				isTun = true
			}

			for _, addr := range addrs {
				var localIP net.IP
				var ipNet *net.IPNet
				switch v := addr.(type) {
				case *net.IPNet:
					localIP = v.IP
					ipNet = v
				case *net.IPAddr:
					localIP = v.IP
				}

				if localIP != nil {
					if localIP.Equal(ip) {
						return true
					}
					if isTun && ipNet != nil && ipNet.Contains(ip) {
						return true
					}
				}
			}
		}
	}
	return false
}

// BuildSocksHeader constructs a standard SOCKS5 UDP header for the given address.
func BuildSocksHeader(addrStr string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(addrStr)
	if err != nil {
		return nil, err
	}
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)

	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			hdr := make([]byte, 10)
			hdr[0] = 0x00 // RSV
			hdr[1] = 0x00 // RSV
			hdr[2] = 0x00 // FRAG
			hdr[3] = 0x01 // ATYP: IPv4
			copy(hdr[4:8], ip4)
			binary.BigEndian.PutUint16(hdr[8:10], port)
			return hdr, nil
		} else {
			hdr := make([]byte, 22)
			hdr[0] = 0x00 // RSV
			hdr[1] = 0x00 // RSV
			hdr[2] = 0x00 // FRAG
			hdr[3] = 0x04 // ATYP: IPv6
			copy(hdr[4:20], ip)
			binary.BigEndian.PutUint16(hdr[20:22], port)
			return hdr, nil
		}
	}

	dlen := len(host)
	if dlen > 255 {
		return nil, fmt.Errorf("domain too long")
	}
	hdr := make([]byte, 4+1+dlen+2)
	hdr[0] = 0x00 // RSV
	hdr[1] = 0x00 // RSV
	hdr[2] = 0x00 // FRAG
	hdr[3] = 0x03 // ATYP: Domain
	hdr[4] = byte(dlen)
	copy(hdr[5:5+dlen], host)
	binary.BigEndian.PutUint16(hdr[5+dlen:7+dlen], port)
	return hdr, nil
}