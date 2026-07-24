//go:build android

package androidbind

import (
	"fmt"
	"log"
	"os"
	"sync"
	"syscall"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	"whitelist-bypass-iran/relay/tunnel"
)

var (
	tunReady  sync.WaitGroup
	tunOrigFd int = -1
	tunDone   chan struct{}
)

func StartTun2Socks(fd, mtu, socksPort int, socksUser, socksPass string) error {
	dupFd, err := syscall.Dup(fd)
	if err != nil {
		return fmt.Errorf("dup tun fd: %w", err)
	}
	tunOrigFd = fd
	tunDone = make(chan struct{})

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	var engineFd int
	if err == nil {
		engineFd = fds[0]
		interceptorFd := fds[1]

		go func() {
			buf := make([]byte, mtu+100)
			for {
				select {
				case <-tunDone:
					return
				default:
				}
				n, err := syscall.Read(fd, buf)
				if err != nil || n <= 0 {
					return
				}
				if reply, isICMP := tunnel.HandleICMPPacket(buf[:n]); isICMP {
					syscall.Write(fd, reply)
					continue
				}
				syscall.Write(interceptorFd, buf[:n])
			}
		}()

		go func() {
			buf := make([]byte, mtu+100)
			for {
				select {
				case <-tunDone:
					return
				default:
				}
				n, err := syscall.Read(interceptorFd, buf)
				if err != nil || n <= 0 {
					return
				}
				syscall.Write(fd, buf[:n])
			}
		}()
	} else {
		log.Printf("tun2socks: socketpair creation failed (%v), falling back to raw dup", err)
		engineFd = dupFd
	}

	var proxy string
	if socksUser != "" {
		proxy = fmt.Sprintf("socks5://%s:%s@127.0.0.1:%d", socksUser, socksPass, socksPort)
	} else {
		proxy = fmt.Sprintf("socks5://127.0.0.1:%d", socksPort)
	}
	log.Printf("tun2socks: starting fd=%d (dup=%d, engine=%d) mtu=%d proxy=%s", fd, dupFd, engineFd, mtu, proxy)
	os.Setenv("TUN2SOCKS_LOG_LEVEL", "info")
	key := &engine.Key{
		Proxy:  proxy,
		Device: fmt.Sprintf("fd://%d", engineFd),
		MTU:    mtu,
	}
	tunReady.Add(1)
	engine.Insert(key)
	engine.Start()
	tunReady.Done()
	log.Printf("tun2socks: running")
	return nil
}

func StopTun2Socks() {
	tunReady.Wait()
	if tunDone != nil {
		close(tunDone)
	}
	engine.Stop()
	if tunOrigFd >= 0 {
		syscall.Close(tunOrigFd)
		tunOrigFd = -1
	}
	log.Printf("tun2socks: stopped")
}
