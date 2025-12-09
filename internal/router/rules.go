package router

import (
	"net"
	"path/filepath"
	"strings"

	"github.com/waylen888/splitdial/internal/config"
)

// Router handles traffic routing decisions based on rules.
type Router struct {
	rules []config.RouteRule
}

// NewRouter creates a new router with the given rules.
func NewRouter(rules []config.RouteRule) *Router {
	return &Router{rules: rules}
}

// UpdateRules updates the routing rules.
func (r *Router) UpdateRules(rules []config.RouteRule) {
	r.rules = rules
}

// RouteResult represents the result of a routing decision.
type RouteResult struct {
	Interface string // "cable" or "wifi"
	RuleID    string // ID of the matched rule
	RuleName  string // Name of the matched rule
}

// Route determines which interface to use for the given destination.
func (r *Router) Route(host string, port int) RouteResult {
	for _, rule := range r.rules {
		if !rule.Enabled {
			continue
		}

		if r.matchRule(rule, host, port) {
			return RouteResult{
				Interface: rule.Interface,
				RuleID:    rule.ID,
				RuleName:  rule.Name,
			}
		}
	}

	// Default to cable if no rule matches
	return RouteResult{
		Interface: "cable",
		RuleID:    "default",
		RuleName:  "Default",
	}
}

// matchRule checks if a rule matches the given destination.
func (r *Router) matchRule(rule config.RouteRule, host string, port int) bool {
	match := rule.Match

	// If no match conditions, this is likely a catch-all rule
	if len(match.Domains) == 0 && len(match.IPs) == 0 && len(match.Ports) == 0 {
		return true
	}

	// Check domains
	if len(match.Domains) > 0 {
		matched := false
		for _, pattern := range match.Domains {
			if matchDomain(pattern, host) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check IPs
	if len(match.IPs) > 0 {
		ip := net.ParseIP(host)
		if ip == nil {
			// Host is a domain name, not an IP
			// If this rule ONLY has IP conditions (no domain conditions),
			// then it cannot match a domain name
			if len(match.Domains) == 0 {
				return false
			}
			// Otherwise, skip IP matching (domain already matched above)
		} else {
			matched := false
			for _, cidr := range match.IPs {
				_, ipNet, err := net.ParseCIDR(cidr)
				if err != nil {
					// Try as single IP
					if net.ParseIP(cidr).Equal(ip) {
						matched = true
						break
					}
					continue
				}
				if ipNet.Contains(ip) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}

	// Check ports
	if len(match.Ports) > 0 {
		matched := false
		for _, p := range match.Ports {
			if p == port {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// matchDomain checks if a domain matches a pattern with wildcard support.
func matchDomain(pattern, domain string) bool {
	pattern = strings.ToLower(pattern)
	domain = strings.ToLower(domain)

	// Exact match
	if pattern == domain {
		return true
	}

	// Wildcard matching: *.example.com matches sub.example.com and example.com
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		if strings.HasSuffix(domain, suffix) {
			return true
		}
		// Also match the root domain (*.example.com matches example.com)
		if domain == pattern[2:] {
			return true
		}
	}

	// Glob pattern matching for more complex patterns
	matched, _ := filepath.Match(pattern, domain)
	return matched
}
