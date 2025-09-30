package server

import "testing"

func TestSafeStringSanitizesHTML(t *testing.T) {
    input := "<script>alert('xss')</script>hello"
    got := safeString(input)
    if got != "hello" {
        t.Fatalf("expected sanitized string 'hello', got %q", got)
    }
}
