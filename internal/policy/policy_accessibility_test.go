package policy

import (
	"testing"

	"github.com/mohammad-safakhou/newser/config"
)

func TestNewAccessibilityPolicyDefaults(t *testing.T) {
	policy, err := NewAccessibilityPolicy(config.AccessibilityConfig{})
	if err != nil {
		t.Fatalf("NewAccessibilityPolicy: %v", err)
	}
	if policy.MinimumContrast != 4.5 {
		t.Fatalf("expected minimum contrast 4.5, got %.2f", policy.MinimumContrast)
	}
	if policy.DefaultLocale != "en-US" {
		t.Fatalf("unexpected default locale: %s", policy.DefaultLocale)
	}
	if len(policy.SupportedLocales) == 0 {
		t.Fatalf("expected supported locales")
	}
	if !policy.SupportsLocale("en-US") {
		t.Fatalf("expected support for en-US")
	}
	if len(policy.DefaultDateFormat) == 0 || len(policy.DefaultTimeFormat) == 0 {
		t.Fatalf("expected default date/time formats")
	}
}

func TestNewAccessibilityPolicyValidation(t *testing.T) {
	_, err := NewAccessibilityPolicy(config.AccessibilityConfig{MinimumContrast: 0.5})
	if err == nil {
		t.Fatalf("expected error for low minimum contrast")
	}
}
