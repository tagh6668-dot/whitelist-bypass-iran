package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"whitelist-bypass-iran/relay/androidbind"
	"whitelist-bypass-iran/relay/common"
	"whitelist-bypass-iran/relay/pion/android"
	"whitelist-bypass-iran/relay/tunnel"
)

func main() {
	mode := flag.String("mode", "", "joiner mode (bale-headless-joiner)")
	socksPort := flag.Int("socks-port", 1080, "SOCKS5 proxy port")
	socksUser := flag.String("socks-user", "", "SOCKS5 proxy username")
	socksPass := flag.String("socks-pass", "", "SOCKS5 proxy password")
	routingConfig := flag.String("routing-config", "", "path to routing rules config JSON file (optional)")
	systemDNS := flag.String("system-dns", "", "comma-separated list of system DNS servers (optional)")
	localDNS := flag.Bool("local-dns", false, "enable local DNS")
	fakeDNS := flag.Bool("fake-dns", false, "enable fake DNS")
	remoteDNS := flag.String("remote-dns", "", "remote DNS server(s)")
	domesticDNS := flag.String("domestic-dns", "", "domestic DNS server(s)")
	localDNSPort := flag.String("local-dns-port", "10853", "local DNS port")
	flag.Parse()

	if *mode == "" {
		fmt.Fprintf(os.Stderr, "Usage: relay --mode bale-headless-joiner [--socks-port N] [--socks-user U] [--socks-pass P] [--routing-config PATH] [--system-dns DNS] [--local-dns] [--fake-dns]\n")
		os.Exit(1)
	}

	startJoinerBridge := func(tun tunnel.DataTunnel, readBuf int) {
		rb := tunnel.NewRelayBridgeWithAuth(tun, "joiner", readBuf, log.Printf, *socksUser, *socksPass)
		rb.SetTunWriter(func(pkt []byte) {
			_ = androidbind.WriteTunPacket(pkt)
		})
		if *systemDNS != "" {
			rb.SetSystemDNS(*systemDNS)
		}
		if *routingConfig != "" {
			if err := rb.LoadRoutingConfig(*routingConfig); err != nil {
				log.Printf("router: failed to load config from %s: %v", *routingConfig, err)
			}
		}
		if *localDNS || *fakeDNS {
			rb.SetLocalDNS(*localDNS, *fakeDNS)
		}
		_ = remoteDNS
		_ = domesticDNS
		_ = localDNSPort

		rb.MarkReady()
		addr := fmt.Sprintf("127.0.0.1:%d", *socksPort)
		go func() {
			if err := rb.ListenSOCKS(addr); err != nil {
				log.Printf("socks listen: %v", err)
			}
		}()
		fmt.Printf("\n  TUNNEL CONNECTED\n  socks5 -> %s\n\n", addr)
	}

	switch *mode {
	case "bale-headless-joiner":
		c := android.NewBaleHeadlessJoiner(log.Printf)
		c.OnConnected = func(tun tunnel.DataTunnel) {
			readBuf := common.VP8BufSize
			if _, ok := tun.(*tunnel.DCTunnel); ok {
				readBuf = common.DCSocksReadBuf
			}
			startJoinerBridge(tun, readBuf)
		}
		c.Run()
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n", *mode)
		os.Exit(1)
	}
}
