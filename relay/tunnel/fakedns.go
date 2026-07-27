package tunnel

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/net/dns/dnsmessage"
)

type FakeDNS struct {
	mu         sync.RWMutex
	ipToDomain map[string]string
	domainToIP map[string]net.IP
	counter    atomic.Uint32
	ipCIDR     *net.IPNet
	baseIP     uint32
	maxOffset  uint32
}

func NewFakeDNS() *FakeDNS {
	_, cidr, _ := net.ParseCIDR("198.18.0.0/15")
	base := binary.BigEndian.Uint32(cidr.IP.To4())
	return &FakeDNS{
		ipToDomain: make(map[string]string),
		domainToIP: make(map[string]net.IP),
		ipCIDR:     cidr,
		baseIP:     base,
		maxOffset:  131070, // 198.18.0.1 ~ 198.19.255.254
	}
}

func (f *FakeDNS) IsFakeIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return f.ipCIDR.Contains(ip)
}

func (f *FakeDNS) GetDomain(ip net.IP) (string, bool) {
	if ip == nil {
		return "", false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	domain, ok := f.ipToDomain[ip.String()]
	return domain, ok
}

func (f *FakeDNS) GetOrAllocateIP(domain string) net.IP {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	if domain == "" {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if ip, ok := f.domainToIP[domain]; ok {
		return ip
	}

	offset := (f.counter.Add(1) % f.maxOffset) + 1
	ipUint := f.baseIP + offset
	ipBytes := make(net.IP, 4)
	binary.BigEndian.PutUint32(ipBytes, ipUint)

	ipStr := ipBytes.String()
	f.ipToDomain[ipStr] = domain
	f.domainToIP[domain] = ipBytes
	return ipBytes
}

func (f *FakeDNS) BuildResponse(queryData []byte) ([]byte, string, error) {
	var msg dnsmessage.Message
	if err := msg.Unpack(queryData); err != nil {
		return nil, "", fmt.Errorf("unpack dns query failed: %w", err)
	}

	if len(msg.Questions) == 0 {
		return nil, "", fmt.Errorf("empty query questions")
	}

	q := msg.Questions[0]
	domain := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(q.Name.String(), ".")))

	resp := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:                 msg.ID,
			Response:           true,
			OpCode:             msg.OpCode,
			Authoritative:      false,
			Truncated:          false,
			RecursionDesired:   msg.RecursionDesired,
			RecursionAvailable: true,
			RCode:              dnsmessage.RCodeSuccess,
		},
		Questions: msg.Questions,
	}

	if q.Type == dnsmessage.TypeA || q.Type == dnsmessage.TypeALL {
		fakeIP := f.GetOrAllocateIP(domain)
		var aBytes [4]byte
		copy(aBytes[:], fakeIP.To4())

		resp.Answers = append(resp.Answers, dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{
				Name:  q.Name,
				Type:  dnsmessage.TypeA,
				Class: q.Class,
				TTL:   300,
			},
			Body: &dnsmessage.AResource{A: aBytes},
		})
	}

	packed, err := resp.Pack()
	if err != nil {
		return nil, domain, fmt.Errorf("pack dns response failed: %w", err)
	}

	return packed, domain, nil
}
