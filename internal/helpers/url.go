package helpers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"sort"
	"strings"
)

var trackingQueryParams = map[string]struct{}{
	"utm_source":      {},
	"utm_medium":      {},
	"utm_campaign":    {},
	"utm_term":        {},
	"utm_content":     {},
	"utm_id":          {},
	"utm_name":        {},
	"utm_reader":      {},
	"utm_place":       {},
	"utm_social":      {},
	"utm_social-type": {},
	"gclid":           {},
	"dclid":           {},
	"fbclid":          {},
	"msclkid":         {},
	"igshid":          {},
}

// CanonicalURL normalises a URL string for comparison and fingerprinting.
// It lowercases scheme/host, removes default ports, strips fragments,
// cleans path segments, removes tracking query parameters (utm_*, fbclid, etc.)
// and sorts remaining query parameters deterministically. When the scheme is
// omitted it defaults to https.
func CanonicalURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty url")
	}

	parsed, err := parseURLPreserveHost(raw)
	if err != nil {
		return "", err
	}

	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)

	host := strings.ToLower(parsed.Host)
	if host == "" {
		return "", errors.New("url missing host")
	}
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		if len(parts) == 2 {
			port := parts[1]
			if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
				host = parts[0]
			}
		}
	}
	parsed.Host = host

	if parsed.Path == "" {
		parsed.Path = "/"
	}
	cleanPath := path.Clean(parsed.Path)
	if cleanPath == "." {
		cleanPath = "/"
	}
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}
	if cleanPath != "/" && strings.HasSuffix(parsed.Path, "/") && !strings.HasSuffix(cleanPath, "/") {
		// Preserve trailing slash if it was explicitly present and not root.
		cleanPath += "/"
	}
	parsed.Path = cleanPath

	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if _, drop := trackingQueryParams[lower]; drop {
			query.Del(key)
			continue
		}
	}

	if len(query) == 0 {
		parsed.RawQuery = ""
	} else {
		keys := make([]string, 0, len(query))
		cleaned := make(map[string][]string, len(query))
		for key, values := range query {
			keys = append(keys, key)
			copied := append([]string(nil), values...)
			sort.Strings(copied)
			cleaned[key] = copied
		}
		sort.Strings(keys)
		var b strings.Builder
		for idx, key := range keys {
			values := cleaned[key]
			for vIdx, value := range values {
				if b.Len() > 0 {
					b.WriteByte('&')
				}
				b.WriteString(url.QueryEscape(key))
				if value != "" {
					b.WriteByte('=')
					b.WriteString(url.QueryEscape(value))
				}
				if idx == len(keys)-1 && vIdx == len(values)-1 {
					continue
				}
			}
		}
		parsed.RawQuery = b.String()
	}

	return parsed.String(), nil
}

// URLFingerprint returns a deterministic SHA-256 hex digest derived from the canonical URL.
func URLFingerprint(raw string) (string, error) {
	canonical, err := CanonicalURL(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

// parseURLPreserveHost attempts to parse raw into a url.URL, handling schemeless URLs.
func parseURLPreserveHost(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" && parsed.Host == "" {
		// Attempt schemeless format like example.com/path or //example.com/path.
		if strings.HasPrefix(raw, "//") {
			return url.Parse("https:" + raw)
		}
		return url.Parse("https://" + raw)
	}
	return parsed, nil
}
