package engine

import (
	"context"
	"errors"
	"fmt"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/models"
	"github.com/mohammad-safakhou/newser/news"
	"github.com/mohammad-safakhou/newser/news/newsapi"
	"github.com/mohammad-safakhou/newser/provider"
	"github.com/mohammad-safakhou/newser/repository"
	"net/http"
	"time"
)

type Server struct {
	Repo     repository.TopicRepository
	Provider provider.Provider
	News     news.Retriever
}

func Start() {
	config.LoadConfig("", false)

	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	ctx := context.Background()

	// Initialize repository and provider
	repo, err := repository.NewTopicRepository(ctx, repository.RepoTypeRedis)
	if err != nil {
		e.Logger.Fatal("Failed to initialize repository:", err)
		return
	}
	prov, err := provider.NewProvider(provider.OpenAI)
	if err != nil {
		e.Logger.Fatal("Failed to initialize provider:", err)
		return
	}

	// Initialize news retriever
	newsClient := news.NewRetriever(prov, newsapi.NewsAPI{
		APIKey:   config.AppConfig.NewsApi.APIKey,
		Endpoint: config.AppConfig.NewsApi.Endpoint,
	})

	server := &Server{
		Repo:     repo,
		Provider: prov,
		News:     newsClient,
	}

	// Define routes
	e.POST("/topics", server.CreateTopic)
	e.GET("/topics", server.GetAllTopics)
	e.GET("/topics/:title", server.GetTopic)
	e.PUT("/topics/:title", server.ModifyTopic)
	e.GET("/topics/:title/generate-news", server.GenerateNews)

	// Start server
	e.Logger.Fatal(e.Start(fmt.Sprintf(":%s", config.AppConfig.General.Listen)))
}

type CreateTopicRequest struct {
	Title string `json:"title"`
}

// CreateTopic creates a new topic
func (s *Server) CreateTopic(c echo.Context) error {
	var req CreateTopicRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}

	if err := s.Repo.SaveTopic(context.Background(), models.Topic{
		State:     models.TopicStateInitial,
		Title:     req.Title,
		CreatedAt: time.Now(),
	}); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save topic", "details": err.Error()})
	}

	return c.JSON(http.StatusCreated, nil)
}

// GetAllTopics retrieves all topics
func (s *Server) GetAllTopics(c echo.Context) error {
	topics, err := s.Repo.GetAllTopics(context.Background())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to retrieve topics", "details": err.Error()})
	}

	return c.JSON(http.StatusOK, topics)
}

// GetTopic retrieves a specific topic by title
func (s *Server) GetTopic(c echo.Context) error {
	title := c.Param("title")
	topic, err := s.Repo.GetTopic(context.Background(), title)
	if err != nil {
		if errors.Is(err, models.ErrTopicNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "topic not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to retrieve topic", "details": err.Error()})
	}

	return c.JSON(http.StatusOK, topic)
}

// ModifyTopic modifies an existing topic
func (s *Server) ModifyTopic(c echo.Context) error {
	title := c.Param("title")
	var request struct {
		Message string `json:"message"`
	}
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request", "details": err.Error()})
	}

	topic, err := s.Repo.GetTopic(context.Background(), title)
	if err != nil {
		if errors.Is(err, models.ErrTopicNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "topic not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to retrieve topic", "details": err.Error()})
	}

	// Use the provider to interact with the LLM
	response, newTopic, err := s.Provider.GeneralMessage(context.Background(), request.Message, topic)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to chat with LLM", "details": err.Error()})
	}

	newTopic.UpdatedAt = time.Now()
	newTopic.History = append(newTopic.History, fmt.Sprintf("User: %s\nLLM: %s", request.Message, response))

	// Update the topic in the repository
	if err := s.Repo.SaveTopic(context.Background(), newTopic); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update topic", "details": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]string{"response": response})
}

// GenerateNews generates news for a topic
func (s *Server) GenerateNews(c echo.Context) error {
	title := c.Param("title")
	topic, err := s.Repo.GetTopic(context.Background(), title)
	if err != nil {
		if errors.Is(err, models.ErrTopicNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "topic not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to retrieve topic", "details": err.Error()})
	}

	newsRes, err := s.News.TriggerNewsUpdate(context.Background(), topic)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to generate news", "details": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]string{"news": newsRes})
}
