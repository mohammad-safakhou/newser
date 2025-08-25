package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	// Deprecated: legacy root config removed in favor of internal/server
	// "github.com/labstack/echo/v4/middleware"
	"github.com/mohammad-safakhou/newser/models"
	"github.com/mohammad-safakhou/newser/news"
	// "github.com/mohammad-safakhou/newser/news/newsapi"
	"github.com/mohammad-safakhou/newser/provider"
	"github.com/mohammad-safakhou/newser/repository"
)

// Deprecated: use internal/server instead. Leaving stub to avoid breaking imports.
type Server struct {
	Repo     repository.TopicRepository
	Provider provider.Provider
	News     news.Retriever
}

func Start() {
	// Deprecated path; do nothing to avoid conflicting servers.
	fmt.Println("engine.Start is deprecated; use cmd/newser serve")
}

type CreateTopicRequest struct {
	Title string `json:"title"`
}

func (s *Server) CreateTopic(c echo.Context) error {
	var req CreateTopicRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	if err := s.Repo.SaveTopic(context.Background(), models.Topic{State: models.TopicStateInitial, Title: req.Title, CreatedAt: time.Now()}); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save topic", "details": err.Error()})
	}
	return c.JSON(http.StatusCreated, nil)
}

func (s *Server) GetAllTopics(c echo.Context) error {
	topics, err := s.Repo.GetAllTopics(context.Background())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to retrieve topics", "details": err.Error()})
	}
	return c.JSON(http.StatusOK, topics)
}

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

func (s *Server) ModifyTopic(c echo.Context) error {
	title := c.Param("title")
	var req struct {
		Message string `json:"message"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request", "details": err.Error()})
	}
	topic, err := s.Repo.GetTopic(context.Background(), title)
	if err != nil {
		if errors.Is(err, models.ErrTopicNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "topic not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to retrieve topic", "details": err.Error()})
	}
	response, newTopic, err := s.Provider.GeneralMessage(context.Background(), req.Message, topic)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to chat with LLM", "details": err.Error()})
	}
	newTopic.UpdatedAt = time.Now()
	newTopic.History = append(newTopic.History, fmt.Sprintf("User: %s\nLLM: %s", req.Message, response))
	if err := s.Repo.SaveTopic(context.Background(), newTopic); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update topic", "details": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"response": response})
}

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
