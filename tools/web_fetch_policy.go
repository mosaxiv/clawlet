package tools

import (
	"net"
	"strings"
)

func allowHostByPolicy(host string, allowedDomains, blockedDomains []string) (bool, string) {
	host = normalizeFetchHost(host)
	if host == "" {
		return false, "invalid host"
	}
	if allowedDomains == nil {
		allowedDomains = []string{"*"}
	}

	for _, raw := range blockedDomains {
		pattern := normalizeDomainPattern(raw)
		if pattern == "" {
			continue
		}
		if domainMatchesPattern(host, pattern) {
			return false, "host is blocked by policy"
		}
	}

	if len(allowedDomains) == 0 {
		return false, "no allowed domains configured"
	}
	for _, raw := range allowedDomains {
		pattern := normalizeDomainPattern(raw)
		if pattern == "" {
			continue
		}
		if pattern == "*" || domainMatchesPattern(host, pattern) {
			return true, ""
		}
	}
	return false, "host is not in allowed domains"
}

func domainMatchesPattern(host, pattern string) bool {
	host = normalizeFetchHost(host)
	pattern = normalizeDomainPattern(pattern)
	if host == "" || pattern == "" {
		return false
	}
	if host == pattern {
		return true
	}
	if net.ParseIP(host) != nil || net.ParseIP(pattern) != nil {
		return false
	}
	return strings.HasSuffix(host, "."+pattern)
}

func normalizeFetchHost(raw string) string {
	h := strings.TrimSpace(raw)
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	if parsedHost, _, err := net.SplitHostPort(h); err == nil {
		h = parsedHost
	}
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	return h
}

func normalizeDomainPattern(raw string) string {
	p := strings.ToLower(strings.TrimSpace(raw))
	if p == "" {
		return ""
	}
	if p == "*" {
		return p
	}
	p = strings.TrimPrefix(p, ".")
	return normalizeFetchHost(p)
}
