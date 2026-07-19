package common

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
)

type Rule struct {
	OutboundTag string   `json:"outboundTag"` // "direct", "proxy", "block"
	Domain      []string `json:"domain"`
	IP          []string `json:"ip"`
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
	sLower := strings.ToLower(s)
	if strings.HasPrefix(sLower, "regexp:") {
		pattern := s[7:]
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

type ipMatcher struct {
	cidr *net.IPNet
}

func parseIPMatcher(s string) ([]ipMatcher, error) {
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
		domainStrategy: "IPIfNonMatch",
		logFn:          logFn,
	}
	r.loadDefaults()
	return r
}

func (r *Router) loadDefaults() {
	r.compiledRules = []compiledRule{
		{
			outboundTag: "direct",
			domainMatchers: []domainMatcher{
				parseDomainMatcher("domain:bale.ai"),
				parseDomainMatcher("domain:ir"),
			},
			ipMatchers: func() []ipMatcher {
				m, _ := parseIPMatcher("geoip:private")
				return m
			}(),
		},
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

		compiled = append(compiled, cRule)
	}

	r.compiledRules = compiled
	r.logFn("router: successfully loaded %d routing rules from %s", len(compiled), configPath)
	return nil
}

func (r *Router) Route(hostPort string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
	}

	// Clean host (e.g. remove brackets from ipv6)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")

	ip := net.ParseIP(host)
	isIP := ip != nil

	// Step 1: Match domain rules if it is not an IP
	if !isIP {
		for _, rule := range r.compiledRules {
			for _, dm := range rule.domainMatchers {
				if dm.Match(host) {
					return rule.outboundTag
				}
			}
		}
	}

	// Step 2: Match IP rules if it is an IP
	if isIP {
		for _, rule := range r.compiledRules {
			for _, im := range rule.ipMatchers {
				if im.Match(ip) {
					return rule.outboundTag
				}
			}
		}
	} else if r.domainStrategy == "IPIfNonMatch" || r.domainStrategy == "IPOnDemand" {
		// Resolve domain to IPs and match against IP rules
		resolvedIPs, err := net.LookupIP(host)
		if err == nil && len(resolvedIPs) > 0 {
			for _, rule := range r.compiledRules {
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

	// Default fallback
	return "proxy"
}
