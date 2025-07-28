package news

import (
	"context"
	"fmt"
	"github.com/mohammad-safakhou/newser/models"
	"github.com/mohammad-safakhou/newser/news/newsapi"
	"github.com/mohammad-safakhou/newser/provider"
	"log"
)

// Retriever handles fetching and displaying news for topics
type Retriever struct {
	ProviderClient provider.Provider
	NewsClient     newsapi.NewsAPI
}

// NewRetriever creates a new news retriever
func NewRetriever(provider provider.Provider, newsClient newsapi.NewsAPI) Retriever {
	return Retriever{
		ProviderClient: provider,
		NewsClient:     newsClient,
	}
}

// TriggerNewsUpdate fetches and prints news for a topic
func (r *Retriever) TriggerNewsUpdate(ctx context.Context, topic models.Topic) (string, error) {
	log.Printf("Fetching news for topic: %s", topic.Title)
	articles, err := r.NewsClient.FetchNewsForTopic(topic)
	if err != nil {
		log.Printf("Error fetching news for %s: %v", topic.Title, err)
		return "", fmt.Errorf("failed to fetch news for topic %s: %w", topic.Title, err)
	}

	news, err := r.ProviderClient.SummarizeNews(ctx, topic, articles)
	if err != nil {
		log.Printf("Error fetching news for %s: %v", topic.Title, err)
		return "", fmt.Errorf("failed to summarize news for topic %s: %w", topic.Title, err)
	}

	fmt.Printf("\n=== NEWS UPDATE: %s ===\n", topic.Title)
	fmt.Printf("- %s\n\n", news)

	return news, nil
}
