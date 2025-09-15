package core

import (
    "context"
    "fmt"
    "net/url"
    "strings"
    "time"

	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/config"
)

// NewsAPIClient implements SourceProvider using newsapi.org
type NewsAPIClient struct {
	cfg  config.NewsAPIConfig
	http *HTTPClient
}

func (n *NewsAPIClient) Search(ctx context.Context, query string, options map[string]interface{}) ([]Source, error) {
	endpoint := n.cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://newsapi.org/v2/everything"
	}
	var resp struct {
		Articles []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			PublishedAt string `json:"publishedAt"`
			Description string `json:"description"`
			Content     string `json:"content"`
			Source      struct {
				Name string `json:"name"`
			} `json:"source"`
		} `json:"articles"`
	}
	headers := map[string]string{"X-Api-Key": n.cfg.APIKey}
	q := query
	if mq, ok := options["query"].(string); ok && mq != "" {
		q = mq
	}
    url := fmt.Sprintf("%s?q=%s&language=en&sortBy=publishedAt&pageSize=%d", endpoint, escapeQuery(q), max1(n.cfg.MaxResults, 20))
    // Optional since filter
    if sv, ok := options["since"]; ok {
        var t time.Time
        switch v := sv.(type) {
        case time.Time:
            t = v
        case string:
            if tt, e := time.Parse(time.RFC3339, v); e == nil { t = tt }
        }
        if !t.IsZero() {
            url += "&from=" + t.UTC().Format(time.RFC3339)
        }
    }
	if err := n.http.DoJSON(ctx, "GET", url, headers, nil, &resp); err != nil {
		return nil, err
	}
    // Exclusion list of known URLs
    exclude := map[string]bool{}
    if arr, ok := options["exclude_urls"].([]string); ok {
        for _, u := range arr { if u != "" { exclude[u] = true } }
    } else if ai, ok := options["exclude_urls"].([]interface{}); ok {
        for _, it := range ai { if s, ok := it.(string); ok && s != "" { exclude[s] = true } }
    }
    var out []Source
    for _, a := range resp.Articles {
        ts, _ := time.Parse(time.RFC3339, a.PublishedAt)
        if exclude[a.URL] { continue }
        out = append(out, Source{
            ID: uuid.NewString(), Title: a.Title, URL: a.URL, Type: "news",
            Credibility: 0.8, PublishedAt: ts, ExtractedAt: time.Now(),
            Content: strings.TrimSpace(a.Content), Summary: strings.TrimSpace(a.Description),
            Tags: []string{"newsapi"},
        })
    }
    return out, nil
}

func (n *NewsAPIClient) GetSource(ctx context.Context, id string) (Source, error) {
	return Source{}, fmt.Errorf("not implemented")
}
func (n *NewsAPIClient) GetSourceTypes() []string        { return []string{"news"} }
func (n *NewsAPIClient) GetCredibility(s Source) float64 { return 0.8 }

// BraveClient implements SourceProvider using Brave Search API
type BraveClient struct {
	cfg  config.WebSearchConfig
	http *HTTPClient
}

func (b *BraveClient) Search(ctx context.Context, query string, options map[string]interface{}) ([]Source, error) {
    var resp struct {
        Web struct {
            Results []struct{ Title, URL, Description string } `json:"results"`
        } `json:"web"`
    }
    headers := map[string]string{"X-Subscription-Token": b.cfg.BraveAPIKey}
    q := query
    if mq, ok := options["query"].(string); ok && mq != "" {
        q = mq
    }
    // Build query parameters
    vals := url.Values{}
    vals.Set("q", q)
    // count and offset
    cnt := max1(b.cfg.MaxResults, 10)
    if v, ok := asInt(options["count"]); ok { if v > 0 && v <= 20 { cnt = v } }
    vals.Set("count", fmt.Sprintf("%d", cnt))
    if v, ok := asInt(options["offset"]); ok { if v >= 0 && v <= 9 { vals.Set("offset", fmt.Sprintf("%d", v)) } }
    // locale and safety
    if s, ok := asString(options["country"]); ok && s != "" { vals.Set("country", s) }
    if s, ok := asString(options["search_lang"]); ok && s != "" { vals.Set("search_lang", s) }
    if s, ok := asString(options["ui_lang"]); ok && s != "" { vals.Set("ui_lang", s) }
    if s, ok := asString(options["safesearch"]); ok && s != "" { vals.Set("safesearch", s) }
    if s, ok := asString(options["freshness"]); ok && s != "" { vals.Set("freshness", s) }
    if v, ok := asBool(options["text_decorations"]); ok { vals.Set("text_decorations", boolStr(v)) } else { vals.Set("text_decorations", "false") }
    if v, ok := asBool(options["spellcheck"]); ok { vals.Set("spellcheck", boolStr(v)) }
    if s, ok := asString(options["units"]); ok && s != "" { vals.Set("units", s) }
    if v, ok := asBool(options["extra_snippets"]); ok { vals.Set("extra_snippets", boolStr(v)) }
    if v, ok := asBool(options["summary"]); ok { vals.Set("summary", boolStr(v)) }
    // result_filter: CSV string or []string
    if s, ok := asString(options["result_filter"]); ok && s != "" { vals.Set("result_filter", s) }
    if arr, ok := options["result_filter"].([]string); ok && len(arr) > 0 { vals.Set("result_filter", strings.Join(arr, ",")) }
    // goggles: repeated parameter
    if arr, ok := options["goggles"].([]string); ok {
        for _, g := range arr { if g != "" { vals.Add("goggles", g) } }
    }
    endpoint := "https://api.search.brave.com/res/v1/web/search?" + vals.Encode()
    if err := b.http.DoJSON(ctx, "GET", endpoint, headers, nil, &resp); err != nil {
        return nil, err
    }
    // Exclusion list of known URLs
    exclude := map[string]bool{}
    if arr, ok := options["exclude_urls"].([]string); ok {
        for _, u := range arr { if u != "" { exclude[u] = true } }
    } else if ai, ok := options["exclude_urls"].([]interface{}); ok {
        for _, it := range ai { if s, ok := it.(string); ok && s != "" { exclude[s] = true } }
    }
    var out []Source
    for _, r := range resp.Web.Results {
        if exclude[r.URL] { continue }
        out = append(out, Source{ID: uuid.NewString(), Title: r.Title, URL: r.URL, Type: "web", Credibility: 0.6, ExtractedAt: time.Now(), Summary: r.Description, Tags: []string{"brave"}})
    }
    return out, nil
}
func (b *BraveClient) GetSource(ctx context.Context, id string) (Source, error) {
	return Source{}, fmt.Errorf("not implemented")
}
func (b *BraveClient) GetSourceTypes() []string        { return []string{"web"} }
func (b *BraveClient) GetCredibility(s Source) float64 { return 0.6 }

