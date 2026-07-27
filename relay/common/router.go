package common

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type StringOrList []string

func (s *StringOrList) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}
	var num int
	if err := json.Unmarshal(data, &num); err == nil {
		*s = []string{strconv.Itoa(num)}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*s = list
		return nil
	}
	var numList []int
	if err := json.Unmarshal(data, &numList); err == nil {
		var res []string
		for _, n := range numList {
			res = append(res, strconv.Itoa(n))
		}
		*s = res
		return nil
	}
	return nil
}

type Rule struct {
	OutboundTag string       `json:"outboundTag"` // "direct", "proxy", "block"
	Domain      StringOrList `json:"domain"`
	IP          StringOrList `json:"ip"`
	Port        StringOrList `json:"port"`
	Network     StringOrList `json:"network"`
}

type RouterConfig struct {
	DomainStrategy  string `json:"domainStrategy"`  // "AsIs", "IPIfNonMatch", "IPOnDemand"
	LocalDNSEnabled bool   `json:"localDnsEnabled"` // Enable local DNS
	FakeDNSEnabled  bool   `json:"fakeDnsEnabled"`  // Enable fake DNS
	RemoteDNS       string `json:"remoteDns"`
	DomesticDNS     string `json:"domesticDns"`
	LocalDNSPort    string `json:"localDnsPort"`
	Rules           []Rule `json:"rules"`
}

type domainMatcher struct {
	matchType string // "full", "regexp", "domain", "keyword"
	pattern   string
	regex     *regexp.Regexp
}

func parseDomainMatcher(s string) domainMatcher {
	s = strings.TrimSpace(s)
	sLower := strings.ToLower(s)
	if strings.HasPrefix(sLower, "geosite:") {
		target := sLower[8:]
		if target == "ir" {
			return domainMatcher{matchType: "domain", pattern: "ir"}
		}
		if strings.HasPrefix(target, "category-") {
			return domainMatcher{matchType: "keyword", pattern: target[9:]}
		}
		return domainMatcher{matchType: "domain", pattern: target}
	}
	if strings.HasPrefix(sLower, "regexp:") {
		pattern := strings.ToLower(s[7:])
		re, err := regexp.Compile(pattern)
		if err == nil {
			return domainMatcher{matchType: "regexp", regex: re}
		}
	}
	if strings.HasPrefix(sLower, "domain:") {
		return domainMatcher{matchType: "domain", pattern: sLower[7:]}
	}
	if strings.HasPrefix(sLower, "full:") {
		return domainMatcher{matchType: "full", pattern: sLower[5:]}
	}
	if strings.HasPrefix(sLower, "keyword:") {
		return domainMatcher{matchType: "keyword", pattern: sLower[8:]}
	}
	// Default as domain prefix/subdomain match
	return domainMatcher{matchType: "domain", pattern: sLower}
}

func (dm domainMatcher) Match(domain string) bool {
	domain = strings.ToLower(domain)
	switch dm.matchType {
	case "regexp":
		if dm.regex != nil {
			return dm.regex.MatchString(domain)
		}
	case "full":
		return domain == dm.pattern
	case "domain":
		return domain == dm.pattern || strings.HasSuffix(domain, "."+dm.pattern)
	case "keyword":
		return strings.Contains(domain, dm.pattern)
	}
	return false
}

type portMatcher struct {
	startPort int
	endPort   int
}

func parsePortMatcher(s string) (portMatcher, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "-") {
		parts := strings.SplitN(s, "-", 2)
		start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 == nil && err2 == nil {
			return portMatcher{startPort: start, endPort: end}, nil
		}
	}
	p, err := strconv.Atoi(s)
	if err == nil {
		return portMatcher{startPort: p, endPort: p}, nil
	}
	return portMatcher{}, fmt.Errorf("invalid port: %s", s)
}

func (pm portMatcher) Match(port int) bool {
	return port >= pm.startPort && port <= pm.endPort
}

type ipMatcher struct {
	cidr *net.IPNet
}

