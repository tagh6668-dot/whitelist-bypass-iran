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
	DomainStrategy string `json:"domainStrategy"` // "AsIs", "IPIfNonMatch", "IPOnDemand"
	Rules          []Rule `json:"rules"`
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
	s = strings.TrimSpace(s)
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
			"2.144.0.0/12",
			"5.52.0.0/14",
			"5.160.0.0/11",
			"31.2.0.0/15",
			"31.47.0.0/16",
			"37.98.0.0/15",
			"37.114.0.0/15",
			"37.254.0.0/15",
			"46.100.0.0/14",
			"46.224.0.0/13",
			"78.38.0.0/15",
			"78.158.0.0/15",
			"80.75.0.0/17",
			"80.191.0.0/16",
			"85.185.0.0/16",
			"89.165.0.0/16",
			"91.98.0.0/15",
			"91.108.0.0/14",
			"94.182.0.0/15",
			"109.122.0.0/15",
			"151.232.0.0/14",
			"178.131.0.0/16",
			"185.0.0.0/8",
			"188.136.0.0/14",
			"188.253.0.0/16",
			"217.218.0.0/15",
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
	domainStrategy string
	compiledRules  []compiledRule
	mu             sync.RWMutex
	logFn          func(string, ...any)
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

func defaultUDP443Rule() compiledRule {
	return compiledRule{
		outboundTag: "block",
		networks:    []string{"udp"},
		portMatchers: []portMatcher{
			{startPort: 443, endPort: 443},
		},
	}
}

func (r *Router) loadDefaults() {
	r.compiledRules = []compiledRule{
		defaultUDP443Rule(),
	}
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

	var compiled []compiledRule
	hasUDP443BlockRule := false

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

		if tag == "block" && len(cRule.networks) == 1 && cRule.networks[0] == "udp" && len(cRule.portMatchers) == 1 && cRule.portMatchers[0].startPort == 443 && cRule.portMatchers[0].endPort == 443 {
			hasUDP443BlockRule = true
		}

		if len(cRule.domainMatchers) > 0 && len(cRule.ipMatchers) > 0 {
			domainRule := cRule
			domainRule.ipMatchers = nil
			compiled = append(compiled, domainRule)

			ipRule := cRule
			ipRule.domainMatchers = nil
			compiled = append(compiled, ipRule)
		} else {
			compiled = append(compiled, cRule)
		}
	}

	if !hasUDP443BlockRule {
		compiled = append(compiled, defaultUDP443Rule())
	}

	r.compiledRules = compiled
	r.logFn("router: successfully loaded %d routing rules from %s", len(compiled), configPath)
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

	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")

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
				continue
			}
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

		// 4. Check IP condition
		if len(rule.ipMatchers) > 0 {
			if !isIP {
				continue
			}
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
