package helpers

import "testing"

func TestContrastRatioBlackWhite(t *testing.T) {
	ratio, err := ContrastRatio("#000000", "#ffffff")
	if err != nil {
		t.Fatalf("ContrastRatio: %v", err)
	}
	if ratio < 21.0 || ratio > 21.1 {
		t.Fatalf("expected ratio ~21, got %.2f", ratio)
	}
}

func TestMeetsWCAGAA(t *testing.T) {
	ok, err := MeetsWCAGAA("#777777", "#ffffff", false)
	if err != nil {
		t.Fatalf("MeetsWCAGAA: %v", err)
	}
	if ok {
		t.Fatalf("expected contrast to fail for normal text")
	}
	ok, err = MeetsWCAGAA("#333333", "#ffffff", false)
	if err != nil {
		t.Fatalf("MeetsWCAGAA: %v", err)
	}
	if !ok {
		t.Fatalf("expected contrast to pass for darker text")
	}
}

func TestEnsureAriaLabel(t *testing.T) {
	if got := EnsureAriaLabel("  Primary action  ", "fallback"); got != "Primary action" {
		t.Fatalf("unexpected label: %q", got)
	}
	if got := EnsureAriaLabel("   ", "  fallback label "); got != "fallback label" {
		t.Fatalf("expected fallback label, got %q", got)
	}
}

func TestFocusHelpers(t *testing.T) {
	order := []string{"name", "email", "submit"}
	if idx := FocusOrderIndex("email", order); idx != 1 {
		t.Fatalf("expected index 1, got %d", idx)
	}
	if idx := FocusOrderIndex("missing", order); idx != -1 {
		t.Fatalf("expected -1 for missing element, got %d", idx)
	}

	next, ok := NextFocusTarget("email", order)
	if !ok || next != "submit" {
		t.Fatalf("expected next focus submit, got %q ok=%t", next, ok)
	}
	next, ok = NextFocusTarget("submit", order)
	if !ok || next != "name" {
		t.Fatalf("expected wrap around to name, got %q ok=%t", next, ok)
	}
	next, ok = NextFocusTarget("unknown", order)
	if !ok || next != "name" {
		t.Fatalf("expected first element when not found, got %q ok=%t", next, ok)
	}
}
