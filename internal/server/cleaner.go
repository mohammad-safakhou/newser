package server

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// ExtractJSON finds and returns the first JSON object or array in s.
// It first removes Markdown code fences if present, then scans for a
// balanced {...} or [...] while ignoring braces/brackets inside strings.
func ExtractJSON(s string) (string, error) {
	s = trimBOM(strings.TrimSpace(s))

	// 1) If the whole content is a fenced block, unwrap it.
	if inner, ok := stripFirstCodeFence(s); ok {
		s = strings.TrimSpace(inner)
	}

	// 2) If the (possibly un-fenced) content already starts with JSON, try quick path.
	if len(s) > 0 && (s[0] == '{' || s[0] == '[') {
		if out, ok := extractBalancedJSONFrom(s, 0); ok {
			return out, nil
		}
	}

	// 3) Otherwise, scan for the first '{' or '[' and extract the first valid balanced segment.
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			if out, ok := extractBalancedJSONFrom(s, i); ok {
				return out, nil
			}
		}
	}

	return "", errors.New("no balanced JSON object/array found")
}

// --- helpers ---

// stripFirstCodeFence removes the first fenced code block if s starts with ``` or ~~~.
// It accepts an optional language tag (e.g., ```json).
func stripFirstCodeFence(s string) (inner string, ok bool) {
	trim := strings.TrimLeft(s, "\n\r\t ")
	if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
		fence := "```"
		if strings.HasPrefix(trim, "~~~") {
			fence = "~~~"
		}
		// Drop opening fence line
		rest := trim[len(fence):]
		// Skip optional language tag up to first newline
		if idx := strings.IndexByte(rest, '\n'); idx != -1 {
			rest = rest[idx+1:]
		} else {
			// No newline after fence -> malformed
			return "", false
		}
		// Find closing fence on its own line
		// Be tolerant: search the next occurrence of the fence sequence.
		if end := strings.Index(rest, fence); end != -1 {
			return rest[:end], true
		}
	}
	return "", false
}

// extractBalancedJSONFrom attempts to extract a balanced JSON value starting at startIdx.
// It supports objects and arrays and correctly handles strings and escape sequences.
func extractBalancedJSONFrom(s string, startIdx int) (string, bool) {
	if startIdx < 0 || startIdx >= len(s) {
		return "", false
	}

	start := s[startIdx]
	if start != '{' && start != '[' {
		return "", false
	}

	var (
		stack        []byte // track brackets/braces
		inString     bool
		escape       bool
		runeStartIdx = startIdx
	)

	push := func(b byte) { stack = append(stack, b) }
	popMatches := func(b byte) bool {
		if len(stack) == 0 {
			return false
		}
		top := stack[len(stack)-1]
		if (top == '{' && b == '}') || (top == '[' && b == ']') {
			stack = stack[:len(stack)-1]
			return true
		}
		return false
	}

	push(start)

	for i := startIdx + 1; i < len(s); i++ {
		c := s[i]

		if inString {
			if escape {
				escape = false
				continue
			}
			switch c {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{', '[':
			push(c)
		case '}', ']':
			if !popMatches(c) {
				return "", false
			}
			if len(stack) == 0 {
				// Found the matching close for the initial opener.
				return s[runeStartIdx : i+1], true
			}
		}
	}

	return "", false
}

// trimBOM removes an optional UTF-8 BOM.
func trimBOM(s string) string {
	if strings.HasPrefix(s, "\uFEFF") {
		return strings.TrimPrefix(s, "\uFEFF")
	}
	// Handle malformed BOM-like prefix (rare)
	if len(s) >= 3 {
		b0, b1, b2 := s[0], s[1], s[2]
		if b0 == 0xEF && b1 == 0xBB && b2 == 0xBF && utf8.ValidString(s[3:]) {
			return s[3:]
		}
	}
	return s
}
