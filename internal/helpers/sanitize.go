package helpers

import (
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

var (
	strictPolicyOnce sync.Once
	strictPolicy     *bluemonday.Policy

	richTextPolicyOnce sync.Once
	richTextPolicy     *bluemonday.Policy
)

// StrictHTMLPolicy returns a singleton bluemonday policy that strips every HTML
// element and attribute. It is useful when the output should be treated as
// plain text while ensuring that script/style injections are removed.
func StrictHTMLPolicy() *bluemonday.Policy {
	strictPolicyOnce.Do(func() {
		strictPolicy = bluemonday.StrictPolicy()
	})
	return strictPolicy
}

// RichTextHTMLPolicy returns a policy that allows a small, curated subset of
// HTML formatting tags (paragraphs, emphasis, lists, code blocks, links) while
// ensuring dangerous attributes and JavaScript URLs are removed. The policy is
// cached to avoid repeated allocations when sanitising many fragments.
func RichTextHTMLPolicy() *bluemonday.Policy {
	richTextPolicyOnce.Do(func() {
		policy := bluemonday.UGCPolicy()
		policy.AllowElements("figure", "figcaption")
		policy.AllowAttrs("class").OnElements("code", "pre", "figure")
		policy.AllowURLSchemes("http", "https", "mailto")
		policy.AllowRelativeURLs(true)
		policy.RequireParseableURLs(true)
		policy.AddTargetBlankToFullyQualifiedLinks(true)
		richTextPolicy = policy
	})
	return richTextPolicy
}

// SanitizeHTMLStrict removes every HTML tag from s while stripping leading and
// trailing whitespace. It provides a safe plain-text representation of the
// value.
func SanitizeHTMLStrict(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.TrimSpace(StrictHTMLPolicy().Sanitize(s))
}

// SanitizeHTMLRichText cleans s using RichTextHTMLPolicy while preserving a
// limited set of formatting tags. Event handlers, inline scripts and unsafe
// URLs are removed.
func SanitizeHTMLRichText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.TrimSpace(RichTextHTMLPolicy().Sanitize(s))
}

// StripUnsafeHTML normalises the provided HTML string by first attempting the
// rich-text sanitisation. If no HTML tags survive the clean-up, it falls back
// to the strict policy so callers receive a plain-text value. It is handy for
// user-supplied fragments that may occasionally include formatting.
func StripUnsafeHTML(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	sanitized := SanitizeHTMLRichText(s)
	if strings.ContainsAny(sanitized, "<>/") {
		return sanitized
	}
	return SanitizeHTMLStrict(s)
}
