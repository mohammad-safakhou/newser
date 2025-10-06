package helpers

import "testing"

func TestSanitizeHTMLStrict_RemovesTagsAndScripts(t *testing.T) {
	input := `<p>Hello <strong>world</strong><script>alert('x')</script></p>`
	got := SanitizeHTMLStrict(input)
	want := "Hello world"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSanitizeHTMLRichText_PreservesFormatting(t *testing.T) {
	input := `<p onclick="evil()">Hi <strong>there</strong> <a href="javascript:alert(1)">click</a></p>`
	got := SanitizeHTMLRichText(input)
	want := `<p>Hi <strong>there</strong> click</p>`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestStripUnsafeHTML_FallsBackToStrictWhenNoMarkup(t *testing.T) {
	input := `Plain text with <b>fake</b> tag`
	got := StripUnsafeHTML(input)
	want := "Plain text with <b>fake</b> tag"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestStripUnsafeHTML_KeepsRichMarkupWhenAllowed(t *testing.T) {
	input := `<p>Paragraph with <em>emphasis</em> and <a href="https://example.com">link</a></p>`
	got := StripUnsafeHTML(input)
	want := `<p>Paragraph with <em>emphasis</em> and <a href="https://example.com" rel="nofollow noopener" target="_blank">link</a></p>`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
