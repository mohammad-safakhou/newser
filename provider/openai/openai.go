package openai_provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/models"
	"github.com/mohammad-safakhou/newser/news/newsapi"
)

const (
	openaiAPIURL = "https://api.openai.com/v1/chat/completions"
)

// client implements the client interface using OpenAI's API
type client struct {
	apiKey          string
	completionModel string
	embeddingModel  string
	temperature     float64
	maxTokens       int
	httpClient      *http.Client
}

// Message represents a message in a conversation
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// request represents a request to the OpenAI API
type request struct {
	Model       string    `json:"completionModel"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// response represents a response from the OpenAI API
type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// NewOpenAIClient creates a new OpenAI client
func NewOpenAIClient(apiKey, completionModel string, embeddingModel string, temperature float64, maxTokens int, timeout time.Duration) *client {
	return &client{
		apiKey:          apiKey,
		completionModel: completionModel,
		embeddingModel:  embeddingModel,
		temperature:     temperature,
		maxTokens:       maxTokens,
		httpClient:      &http.Client{Timeout: timeout},
	}
}

// CreateEmbedding generates an embedding for the given texts using OpenAI's API
func (c *client) CreateEmbedding(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Prepare the request body
	requestBody := map[string]interface{}{
		"model": c.embeddingModel,
		"input": texts,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status: %d", resp.StatusCode)
	}

	var openaiResp struct {
		Data []struct {
			Object    string    `json:"object"`
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	vecs := make([][]float32, len(openaiResp.Data))
	for i, d := range openaiResp.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

// GeneralMessage implements the client interface
func (c *client) GeneralMessage(ctx context.Context, message string, topic models.Topic) (string, models.Topic, error) {
	systemPrompt := fmt.Sprintf(`
You are a conversational topic management assistant that helps users configure news subscription topics. Your role is to fill and modify Topic objects through natural conversation.

RULES:
1. Be conversational and friendly, not technical
2. Never mention JSON, objects, or technical terms to the user
3. Guide users to complete fields naturally
4. If they agree to launch it, they should have filled cron_spec
5. Always respond with both updated topic data AND a conversational message

RESPONSE FORMAT:
Respond ONLY with valid JSON in the following format:
{
  "topic": {
    "state": "state_value",
    "subtopics": ["array", "of", "strings"],
    "key_concepts": ["array", "of", "strings"], 
    "related_topics": ["array", "of", "strings"],
    "preferences": {"key": "value"},
    "cron_spec": "cron_expression_if_set"
  },
  "message": "Your conversational response to the user"
}
Do not include any other text or explanation.

Remember: Be helpful, conversational, and guide users naturally toward completing their topic configuration.
`)
	userPrompt := fmt.Sprintf(`
CONTEXT HISTORY:
[%s]

CURRENT TOPIC:
Title: "%s"
State: "%s"
Subtopics: %v
Key Concepts: %v
Related Topics: %v
Preferences: %v
Cron Spec: "%s"

USER MESSAGE: "%s"
`, strings.Join(topic.History, ","), topic.Title, topic.State, topic.Subtopics, topic.KeyConcepts, topic.RelatedTopics, topic.Preferences, topic.CronSpec, message)

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	responseStr, err := c.sendRequest(ctx, messages)
	if err != nil {
		return "", models.Topic{}, err
	}
	type response struct {
		Topic struct {
			State         string                 `json:"state"`
			Subtopics     []string               `json:"subtopics"`
			KeyConcepts   []string               `json:"key_concepts"`
			RelatedTopics []string               `json:"related_topics"`
			Preferences   map[string]interface{} `json:"preferences"`
			CronSpec      string                 `json:"cron_spec"`
		} `json:"topic"`
		Message string `json:"message"`
	}
	var resp response
	if err := json.Unmarshal([]byte(responseStr), &resp); err != nil {
		return "", models.Topic{}, fmt.Errorf("failed to parse analysis: %w", err)
	}

	topic.State = models.TopicState(resp.Topic.State)
	topic.Subtopics = resp.Topic.Subtopics
	topic.KeyConcepts = resp.Topic.KeyConcepts
	topic.RelatedTopics = resp.Topic.RelatedTopics
	topic.Preferences = resp.Topic.Preferences
	topic.CronSpec = resp.Topic.CronSpec

	return resp.Message, topic, nil
}

// SummarizeNews generates a summary of news based on the given articles and topic
func (c *client) SummarizeNews(ctx context.Context, topic models.Topic, articles []newsapi.Article) (string, error) {

	// Prepare the articles for the LLM
	var articleSummaries []string
	for _, article := range articles {
		articleSummaries = append(articleSummaries, fmt.Sprintf(
			"Title: %s\nAuthor: %s\nSource: %s\nPublished At: %s\nDescription: %s\nURL: %s",
			article.Title, article.Author, article.Source.Name, article.PublishedAt.Format(time.RFC1123), article.Description, article.URL,
		))
	}

	// Build the LLM prompt
	prompt := fmt.Sprintf(`You are a helpful assistant. Summarize the following news articles related to the topic "%s". Provide a concise summary in Markdown format, highlighting the key points and insights from the articles.

Topic: %s

Articles:
%s

Respond with a Markdown summary only.`, topic.Title, topic.Title, strings.Join(articleSummaries, "\n\n"))

	messages := []Message{
		{Role: "user", Content: prompt},
	}

	responseStr, err := c.sendRequest(ctx, messages)
	if err != nil {
		return "", err
	}
	return responseStr, nil
}

// GenerateNews generates news based on the given topic
func (c *client) GenerateNews(ctx context.Context, topic models.Topic) (string, error) {
	systemPrompt := fmt.Sprintf(`	
You are a news generation assistant that creates news articles based on user-defined topics. Your role is to generate engaging and informative news content.
RULES:
1. Be creative and engaging, not technical
2. Never mention JSON, objects, or technical terms to the user
3. Generate news articles that are relevant to the topic
4. Always respond with a well-structured news article
RESPONSE FORMAT:
Respond ONLY with a well-structured news article in plain text. Do not include any other text
or explanation.
Remember: Be creative, engaging, and generate news articles that captivate the reader.
`)
	userPrompt := fmt.Sprintf(`
CURRENT TOPIC:
Title: "%s"
State: "%s"
Subtopics: %v
Key Concepts: %v
Related Topics: %v
Preferences: %v
`, topic.Title, topic.State, topic.Subtopics, topic.KeyConcepts, topic.RelatedTopics, topic.Preferences)

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	responseStr, err := c.sendRequest(ctx, messages)
	if err != nil {
		return "", err
	}
	return responseStr, nil
}

// sendRequest sends a request to the OpenAI API
func (c *client) sendRequest(ctx context.Context, messages []Message) (string, error) {
	requestBody := request{
		Model:       c.completionModel,
		Messages:    messages,
		Temperature: c.temperature,
		MaxTokens:   c.maxTokens,
	}

	fmt.Printf("Sending request to OpenAI API with completionModel: %s, temperature: %f, max_tokens: %d\nMessage: %v\n", c.completionModel, c.temperature, c.maxTokens, messages)

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openaiAPIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status: %d", resp.StatusCode)
	}

	// Log the response status and body for debugging
	fmt.Printf("Received response from OpenAI API: %s\n", resp.Status)
	// Decode the response
	if resp.Body == nil {
		return "", fmt.Errorf("empty response body")
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	fmt.Printf("Response body: %s\n", buf.String())

	var openaiResp response
	if err := json.Unmarshal(buf.Bytes(), &openaiResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(openaiResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return openaiResp.Choices[0].Message.Content, nil
}