func parseIPMatcher(s string) ([]ipMatcher, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "geoip:private" {
		privateRanges := []string{
			"127.0.0.0/8",
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"fc00::/7",
			"fe80::/10",
			"::1/128",
		}
		var matchers []ipMatcher
		for _, pr := range privateRanges {
			_, cidr, _ := net.ParseCIDR(pr)
			matchers = append(matchers, ipMatcher{cidr: cidr})
		}
		return matchers, nil
	}

	if s == "geoip:ir" {
		irRanges := []string{
			"2.176.0.0/12",
			"5.112.0.0/12",
			"5.232.0.0/13",
			"2.144.0.0/14",
			"83.120.0.0/14",
			"151.232.0.0/14",
			"5.208.0.0/15",
			"5.212.0.0/15",
			"5.214.0.0/15",
			"5.216.0.0/15",
			"5.220.0.0/15",
			"37.254.0.0/15",
			"78.38.0.0/15",
			"151.238.0.0/15",
			"217.218.0.0/15",
			"5.52.0.0/16",
			"5.72.0.0/16",
			"5.73.0.0/16",
			"5.74.0.0/16",
			"5.106.0.0/16",
			"5.190.0.0/16",
			"5.202.0.0/16",
			"5.210.0.0/16",
			"5.211.0.0/16",
			"5.218.0.0/16",
			"37.129.0.0/16",
			"37.137.0.0/16",
			"46.100.0.0/16",
			"80.191.0.0/16",
			"85.185.0.0/16",
			"86.55.0.0/16",
			"87.107.0.0/16",
			"89.196.0.0/16",
			"89.199.0.0/16",
			"91.251.0.0/16",
			"93.110.0.0/16",
			"94.182.0.0/16",
			"95.38.0.0/16",
			"95.162.0.0/16",
			"178.131.0.0/16",
			"188.158.0.0/16",
			"192.15.0.0/16",
			"194.225.0.0/16",
			"204.18.0.0/16",
			"5.22.0.0/17",
			"5.75.0.0/17",
			"5.200.128.0/17",
			"5.250.0.0/17",
			"31.2.128.0/17",
			"37.63.128.0/17",
			"37.98.0.0/17",
			"37.148.0.0/17",
			"46.51.0.0/17",
			"46.143.0.0/17",
			"62.60.128.0/17",
			"77.36.128.0/17",
			"79.127.0.0/17",
			"80.210.128.0/17",
			"81.12.0.0/17",
			"85.133.128.0/17",
			"86.57.0.0/17",
			"89.165.0.0/17",
			"89.198.0.0/17",
			"89.198.128.0/17",
			"91.133.128.0/17",
			"94.183.0.0/17",
			"94.184.0.0/17",
			"94.184.128.0/17",
			"95.64.0.0/17",
			"109.162.128.0/17",
			"113.203.0.0/17",
			"158.58.0.0/17",
			"164.215.128.0/17",
			"172.80.128.0/17",
			"188.159.0.0/17",
			"188.229.0.0/17",
		}
		var matchers []ipMatcher
		for _, ir := range irRanges {
			_, cidr, _ := net.ParseCIDR(ir)
			matchers = append(matchers, ipMatcher{cidr: cidr})
		}
		return matchers, nil
	}

	_, cidr, err := net.ParseCIDR(s)
	if err != nil {
		ip := net.ParseIP(s)
		if ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			cidr = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
		} else {
			return nil, err
		}
	}
	return []ipMatcher{{cidr: cidr}}, nil
}

func (im ipMatcher) Match(ip net.IP) bool {
	if im.cidr == nil {
		return false
	}
	return im.cidr.Contains(ip)
}

type compiledRule struct {
	outboundTag    string
	domainMatchers []domainMatcher
	ipMatchers     []ipMatcher
	portMatchers   []portMatcher
	networks       []string
}

type Router struct {
	domainStrategy  string
	localDnsEnabled bool
	fakeDnsEnabled  bool
	remoteDns       string
	domesticDns     string
	localDnsPort    string
	compiledRules   []compiledRule
	mu              sync.RWMutex
	logFn           func(string, ...any)
}

func NewRouter(logFn func(string, ...any)) *Router {
	if logFn == nil {
		logFn = func(string, ...any) {}
	}
	r := &Router{
		domainStrategy: "AsIs",
		logFn:          logFn,
	}
	r.loadDefaults()
	return r
}

func (r *Router) loadDefaults() {
	r.compiledRules = []compiledRule{}
}

func (r *Router) LocalDNSEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.localDnsEnabled
}

func (r *Router) FakeDNSEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.localDnsEnabled && r.fakeDnsEnabled
}

func (r *Router) SetLocalDNS(enabled, fakeDNS bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.localDnsEnabled = enabled
	r.fakeDnsEnabled = fakeDNS
}

func (r *Router) DomainStrategy() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.domainStrategy
}

