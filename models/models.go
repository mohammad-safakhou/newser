package models

import (
	"errors"
	"time"
)

// ErrTopicNotFound is returned when a topic is not found
var ErrTopicNotFound = errors.New("topic not found")

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

type TopicState string

const (
	TopicStateInitial    TopicState = "initial"
	TopicStateInProgress TopicState = "in_progress"
	TopicStateCompleted  TopicState = "completed"
)
