package serper

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mohammad-safakhou/newser/tools/web_search/models"
	"github.com/mohammad-safakhou/newser/utils"
	"net/http"
	"strings"
)

type Search struct {
	ApiKey string
}

func (s Search) Discover(ctx context.Context, q string, k int, sites []string, recency int) ([]models.Result, error) {
	// https://serper.dev/ docs
	payload := map[string]any{"q": q, "num": k}
	if len(sites) > 0 {
		payload["site"] = strings.Join(sites, " OR ")
	}
	if recency > 0 {
		payload["tbs"] = fmt.Sprintf("qdr:%d", recency)
	} // quick & dirty

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://google.serper.dev/search", strings.NewReader(string(body)))
	req.Header.Set("X-API-KEY", s.ApiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	var out []models.Result
	if items, ok := raw["organic"].([]any); ok {
		for i, it := range items {
			if i >= k {
				break
			}
			m := it.(map[string]any)
			out = append(out, models.Result{
				Title: utils.Str(m["title"]), URL: utils.Str(m["link"]), Snippet: utils.Str(m["snippet"]),
			})
		}
	}
	return out, nil
}