// SerperClient implements SourceProvider using serper.dev
type SerperClient struct {
	cfg  config.WebSearchConfig
	http *HTTPClient
}

func (s *SerperClient) Search(ctx context.Context, query string, options map[string]interface{}) ([]Source, error) {
	var resp struct {
		Organic []struct{ Title, Link, Snippet string } `json:"organic"`
	}
	headers := map[string]string{"X-API-KEY": s.cfg.SerperAPIKey}
	body := map[string]any{"q": query, "num": max1(s.cfg.MaxResults, 10)}
	if mq, ok := options["query"].(string); ok && mq != "" {
		body["q"] = mq
	}
	if err := s.http.DoJSON(ctx, "POST", "https://google.serper.dev/search", headers, body, &resp); err != nil {
		return nil, err
	}
    // Exclusion list of known URLs
    exclude := map[string]bool{}
    if arr, ok := options["exclude_urls"].([]string); ok {
        for _, u := range arr { if u != "" { exclude[u] = true } }
    } else if ai, ok := options["exclude_urls"].([]interface{}); ok {
        for _, it := range ai { if s, ok := it.(string); ok && s != "" { exclude[s] = true } }
    }
    var out []Source
    for _, r := range resp.Organic {
        if exclude[r.Link] { continue }
        out = append(out, Source{ID: uuid.NewString(), Title: r.Title, URL: r.Link, Type: "web", Credibility: 0.65, ExtractedAt: time.Now(), Summary: r.Snippet, Tags: []string{"serper"}})
    }
    return out, nil
}
func (s *SerperClient) GetSource(ctx context.Context, id string) (Source, error) {
	return Source{}, fmt.Errorf("not implemented")
}
func (s *SerperClient) GetSourceTypes() []string          { return []string{"web"} }
func (s *SerperClient) GetCredibility(src Source) float64 { return 0.65 }

func escapeQuery(q string) string { return strings.ReplaceAll(q, " ", "+") }
func max1(a, def int) int {
    if a > 0 {
        return a
    }
    return def
}
func asString(v interface{}) (string, bool) {
    s, ok := v.(string)
    return s, ok
}
func asInt(v interface{}) (int, bool) {
    switch t := v.(type) {
    case int:
        return t, true
    case int64:
        return int(t), true
    case float64:
        return int(t), true
    default:
        return 0, false
    }
}
func asBool(v interface{}) (bool, bool) {
    b, ok := v.(bool)
    return b, ok
}
func boolStr(b bool) string { if b { return "true" }; return "false" }

// DeduplicateSources merges sources by URL (or title fallback) and keeps the highest credibility
func DeduplicateSources(in []Source) []Source {
	seen := make(map[string]Source)
	keyOf := func(s Source) string {
		if s.URL != "" {
			return s.URL
		}
		return strings.ToLower(s.Title)
	}
	for _, s := range in {
		k := keyOf(s)
		if prev, ok := seen[k]; ok {
			if s.Credibility > prev.Credibility {
				seen[k] = s
			}
		} else {
			seen[k] = s
		}
	}
	out := make([]Source, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	return out
}
