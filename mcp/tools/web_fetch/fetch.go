// Package web_fetch: headless fetch + readability extraction.
package web_fetch

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/go-shiori/go-readability"
)

type Result struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Byline      string `json:"byline"`
	PublishedAt string `json:"published_at"`
	Text        string `json:"text"`
	TopImage    string `json:"top_image"`
	HTMLHash    string `json:"html_hash"`
	Status      int    `json:"status"`
	RenderMS    int    `json:"render_ms"`
}

// Fetcher owns a long-lived Chrome context for performance.
// Construct once; call Exec per URL. Call Close() on shutdown.
type Fetcher struct {
	allocCtx  context.Context
	cancelAll context.CancelFunc
	brCtx     context.Context
	cancelBr  context.CancelFunc

	UserAgent string
	DefaultTO time.Duration
	MaxChars  int
}

// NewFetcher starts a reusable headless browser.
// userAgent is optional; default timeout and maxChars are clamped.
func NewFetcher(defaultTO time.Duration, maxChars int, userAgent string) (*Fetcher, error) {
	if defaultTO <= 0 {
		defaultTO = 30 * time.Second
	}
	if maxChars <= 0 {
		maxChars = 12000
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.UserAgent(userAgent),
	)
	actx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	bctx, cancelBr := chromedp.NewContext(actx)

	return &Fetcher{
		allocCtx:  actx,
		cancelAll: cancelAlloc,
		brCtx:     bctx,
		cancelBr:  cancelBr,
		UserAgent: userAgent,
		DefaultTO: defaultTO,
		MaxChars:  maxChars,
	}, nil
}

// Close tears down Chrome resources.
func (f *Fetcher) Close() {
	if f.cancelBr != nil {
		f.cancelBr()
	}
	if f.cancelAll != nil {
		f.cancelAll()
	}
}

// Exec navigates to link, extracts main content via readability,
// applies per-call timeout, and returns a structured Result.
// Non-HTML or parse failures return status 200 with empty text; network failures return 599.
func (f *Fetcher) Exec(ctx context.Context, link string, timeout time.Duration) (Result, error) {
	if strings.TrimSpace(link) == "" {
		return Result{}, errors.New("invalid url")
	}
	if timeout <= 0 {
		timeout = f.DefaultTO
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	t0 := time.Now()
	html, err := f.outerHTML(ctx, link)
	if err != nil {
		return Result{
			URL: link, Status: 599, RenderMS: int(time.Since(t0) / time.Millisecond),
		}, nil // synthetic failure but not a hard error
	}

	article, err := readability.FromReader(strings.NewReader(html), mustParseURL(link))
	if err != nil {
		return Result{
			URL: link, Status: 200, RenderMS: int(time.Since(t0) / time.Millisecond),
		}, nil
	}
	text := strings.TrimSpace(article.TextContent)
	if len(text) > f.MaxChars {
		text = text[:f.MaxChars]
	}
	sum := sha1.Sum([]byte(html))
	return Result{
		URL:         link,
		Title:       strings.TrimSpace(article.Title),
		Byline:      strings.TrimSpace(article.Byline),
		PublishedAt: article.SiteName, // TODO: improve date extraction
		Text:        text,
		TopImage:    article.Image,
		HTMLHash:    hex.EncodeToString(sum[:]),
		Status:      200,
		RenderMS:    int(time.Since(t0) / time.Millisecond),
	}, nil
}

func (f *Fetcher) outerHTML(ctx context.Context, link string) (string, error) {
	var html string
	err := chromedp.Run(f.brCtx,
		chromedp.Navigate(link),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	return html, err
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return &url.URL{}
	}
	return u
}
