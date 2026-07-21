package common

import (
	"os"
	"testing"
)

func TestRouterDefaults(t *testing.T) {
	r := NewRouter(nil)

	// Default direct domain rules (bale.ai, .ir) are completely removed
	if res := r.Route("meet.bale.ai:443"); res != "proxy" {
		t.Errorf("expected proxy for bale.ai, got %q", res)
	}
	if res := r.Route("varzesh3.ir:80"); res != "proxy" {
		t.Errorf("expected proxy for .ir, got %q", res)
	}

	// UDP on port 443 must be blocked by default
	if res := r.RouteWithNetwork("google.com:443", "udp"); res != "block" {
		t.Errorf("expected block for UDP 443, got %q", res)
	}
	if res := r.RouteWithNetwork("1.2.3.4:443", "udp"); res != "block" {
		t.Errorf("expected block for UDP 443, got %q", res)
	}

	// TCP on port 443 should default to proxy
	if res := r.RouteWithNetwork("google.com:443", "tcp"); res != "proxy" {
		t.Errorf("expected proxy for TCP 443, got %q", res)
	}
}

func TestRouterConfig(t *testing.T) {
	configJSON := `{
		"domainStrategy": "AsIs",
		"rules": [
			{
				"outboundTag": "direct",
				"domain": [
					"anjammidam.com",
					"regexp:.*\\.ir$"
				],
				"ip": [
					"geoip:private"
				]
			},
			{
				"outboundTag": "block",
				"network": ["udp"],
				"port": ["443"]
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
		host    string
		network string
		want    string
	}{
		// Bare domain direct matching (domain and subdomains)
		{"anjammidam.com:443", "tcp", "direct"},
		{"sub.anjammidam.com:80", "tcp", "direct"},
		{"api.sub.anjammidam.com:443", "tcp", "direct"},

		// Regex domain matching (domain and subdomains)
		{"varzesh3.ir:80", "tcp", "direct"},
		{"sub.domain.ir:443", "tcp", "direct"},
		{"domain.ir:443", "tcp", "direct"},

		// Other domains -> proxy
		{"google.com:443", "tcp", "proxy"},

		// UDP on port 443 -> block
		{"google.com:443", "udp", "block"},
		{"anjammidam.com:443", "udp", "direct"}, // Direct rule matches first before UDP 443 block
	}

	for _, tc := range tests {
		got := r.RouteWithNetwork(tc.host, tc.network)
		if got != tc.want {
			t.Errorf("RouteWithNetwork(%q, %q) = %q; want %q", tc.host, tc.network, got, tc.want)
		}
	}
}
