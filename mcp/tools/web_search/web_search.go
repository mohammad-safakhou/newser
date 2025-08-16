// Package websearch: thin Brave & Serper clients with robust handling.
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// -------- Brave --------

type Brave struct {
	APIKey string
	Doer   Doer // inject http.Client for tests/timeouts
}

// Discover returns up to k results for q. k is clamped to [1,25].
// Non-200s return an error with a truncated body for clarity.
func (b Brave) Discover(ctx context.Context, q string, k int, sites []string, recency int) ([]Result, error) {
	if strings.TrimSpace(q) == "" {
		return nil, errors.New("brave: empty query")
	}
	if k < 1 || k > 25 {
		k = 10
	}
	if b.Doer == nil {
		b.Doer = &http.Client{Timeout: 20 * time.Second}
	}

	url := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d", urlQuery(q), k)
	// NOTE: Add site filters/freshness here if you want to support them explicitly.

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.APIKey)

	resp, err := b.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var raw struct {
		Web struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Snippet string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(raw.Web.Results))
	for i, r := range raw.Web.Results {
		if i >= k {
			break
		}
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: r.Snippet})
	}
	return out, nil
}

// -------- Serper --------

type Serper struct {
	APIKey string
	Doer   Doer
}

func (s Serper) Discover(ctx context.Context, q string, k int, sites []string, recencyDays int) ([]Result, error) {
	if strings.TrimSpace(q) == "" {
		return nil, errors.New("serper: empty query")
	}
	if k < 1 || k > 25 {
		k = 10
	}
	if s.Doer == nil {
		s.Doer = &http.Client{Timeout: 20 * time.Second}
	}

	payload := map[string]any{"q": q, "num": k}
	if len(sites) > 0 {
		payload["site"] = strings.Join(sites, " OR ")
	}
	if recencyDays > 0 {
		payload["tbs"] = recencyToTBS(recencyDays)
	}
	b, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://google.serper.dev/search", strings.NewReader(string(b)))
	req.Header.Set("X-API-KEY", s.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("serper %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var raw struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(raw.Organic))
	for i, r := range raw.Organic {
		if i >= k {
			break
		}
		out = append(out, Result{Title: r.Title, URL: r.Link, Snippet: r.Snippet})
	}
	return out, nil
}

// ---------- helpers ----------

func urlQuery(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), " ", "+") }

func recencyToTBS(days int) string {
	switch {
	case days <= 1:
		return "qdr:d"
	case days <= 7:
		return "qdr:w"
	case days <= 31:
		return "qdr:m"
	default:
		return "qdr:y"
	}
}
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
