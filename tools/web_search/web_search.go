package web_search

import (
	"context"

	"github.com/mohammad-safakhou/newser/tools/web_search/brave"
	"github.com/mohammad-safakhou/newser/tools/web_search/models"
	"github.com/mohammad-safakhou/newser/tools/web_search/serper"
)

type WebSearcher interface {
	Discover(ctx context.Context, q string, k int, sites []string, recency int) ([]models.Result, error)
}

type Provider string

const (
	SerperProvider Provider = "serper"
	BraveProvider  Provider = "brave"
)

var ErrUnsupportedProvider = &Error{"unsupported provider"}

func NewWebSearcher(provider Provider, apiKey string) (WebSearcher, error) {
	switch provider {
	case SerperProvider:
		return serper.Search{ApiKey: apiKey}, nil
	case BraveProvider:
		return brave.Search{ApiKey: apiKey}, nil
	default:
		return nil, ErrUnsupportedProvider
	}
}
