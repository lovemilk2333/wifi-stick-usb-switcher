package daemon

import (
	"fmt"
	"net/netip"
	"strings"
)

const (
	DOMAIN_FILTER_SUFFIX_SUBDOMAIN_PREFIX      = "+."
	DOMAIN_FILTER_SUFFIX_PUNYCODE_PREFIX       = "+*."
	DOMAIN_FILTER_SUFFIX_ANY_SUBDOMAIN_PREFIX  = "*."
	DOMAIN_FILTER_SUFFIX_SINGLE_LABEL_PREFIX   = "?."
	DOMAIN_FILTER_SUFFIX_DEEP_SUBDOMAIN_PREFIX = "**."
	DOMAIN_FILTER_ERROR_EMPTY_RULE             = "--tls-allow entries must not be empty"
	DOMAIN_FILTER_ERROR_IP_REQUIRES_CIDR       = "--tls-allow IP entries must use CIDR notation: %q"
	DOMAIN_FILTER_ERROR_INVALID_DNS_PATTERN    = "invalid --tls-allow DNS pattern %q"
	DOMAIN_FILTER_PUNYCODE_LABEL_PREFIX        = "xn--"
)

type domainFilterRuleKind uint8

const (
	DOMAIN_FILTER_RULE_EXACT domainFilterRuleKind = iota
	DOMAIN_FILTER_RULE_CIDR
	DOMAIN_FILTER_RULE_SUBDOMAIN
	DOMAIN_FILTER_RULE_PUNYCODE_LABEL
	DOMAIN_FILTER_RULE_DEEP_SUBDOMAIN
	DOMAIN_FILTER_RULE_DOMAIN_AND_SUBDOMAINS
	DOMAIN_FILTER_RULE_SINGLE_LABEL
)

type domainFilterRule struct {
	kind   domainFilterRuleKind
	prefix netip.Prefix
	name   string
}

type domainFilter struct {
	rules []domainFilterRule
}

func parse_domain_filter(values []string) (*domainFilter, error) {
	filter := &domainFilter{rules: make([]domainFilterRule, 0, len(values))}
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			return nil, fmt.Errorf(DOMAIN_FILTER_ERROR_EMPTY_RULE)
		}
		if prefix, err := netip.ParsePrefix(value); err == nil {
			filter.rules = append(filter.rules, domainFilterRule{kind: DOMAIN_FILTER_RULE_CIDR, prefix: prefix.Masked()})
			continue
		}
		if _, err := netip.ParseAddr(value); err == nil {
			return nil, fmt.Errorf(DOMAIN_FILTER_ERROR_IP_REQUIRES_CIDR, raw)
		}

		rule := domainFilterRule{kind: DOMAIN_FILTER_RULE_EXACT, name: value}
		switch {
		case strings.HasPrefix(value, DOMAIN_FILTER_SUFFIX_PUNYCODE_PREFIX):
			rule.kind, rule.name = DOMAIN_FILTER_RULE_PUNYCODE_LABEL, strings.TrimPrefix(value, DOMAIN_FILTER_SUFFIX_PUNYCODE_PREFIX)
		case strings.HasPrefix(value, DOMAIN_FILTER_SUFFIX_DEEP_SUBDOMAIN_PREFIX):
			rule.kind, rule.name = DOMAIN_FILTER_RULE_DEEP_SUBDOMAIN, strings.TrimPrefix(value, DOMAIN_FILTER_SUFFIX_DEEP_SUBDOMAIN_PREFIX)
		case strings.HasPrefix(value, DOMAIN_FILTER_SUFFIX_SUBDOMAIN_PREFIX):
			rule.kind, rule.name = DOMAIN_FILTER_RULE_SUBDOMAIN, strings.TrimPrefix(value, DOMAIN_FILTER_SUFFIX_SUBDOMAIN_PREFIX)
		case strings.HasPrefix(value, DOMAIN_FILTER_SUFFIX_ANY_SUBDOMAIN_PREFIX):
			rule.kind, rule.name = DOMAIN_FILTER_RULE_DOMAIN_AND_SUBDOMAINS, strings.TrimPrefix(value, DOMAIN_FILTER_SUFFIX_ANY_SUBDOMAIN_PREFIX)
		case strings.HasPrefix(value, DOMAIN_FILTER_SUFFIX_SINGLE_LABEL_PREFIX):
			rule.kind, rule.name = DOMAIN_FILTER_RULE_SINGLE_LABEL, strings.TrimPrefix(value, DOMAIN_FILTER_SUFFIX_SINGLE_LABEL_PREFIX)
		}
		if strings.ContainsAny(rule.name, "*+?") || !valid_dns_name(rule.name) {
			return nil, fmt.Errorf(DOMAIN_FILTER_ERROR_INVALID_DNS_PATTERN, raw)
		}
		filter.rules = append(filter.rules, rule)
	}
	return filter, nil
}

func (this *domainFilter) configured() bool {
	return this != nil && len(this.rules) > 0
}

func (this *domainFilter) allows(name string) bool {
	if !this.configured() {
		return true
	}
	if ip, err := netip.ParseAddr(name); err == nil {
		for _, rule := range this.rules {
			if rule.kind == DOMAIN_FILTER_RULE_CIDR && rule.prefix.Contains(ip) {
				return true
			}
		}
		return false
	}

	name = strings.ToLower(name)
	for _, rule := range this.rules {
		switch rule.kind {
		case DOMAIN_FILTER_RULE_EXACT:
			if name == rule.name {
				return true
			}
		case DOMAIN_FILTER_RULE_SUBDOMAIN:
			if name != rule.name && strings.HasSuffix(name, "."+rule.name) {
				return true
			}
		case DOMAIN_FILTER_RULE_PUNYCODE_LABEL:
			prefix := strings.TrimSuffix(name, "."+rule.name)
			if prefix != name && !strings.Contains(prefix, ".") && strings.HasPrefix(prefix, DOMAIN_FILTER_PUNYCODE_LABEL_PREFIX) && len(prefix) > len(DOMAIN_FILTER_PUNYCODE_LABEL_PREFIX) {
				return true
			}
		case DOMAIN_FILTER_RULE_DEEP_SUBDOMAIN:
			if name != rule.name && strings.HasSuffix(name, "."+rule.name) {
				return true
			}
		case DOMAIN_FILTER_RULE_DOMAIN_AND_SUBDOMAINS:
			if name == rule.name || strings.HasSuffix(name, "."+rule.name) {
				return true
			}
		case DOMAIN_FILTER_RULE_SINGLE_LABEL:
			prefix := strings.TrimSuffix(name, "."+rule.name)
			if len(prefix) == 1 && prefix != name {
				return true
			}
		}
	}
	return false
}

func valid_dns_name(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	return valid_certificate_name(name)
}
