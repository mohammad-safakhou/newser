package newsapi

import (
	"encoding/json"
	"fmt"
	"github.com/mohammad-safakhou/newser/models"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Article struct {
	Source struct {
		Name string `json:"name"`
	} `json:"source"`
	Author      string    `json:"author"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
}

type response struct {
	Status       string    `json:"status"`
	TotalResults int       `json:"totalResults"`
	Articles     []Article `json:"articles"`
}

func buildQueryFromTopic(topic models.Topic) string {
	var terms []string
	terms = append(terms, topic.Title)
	terms = append(terms, topic.Subtopics...)
	terms = append(terms, topic.KeyConcepts...)
	terms = append(terms, topic.RelatedTopics...)

	// Wrap each term in quotes and join with OR
	for i, t := range terms {
		terms[i] = fmt.Sprintf(`"%s"`, t)
	}

	return strings.Join(terms, " OR ")
}

type NewsAPI struct {
	APIKey   string
	Endpoint string
}

func (n NewsAPI) FetchNewsForTopic(topic models.Topic) ([]Article, error) {
	query := buildQueryFromTopic(topic)

	params := url.Values{}
	params.Add("q", query)

	// Handle preferences if present
	if lang, ok := topic.Preferences["language"].(string); ok {
		params.Add("language", lang)
	}
	if from, ok := topic.Preferences["from"].(string); ok {
		params.Add("from", from) // format: YYYY-MM-DD
	}
	if to, ok := topic.Preferences["to"].(string); ok {
		params.Add("to", to)
	}
	if sortBy, ok := topic.Preferences["sort_by"].(string); ok {
		params.Add("sortBy", sortBy) // options: relevancy, popularity, publishedAt
	}
	if domains, ok := topic.Preferences["domains"].(string); ok {
		params.Add("domains", domains) // e.g., "bbc.co.uk,techcrunch.com"
	}

	params.Add("apiKey", n.APIKey)

	reqURL := fmt.Sprintf("%s?%s", n.Endpoint, params.Encode())
	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch news: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("newsapi error: %s", resp.Status)
	}

	var result response
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Articles, nil
}
