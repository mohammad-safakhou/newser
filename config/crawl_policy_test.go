package config

import "testing"

func TestCrawlPolicyNormalize(t *testing.T) {
	cfg := CrawlPolicyConfig{
		Allow:       []string{"Example.com", "https://news.example.com"},
		Disallow:    []string{"www.Example.com", "bad.com"},
		Paywall:     []string{"Paywall.com", "PAYWALL.COM"},
		Attribution: map[string]string{"WWW.Example.com": "Example", "PAYWALL.com": "Paywall"},
	}

	norm := cfg.Normalize()
	if len(norm.Allow) != 2 || norm.Allow[0] != "example.com" {
		t.Fatalf("unexpected allow list: %#v", norm.Allow)
	}
	if len(norm.Disallow) != 2 || norm.Disallow[0] != "bad.com" {
		t.Fatalf("unexpected disallow list: %#v", norm.Disallow)
	}
	if len(norm.Paywall) != 1 || norm.Paywall[0] != "paywall.com" {
		t.Fatalf("unexpected paywall list: %#v", norm.Paywall)
	}
	if val := norm.Attribution["example.com"]; val != "Example" {
		t.Fatalf("expected attribution for example.com, got %q", val)
	}
}

func TestCrawlPolicyValidate(t *testing.T) {
	valid := CrawlPolicyConfig{
		Allow:       []string{"example.com"},
		Disallow:    []string{"blocked.com"},
		Attribution: map[string]string{"example.com": "Example"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	conflict := CrawlPolicyConfig{
		Allow:    []string{"example.com"},
		Disallow: []string{"example.com"},
	}
	if err := conflict.Validate(); err == nil {
		t.Fatalf("expected conflict validation error")
	}

	paywallConflict := CrawlPolicyConfig{
		Disallow: []string{"paywall.com"},
		Paywall:  []string{"paywall.com"},
	}
	if err := paywallConflict.Validate(); err == nil {
		t.Fatalf("expected paywall/disallow conflict error")
	}
}
