package helpers

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// ExtractMarkdown returns the first fenced markdown code block content.
// Optionally filters by one or more language identifiers (case-insensitive).
// If langFilter is empty, any fenced block is returned.
// Supports ``` and ~~~ fences. Returns inner content without the fence lines.
func ExtractMarkdown(s string, langFilter ...string) (string, error) {
	s = trimBOM(strings.TrimSpace(s))
	if s == "" {
		return "", errors.New("empty input")
	}

	var want map[string]struct{}
	if len(langFilter) > 0 {
		want = make(map[string]struct{}, len(langFilter))
		for _, lf := range langFilter {
			lf = strings.ToLower(strings.TrimSpace(lf))
			if lf != "" {
				want[lf] = struct{}{}
			}
		}
	}

	search := func(fence string) (string, bool, error) {
		start := 0
		for {
			i := strings.Index(s[start:], fence)
			if i == -1 {
				return "", false, nil
			}
			i += start
			afterFence := i + len(fence)
			// Get info string (language) until newline
			nl := strings.IndexByte(s[afterFence:], '\n')
			if nl == -1 {
				return "", false, errors.New("unterminated fence (no newline after opening)")
			}
			info := strings.TrimSpace(s[afterFence : afterFence+nl])
			contentStart := afterFence + nl + 1

			// Find closing fence beginning on or after contentStart
			j := strings.Index(s[contentStart:], fence)
			if j == -1 {
				return "", false, errors.New("unterminated fenced block (no closing)")
			}
			closeIdx := contentStart + j
			content := s[contentStart:closeIdx]

			// Language filtering
			if want != nil {
				lang := strings.ToLower(strings.Fields(info + " ")[0])
				if lang == "" {
					// No language tag; skip if we are filtering
					start = afterFence
					continue
				}
				if _, ok := want[lang]; !ok {
					start = afterFence
					continue
				}
			}
			return strings.TrimSpace(content), true, nil
		}
	}

	// Try backtick fences first, then tildes
	if out, ok, err := search("```"); err != nil {
		return "", err
	} else if ok {
		return out, nil
	}
	if out, ok, err := search("~~~"); err != nil {
		return "", err
	} else if ok {
		return out, nil
	}

	return "", errors.New("no fenced markdown block found")
}

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
