# Newser

Getting personalized news all the time, not as a SPAM :)

## Overview

Newser is an intelligent news aggregation system that delivers personalized news summaries based on user-defined topics.
The system uses AI-powered topic analysis and conversation management to create customized news feeds that are delivered
on a schedule, ensuring users get relevant information without spam.

## Features

- **Intelligent Topic Management**: Create and configure news topics with AI assistance
- **Conversational AI Interface**: Interact with LLM to refine and modify topics naturally
- **Automated News Fetching**: Schedule-based news collection using cron expressions
- **AI-Powered Summarization**: Generate comprehensive markdown summaries of collected articles
- **Flexible Scheduling**: Configurable delivery schedules for each topic
- **State Management**: Track topic configuration progress and conversation history
- **RESTful API**: Complete API for managing topics and interactions

## Architecture

The project follows a clean architecture pattern with the following components:

- **Models**: Core data structures (Topic, Article, etc.)
- **Repository**: Data persistence layer with Redis support
- **Provider**: AI/LLM integration (OpenAI, etc.)
- **Conversation**: Conversation state management
- **API**: RESTful endpoints using Echo framework

## Getting Started

### Prerequisites

- Go 1.19 or higher
- Redis server
- OpenAI API key (For now only OpenAI is supported)
- NewsApi API key (for news fetching)

### Installation

1. Clone the repository:

```bash
git clone https://github.com/mohammad-safakhou/newser.git
cd newser
```

2. Install dependencies:
```bash
go mod tidy
```

3. Set up environment variables:

```bash
export NEWSER_OPENAI_API_KEY="your-openai-api-key"
export NEWSER_NEWSAPI_API_KEY="your-newsapi-key"

OR

Update the /config/config.go file with your API keys and Redis URL.
```

4. Run Redis server:

```bash
redis-server
```

5. Start the application:

```bash
go run main.go
```

## API Endpoints

### Topic Management

- `POST /topics` - Create a new topic
- `GET /topics` - Get all topics
- `GET /topics/:title` - Get a specific topic
- `PUT /topics/:title` - Update a topic
- `DELETE /topics/:title` - Delete a topic

### AI Interaction

- `POST /topics/:title/complete` - Use AI to complete topic structure
- `POST /topics/:title/chat` - Chat with AI to modify topic

### News Processing

- `POST /topics/:title/fetch` - Fetch news for a topic
- `POST /topics/:title/summarize` - Generate markdown summary

## Usage Example

### 1. Create a Topic

```bash
curl -X POST http://localhost:8080/topics \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Artificial Intelligence",
    "state": "initial"
  }'
```

### 2. Complete Topic with AI

```bash
curl -X POST http://localhost:8080/topics/Artificial%20Intelligence/complete
```

### 3. Chat with AI to Refine Topic

```bash
curl -X POST http://localhost:8080/topics/Artificial%20Intelligence/chat \
  -H "Content-Type: application/json" \
  -d '{
    "message": "I want to focus more on machine learning and deep learning. Set up daily updates at 9 AM."
  }'
```

### 4. Launch Topic

Once configured, the system will automatically fetch news based on the cron specification and generate summaries.

## Data Models

### Topic

```go
type Topic struct {
    State         TopicState             `json:"state"`
    History       []string               `json:"history"`
    Title         string                 `json:"title"`
    Subtopics     []string               `json:"subtopics"`
    KeyConcepts   []string               `json:"key_concepts"`
    RelatedTopics []string               `json:"related_topics"`
    Preferences   map[string]interface{} `json:"preferences"`
    CronSpec      string                 `json:"cron_spec"`
    CreatedAt     time.Time              `json:"created_at"`
    UpdatedAt     time.Time              `json:"updated_at"`
}
```

### Article

```go
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
```

## Cron Schedule Examples

- **Daily at 9 AM**: `0 9 * * *`
- **Every Monday at 9 AM**: `0 9 * * 1`
- **Twice daily (9 AM & 6 PM)**: `0 9,18 * * *`
- **Every hour**: `0 * * * *`

## Configuration

### Environment Variables

- `OPENAI_API_KEY` - OpenAI API key for LLM integration
- `REDIS_URL` - Redis server connection string
- `PORT` - Server port (default: 8080)

### Provider Configuration

The system supports multiple LLM providers. Configure in your initialization code:

```go
providerClient := openai_provider.NewOpenAIClient(
    apiKey,
    "gpt-3.5-turbo",  // or "gpt-4"
    0.7,              // temperature
    2000,             // max_tokens
    30*time.Second,   // timeout
)
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Roadmap

- [ ] Support for multiple news sources
- [ ] Web UI interface
- [ ] Email delivery integration
- [ ] Advanced filtering and categorization
- [ ] User authentication and multi-tenancy
- [ ] Analytics and usage statistics
- [ ] Mobile app support

## Support

For support and questions, please open an issue in the GitHub repository.


This README provides a comprehensive overview of your Newser project, including setup instructions, API documentation, usage examples, and technical details that would help users understand and use your system effectively.
