package helpers

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ContrastRatio computes the WCAG relative luminance contrast ratio for two hex colours.
// Colours must be supplied as #RGB or #RRGGBB.
func ContrastRatio(foreground, background string) (float64, error) {
	fg, err := parseHexColour(foreground)
	if err != nil {
		return 0, fmt.Errorf("foreground: %w", err)
	}
	bg, err := parseHexColour(background)
	if err != nil {
		return 0, fmt.Errorf("background: %w", err)
	}
	l1 := relativeLuminance(fg)
	l2 := relativeLuminance(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05), nil
}

// MeetsWCAGAA reports whether the colour pair satisfies WCAG AA contrast requirements.
// Large text lowers the minimum ratio from 4.5 to 3.0.
func MeetsWCAGAA(foreground, background string, largeText bool) (bool, error) {
	ratio, err := ContrastRatio(foreground, background)
	if err != nil {
		return false, err
	}
	min := 4.5
	if largeText {
		min = 3.0
	}
	return ratio >= min, nil
}

// EnsureAriaLabel returns a trimmed label or the fallback when empty.
func EnsureAriaLabel(label, fallback string) string {
	label = strings.TrimSpace(label)
	if label != "" {
		return label
	}
	return strings.TrimSpace(fallback)
}

// NextFocusTarget returns the next identifier in the focus order. When current is not found,
// the first entry is returned. A boolean signals whether a target exists.
func NextFocusTarget(current string, order []string) (string, bool) {
	if len(order) == 0 {
		return "", false
	}
	idx := FocusOrderIndex(current, order)
	if idx == -1 {
		return order[0], true
	}
	return order[(idx+1)%len(order)], true
}

// FocusOrderIndex returns the index of the current identifier within the ordered slice.
// A value of -1 indicates the identifier is not present.
func FocusOrderIndex(current string, order []string) int {
	current = strings.TrimSpace(current)
	for i, id := range order {
		if strings.TrimSpace(id) == current {
			return i
		}
	}
	return -1
}

type rgb struct {
	r float64
	g float64
	b float64
}

func parseHexColour(raw string) (rgb, error) {
	s := strings.TrimSpace(strings.TrimPrefix(raw, "#"))
	if len(s) != 6 && len(s) != 3 {
		return rgb{}, fmt.Errorf("invalid colour %q", raw)
	}
	if len(s) == 3 {
		s = strings.ToLower(s)
		s = fmt.Sprintf("%c%c%c%c%c%c", s[0], s[0], s[1], s[1], s[2], s[2])
	}
	var r, g, b uint64
	var err error
	if r, err = parseHexComponent(s[0:2]); err != nil {
		return rgb{}, err
	}
	if g, err = parseHexComponent(s[2:4]); err != nil {
		return rgb{}, err
	}
	if b, err = parseHexComponent(s[4:6]); err != nil {
		return rgb{}, err
	}
	return rgb{
		r: float64(r) / 255.0,
		g: float64(g) / 255.0,
		b: float64(b) / 255.0,
	}, nil
}

func parseHexComponent(component string) (uint64, error) {
	v, err := strconv.ParseUint(component, 16, 8)
	if err != nil {
		return 0, fmt.Errorf("invalid hex component %q", component)
	}
	return v, nil
}

func relativeLuminance(c rgb) float64 {
	return 0.2126*linearise(c.r) + 0.7152*linearise(c.g) + 0.0722*linearise(c.b)
}

func linearise(v float64) float64 {
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}
