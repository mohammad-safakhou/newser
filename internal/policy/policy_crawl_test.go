package policy

import (
	"testing"

	"github.com/mohammad-safakhou/newser/config"
)

func TestNewCrawlPolicy(t *testing.T) {
	cfg := config.CrawlPolicyConfig{
		RespectRobots: true,
		Allow:         []string{"news.example.com"},
		Disallow:      []string{"bad.example.com"},
		Paywall:       []string{"paywall.example.com"},
		Attribution: map[string]string{
			"news.example.com": "Example News",
		},
	}

	policy, err := NewCrawlPolicy(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !policy.IsAllowed("https://news.example.com/story") {
		t.Fatalf("expected host to be allowed")
	}
	if !policy.IsDisallowed("bad.example.com") {
		t.Fatalf("expected host to be disallowed")
	}
	if !policy.RequiresPaywall("PAYWALL.EXAMPLE.COM") {
		t.Fatalf("expected paywall host to be recognized")
	}
	if note, ok := policy.Attribution("news.example.com"); !ok || note != "Example News" {
		t.Fatalf("expected attribution note, got %q", note)
	}
}

func TestNewCrawlPolicyValidation(t *testing.T) {
	cfg := config.CrawlPolicyConfig{
		Allow:    []string{"example.com"},
		Disallow: []string{"example.com"},
	}
	if _, err := NewCrawlPolicy(cfg); err == nil {
		t.Fatalf("expected validation error for conflicting allow/disallow")
	}
}
