package provider

import (
	"context"
	"errors"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/models"
	"github.com/mohammad-safakhou/newser/news/newsapi"
	openai_provider "github.com/mohammad-safakhou/newser/provider/openai"
)

// Client represents different LLM providers
type Client string

const (
	OpenAI    Client = "openai"
	Anthropic Client = "anthropic"
	Gemini    Client = "gemini"
)

// Provider is the interface that all LLM implementations must satisfy
type Provider interface {
	GeneralMessage(ctx context.Context, message string, topic models.Topic) (string, models.Topic, error)
	GenerateNews(ctx context.Context, topic models.Topic) (string, error)
	SummarizeNews(ctx context.Context, topic models.Topic, articles []newsapi.Article) (string, error)
	CreateEmbedding(ctx context.Context, texts []string) ([][]float32, error)
}

// NewProvider creates a new LLM client based on the provided configuration
func NewProvider(client Client) (Provider, error) {
	switch client {
	case OpenAI:
		return openai_provider.NewOpenAIClient(
			config.AppConfig.Providers.OpenAi.APIKey,
			config.AppConfig.Providers.OpenAi.CompletionModel,
			config.AppConfig.Providers.OpenAi.EmbeddingModel,
			config.AppConfig.Providers.OpenAi.Temperature,
			config.AppConfig.Providers.OpenAi.MaxTokens,
			config.AppConfig.Providers.OpenAi.Timeout,
		), nil
	case Anthropic:
		return nil, errors.New("anthropic client not implemented yet")
	case Gemini:
		return nil, errors.New("gemini client not implemented yet")
	default:
		return nil, errors.New("unsupported LLM provider")
	}
}