func (r *Router) LoadConfig(configPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	file, err := os.Open(configPath)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	var cfg RouterConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	if cfg.DomainStrategy != "" {
		r.domainStrategy = cfg.DomainStrategy
	}
	r.localDnsEnabled = cfg.LocalDNSEnabled
	r.fakeDnsEnabled = cfg.FakeDNSEnabled
	r.remoteDns = cfg.RemoteDNS
	r.domesticDns = cfg.DomesticDNS
	r.localDnsPort = cfg.LocalDNSPort

	var compiled []compiledRule

	for _, rule := range cfg.Rules {
		tag := strings.ToLower(rule.OutboundTag)
		if tag != "direct" && tag != "proxy" && tag != "block" {
			r.logFn("router: warning: unknown outboundTag %q, defaulting to proxy", rule.OutboundTag)
			tag = "proxy"
		}

		cRule := compiledRule{
			outboundTag: tag,
		}

		for _, d := range rule.Domain {
			cRule.domainMatchers = append(cRule.domainMatchers, parseDomainMatcher(d))
		}

		for _, ipStr := range rule.IP {
			matchers, err := parseIPMatcher(ipStr)
			if err != nil {
				r.logFn("router: error parsing IP matcher %q: %v", ipStr, err)
				continue
			}
			cRule.ipMatchers = append(cRule.ipMatchers, matchers...)
		}

		for _, pStr := range rule.Port {
			pMatcher, err := parsePortMatcher(pStr)
			if err != nil {
				r.logFn("router: error parsing port matcher %q: %v", pStr, err)
				continue
			}
			cRule.portMatchers = append(cRule.portMatchers, pMatcher)
		}

		for _, nStr := range rule.Network {
			nLower := strings.ToLower(strings.TrimSpace(nStr))
			if nLower == "tcp" || nLower == "udp" {
				cRule.networks = append(cRule.networks, nLower)
			}
		}

		compiled = append(compiled, cRule)
	}

	r.compiledRules = compiled
	r.logFn("router: successfully loaded %d routing rules from %s (domainStrategy=%s, localDNS=%v, fakeDNS=%v)",
		len(compiled), configPath, r.domainStrategy, r.localDnsEnabled, r.fakeDnsEnabled)
	return nil
}

func (r *Router) Route(hostPort string) string {
	return r.RouteWithNetwork(hostPort, "")
}

func (r *Router) RouteWithNetwork(hostPort string, network string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		portStr = ""
	}

	host = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")))

	port, _ := strconv.Atoi(portStr)
	network = strings.ToLower(strings.TrimSpace(network))

	ip := net.ParseIP(host)
	isIP := ip != nil

	for _, rule := range r.compiledRules {
		// 1. Check network condition
		if len(rule.networks) > 0 {
			netMatch := false
			for _, n := range rule.networks {
				if n == network {
					netMatch = true
					break
				}
			}
			if !netMatch {
				continue
			}
		}

		// 2. Check port condition
		if len(rule.portMatchers) > 0 {
			if port <= 0 {
				continue
			}
			portMatch := false
			for _, pm := range rule.portMatchers {
				if pm.Match(port) {
					portMatch = true
					break
				}
			}
			if !portMatch {
				continue
			}
		}

		// 3. Check domain condition
		if len(rule.domainMatchers) > 0 {
			if isIP {
				if len(rule.ipMatchers) == 0 {
					continue
				}
			} else {
				domainMatch := false
				for _, dm := range rule.domainMatchers {
					if dm.Match(host) {
						domainMatch = true
						break
					}
				}
				if !domainMatch {
					continue
				}
			}
		}

		// 4. Check IP condition
		if len(rule.ipMatchers) > 0 {
			if !isIP {
				if len(rule.domainMatchers) == 0 {
					continue
				}
			} else {
				ipMatch := false
				for _, im := range rule.ipMatchers {
					if im.Match(ip) {
						ipMatch = true
						break
					}
				}
				if !ipMatch {
					continue
				}
			}
		}

		return rule.outboundTag
	}

	if !isIP && (r.domainStrategy == "IPIfNonMatch" || r.domainStrategy == "IPOnDemand") {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		var resolver net.Resolver
		resolvedIPs, err := resolver.LookupIP(ctx, "ip", host)
		if err == nil && len(resolvedIPs) > 0 {
			for _, rule := range r.compiledRules {
				if len(rule.ipMatchers) > 0 {
					for _, im := range rule.ipMatchers {
						for _, rip := range resolvedIPs {
							if im.Match(rip) {
								return rule.outboundTag
							}
						}
					}
				}
			}
		}
	}

	return "proxy"
}
