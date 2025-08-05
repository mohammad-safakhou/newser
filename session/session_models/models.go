package session_models

import "time"

type DocChunk struct {
	DocID        string
	URL          string
	Title        string
	Text         string
	PublishedAt  string
	ContentHash  string
	IngestedAt   time.Time
	ChunkIndex   int
	SourceSessID string
}

type DocInput struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	PublishedAt string `json:"published_at,omitempty"`
}

type SearchHit struct {
	DocID   string  `json:"doc_id"`
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
	Rank    int     `json:"rank"`
}
