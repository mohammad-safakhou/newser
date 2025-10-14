package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Normalize cleans entries and removes duplicates.
func (c CrawlPolicyConfig) Normalize() CrawlPolicyConfig {
	norm := c
	norm.Allow = sanitizeDomainList(norm.Allow)
	norm.Disallow = sanitizeDomainList(norm.Disallow)
	norm.Paywall = sanitizeDomainList(norm.Paywall)
	if norm.Attribution == nil {
		norm.Attribution = map[string]string{}
	} else {
		normalizedAttr := make(map[string]string, len(norm.Attribution))
		for host, val := range norm.Attribution {
			key := normalizeHost(host)
			if key == "" {
				continue
			}
			normalizedAttr[key] = strings.TrimSpace(val)
		}
		norm.Attribution = normalizedAttr
	}
	return norm
}

// Validate ensures configured policy entries do not conflict and are well-formed.
func (c CrawlPolicyConfig) Validate() error {
	norm := c.Normalize()

	allow := make(map[string]struct{}, len(norm.Allow))
	for _, host := range norm.Allow {
		allow[host] = struct{}{}
	}
	disallow := make(map[string]struct{}, len(norm.Disallow))
	for _, host := range norm.Disallow {
		if _, ok := allow[host]; ok {
			return fmt.Errorf("crawl policy conflict: host %q present in both allow and disallow lists", host)
		}
		disallow[host] = struct{}{}
	}
	for _, host := range norm.Paywall {
		if host == "" {
			return fmt.Errorf("crawl policy paywall entry must not be empty")
		}
		if _, ok := disallow[host]; ok {
			return fmt.Errorf("crawl policy conflict: host %q marked disallow and paywall", host)
		}
	}
	for host := range norm.Attribution {
		if _, ok := allow[host]; ok {
			continue
		}
		if _, ok := disallow[host]; ok {
			continue
		}
		if host == "" {
			return fmt.Errorf("crawl policy attribution key must not be empty")
		}
	}
	return nil
}

func sanitizeDomainList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		host := normalizeHost(raw)
		if host == "" {
			continue
		}
		seen[host] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for host := range seen {
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

func normalizeHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		if u, err := url.Parse(value); err == nil && u.Host != "" {
			return strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		}
	}
	value = strings.TrimPrefix(value, "www.")
	return value
}
