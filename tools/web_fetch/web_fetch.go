package web_fetch

import (
	"context"
	"github.com/mohammad-safakhou/newser/tools/web_fetch/chromedp"
	"github.com/mohammad-safakhou/newser/tools/web_fetch/models"
	"time"
)

const (
	DefaultTimeoutMS = 15000
	MaxCharsDefault  = 20000
)

type WebFetcher interface {
	Exec(ctx context.Context, url string) (models.Result, error)
}

type FetcherType string

const (
	ChromedpFetcherType FetcherType = "chromedp"
)

func NewWebFetcher(fetcherType FetcherType, timeoutMS time.Duration, maxChars int) (WebFetcher, error) {
	if timeoutMS <= 0 {
		timeoutMS = DefaultTimeoutMS
	}
	if maxChars <= 0 {
		maxChars = MaxCharsDefault
	}

	switch fetcherType {
	case ChromedpFetcherType:
		return &chromedp.Fetch{TimeoutMS: timeoutMS, MaxChars: maxChars}, nil
	default:
		return nil, &Error{"unsupported fetcher type"}
	}
}
