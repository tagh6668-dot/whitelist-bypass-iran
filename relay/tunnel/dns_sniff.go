package tunnel

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

// parseDNSResponse parses a raw DNS message and extracts (domain, IPs, error).
func parseDNSResponse(data []byte) (string, []net.IP, error) {
	var msg dnsmessage.Message
	if err := msg.Unpack(data); err != nil {
		return "", nil, err
	}
	if len(msg.Questions) == 0 {
		return "", nil, fmt.Errorf("no questions")
	}
	// Extract domain name (e.g., "google.com")
	domain := strings.ToLower(strings.TrimSuffix(msg.Questions[0].Name.String(), "."))
	var ips []net.IP
	for _, ans := range msg.Answers {
		switch body := ans.Body.(type) {
		case *dnsmessage.AResource:
			ips = append(ips, net.IP(body.A[:]))
		case *dnsmessage.AAAAResource:
			ips = append(ips, net.IP(body.AAAA[:]))
		}
	}
	return domain, ips, nil
}
