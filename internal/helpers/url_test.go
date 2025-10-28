package helpers

import (
	"strings"
	"testing"
)

func TestCanonicalURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "defaults https and cleans path",
			in:   "Example.com/news/../tech/latest",
			want: "https://example.com/tech/latest",
		},
		{
			name: "removes default port and tracking params",
			in:   "http://news.example.com:80/article?id=123&utm_source=rss#section",
			want: "http://news.example.com/article?id=123",
		},
		{
			name: "sorts query parameters and preserves trailing slash",
			in:   "https://example.com/path/?b=2&a=1&fbclid=xyz",
			want: "https://example.com/path/?a=1&b=2",
		},
		{
			name: "handles schemeless url with double slash",
			in:   "//blog.example.com/post/42?utm_medium=email",
			want: "https://blog.example.com/post/42",
		},
		{
			name: "normalises repeated slashes",
			in:   "https://example.com//a//b///c",
			want: "https://example.com/a/b/c",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalURL(tt.in)
			if err != nil {
				t.Fatalf("CanonicalURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("CanonicalURL() got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCanonicalURLErrors(t *testing.T) {
	t.Parallel()
	if _, err := CanonicalURL(""); err == nil {
		t.Fatalf("expected error for empty input")
	}
	if _, err := CanonicalURL(":///invalid"); err == nil {
		t.Fatalf("expected error for malformed url")
	}
}

func TestURLFingerprintDeterministic(t *testing.T) {
	t.Parallel()
	url := "https://Example.com/Article?utm_campaign=foo&a=1&b=2"
	fp1, err := URLFingerprint(url)
	if err != nil {
		t.Fatalf("URLFingerprint: %v", err)
	}
	fp2Input := strings.ReplaceAll(url, "https://", "HTTPS://")
	fp2, err := URLFingerprint(fp2Input)
	if err != nil {
		t.Fatalf("URLFingerprint: %v", err)
	}
	if fp1 == "" || fp2 == "" {
		t.Fatalf("expected fingerprint to be non-empty")
	}
	if fp1 != fp2 {
		t.Fatalf("expected deterministic fingerprint, got %s vs %s", fp1, fp2)
	}
}
