package chromedp

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"github.com/chromedp/chromedp"
	"github.com/go-shiori/go-readability"
	"github.com/mohammad-safakhou/newser/tools/web_fetch/models"
	"net/url"
	"strings"
	"time"
)

type Fetch struct {
	TimeoutMS time.Duration // Timeout in milliseconds
	MaxChars  int           // Maximum characters to return from the article text
}

func (f Fetch) Exec(ctx context.Context, url string) (models.Result, error) {
	if strings.TrimSpace(url) == "" {
		return models.Result{}, errors.New("invalid url")
	}

	ctx, cancel := context.WithTimeout(ctx, f.TimeoutMS)
	defer cancel()
	t0 := time.Now()

	// Headless browsing
	html, err := fetchHTML(ctx, url)
	if err != nil {
		return models.Result{URL: url, Status: 599, RenderMS: int(time.Since(t0) / time.Millisecond)}, nil
	}

	// Extract content using readability
	article, err := readability.FromReader(strings.NewReader(html), mustParseURL(url))
	if err != nil {
		return models.Result{URL: url, Status: 200, RenderMS: int(time.Since(t0) / time.Millisecond)}, nil
	}
	text := article.TextContent
	if len(text) > f.MaxChars {
		text = text[:f.MaxChars]
	}

	// Hash raw HTML
	sum := sha1.Sum([]byte(html))
	htmlHash := hex.EncodeToString(sum[:])

	return models.Result{
		URL:         url,
		Title:       strings.TrimSpace(article.Title),
		Byline:      strings.TrimSpace(article.Byline),
		PublishedAt: article.SiteName, // Placeholder for actual date parsing
		Text:        strings.TrimSpace(text),
		TopImage:    article.Image,
		HTMLHash:    htmlHash,
		Status:      200,
		RenderMS:    int(time.Since(t0) / time.Millisecond),
	}, nil
}

func fetchHTML(ctx context.Context, url string) (string, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.UserAgent("RealTimeAIAgent/1.0 (+contact@example.com)"),
	)
	actx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	bctx, cancelBrowser := chromedp.NewContext(actx)
	defer cancelBrowser()

	var html string
	err := chromedp.Run(bctx,
		chromedp.Navigate(url),
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
