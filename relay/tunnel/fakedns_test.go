package tunnel

import (
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestFakeDNS(t *testing.T) {
	fake := NewFakeDNS()

	domain := "example.com"
	ip1 := fake.GetOrAllocateIP(domain)
	if ip1 == nil {
		t.Fatalf("expected non-nil fake IP")
	}

	if !fake.IsFakeIP(ip1) {
		t.Errorf("expected %s to be in fake IP pool", ip1.String())
	}

	retDomain, ok := fake.GetDomain(ip1)
	if !ok || retDomain != domain {
		t.Errorf("expected domain %s for IP %s, got %s (ok=%v)", domain, ip1.String(), retDomain, ok)
	}

	// Test DNS Query / Response synthesis
	reqMsg := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:               0x1234,
			RecursionDesired: true,
		},
		Questions: []dnsmessage.Question{
			{
				Name:  dnsmessage.MustNewName("test.org."),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
			},
		},
	}
	reqBytes, err := reqMsg.Pack()
	if err != nil {
		t.Fatalf("failed to pack test dns query: %v", err)
	}

	respBytes, qDomain, err := fake.BuildResponse(reqBytes)
	if err != nil {
		t.Fatalf("failed to build fake dns response: %v", err)
	}
	if qDomain != "test.org" {
		t.Errorf("expected qDomain test.org, got %s", qDomain)
	}

	var respMsg dnsmessage.Message
	if err := respMsg.Unpack(respBytes); err != nil {
		t.Fatalf("failed to unpack response: %v", err)
	}

	if len(respMsg.Answers) == 0 {
		t.Fatalf("expected at least 1 answer")
	}

	aRec, ok := respMsg.Answers[0].Body.(*dnsmessage.AResource)
	if !ok {
		t.Fatalf("expected AResource body")
	}

	fakeIP := net.IP(aRec.A[:])
	if !fake.IsFakeIP(fakeIP) {
		t.Errorf("allocated IP %s is not in fake IP pool", fakeIP.String())
	}

	lookupDomain, found := fake.GetDomain(fakeIP)
	if !found || lookupDomain != "test.org" {
		t.Errorf("expected test.org for IP %s, got %s", fakeIP.String(), lookupDomain)
	}
}
