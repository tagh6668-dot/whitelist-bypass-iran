package common

import (
	"os"
	"testing"
)

func TestRouterDefaults(t *testing.T) {
	r := NewRouter(nil)

	// Default rules: bale.ai and .ir should be direct
	if res := r.Route("meet.bale.ai:443"); res != "direct" {
		t.Errorf("expected direct for bale.ai, got %q", res)
	}
	if res := r.Route("varzesh3.ir:80"); res != "direct" {
		t.Errorf("expected direct for .ir, got %q", res)
	}
	if res := r.Route("sub.domain.ir:443"); res != "direct" {
		t.Errorf("expected direct for .ir, got %q", res)
	}

	// Local and private IPs should be direct
	if res := r.Route("127.0.0.1:80"); res != "direct" {
		t.Errorf("expected direct for localhost, got %q", res)
	}
	if res := r.Route("192.168.1.1:53"); res != "direct" {
		t.Errorf("expected direct for private IP, got %q", res)
	}

	// Normal web should be proxy
	if res := r.Route("google.com:443"); res != "proxy" {
		t.Errorf("expected proxy for google.com, got %q", res)
	}
}

func TestRouterConfig(t *testing.T) {
	configJSON := `{
		"domainStrategy": "AsIs",
		"rules": [
			{
				"outboundTag": "direct",
				"domain": [
					"domain:example.com",
					"regexp:.*\\.local$"
				],
				"ip": [
					"10.0.0.0/8"
				]
			},
			{
				"outboundTag": "block",
				"domain": [
					"keyword:adserver",
					"full:malicious.com"
				]
			}
		]
	}`

	tmpFile, err := os.CreateTemp("", "routing_config_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte(configJSON)); err != nil {
		t.Fatalf("failed to write config to temp file: %v", err)
	}
	tmpFile.Close()

	r := NewRouter(nil)
	if err := r.LoadConfig(tmpFile.Name()); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	tests := []struct {
		host string
		want string
	}{
		{"example.com:443", "direct"},
		{"sub.example.com:80", "direct"},
		{"test.local:8080", "direct"},
		{"10.1.2.3:80", "direct"},
		{"badadserver.com:443", "block"},
		{"malicious.com:443", "block"},
		{"notmalicious.com:443", "proxy"},
		{"google.com:443", "proxy"},
	}

	for _, tc := range tests {
		got := r.Route(tc.host)
		if got != tc.want {
			t.Errorf("Route(%q) = %q; want %q", tc.host, got, tc.want)
		}
	}
}
