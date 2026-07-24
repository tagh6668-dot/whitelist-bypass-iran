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

func TestRouterGeoIPIR(t *testing.T) {
	configJSON := `{
		"rules": [
			{
				"outboundTag": "direct",
				"ip": ["geoip:ir"]
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
		// Irancell IP (2.144.1.2) -> direct
		{"2.144.1.2:443", "direct"},
		// Respina IP (5.160.1.2) -> proxy (no longer in Respina range since Respina's /16 is not in prefixlen<=17 list, or wait, is 5.160.1.2 in our list? Let's check: 5.160.0.0/16 is not in <=17 list, but we have other Respina ranges like 5.190.0.0/16. Let's test 5.190.1.2:443 -> direct)
		{"5.190.1.2:443", "direct"},
		// TCI IP (217.218.1.2) -> direct
		{"217.218.1.2:443", "direct"},
		// Hetzner Germany IP (46.224.1.2) -> proxy
		{"46.224.1.2:443", "proxy"},
		// Hetzner Germany IP (91.98.1.2) -> proxy
		{"91.98.1.2:443", "proxy"},
		// Telegram IP (91.108.4.5) -> proxy
		{"91.108.4.5:443", "proxy"},
		// Google IP -> proxy
		{"8.8.8.8:53", "proxy"},
	}

	for _, tc := range tests {
		got := r.Route(tc.host)
		if got != tc.want {
			t.Errorf("Route(%q) = %q; want %q", tc.host, got, tc.want)
		}
	}
}

func TestRouterCaseInsensitivityAndANDLogic(t *testing.T) {
	configJSON := `{
		"rules": [
			{
				"outboundTag": "direct",
				"domain": ["YouTube.Com"],
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
		t.Fatalf("failed to write config: %v", err)
	}
	tmpFile.Close()

	r := NewRouter(nil)
	if err := r.LoadConfig(tmpFile.Name()); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Case insensitive test on domain + port AND logic
	if got := r.RouteWithNetwork("WWW.YOUTUBE.COM:443", "tcp"); got != "direct" {
		t.Errorf("expected direct for WWW.YOUTUBE.COM:443, got %q", got)
	}

	// Same domain but different port (80 vs 443) -> must NOT match rule (AND logic enforces port 443)
	if got := r.RouteWithNetwork("WWW.YOUTUBE.COM:80", "tcp"); got != "proxy" {
		t.Errorf("expected proxy for WWW.YOUTUBE.COM:80 (AND condition port 443 fail), got %q", got)
	}
}

