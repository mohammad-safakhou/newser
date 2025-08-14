package brave

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mohammad-safakhou/newser/tools/web_search/models"
	"github.com/mohammad-safakhou/newser/utils"
)

type Search struct {
	ApiKey string
}

func (s Search) Discover(ctx context.Context, q string, k int, sites []string, recency int) ([]models.Result, error) {
	// https://api.search.brave.com/app/documentation/web-search
	url := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d", utils.UrlQuery(q), k)
	if len(sites) > 0 {
		url += "&source=web&freshness=&safesearch=off"
	} // sites filter omitted for brevity
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", s.ApiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw struct {
		Web struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Snippet string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var out []models.Result
	for i, r := range raw.Web.Results {
		if i >= k {
			break
		}
		out = append(out, models.Result{Title: r.Title, URL: r.URL, Snippet: r.Snippet})
	}
	return out, nil
}
