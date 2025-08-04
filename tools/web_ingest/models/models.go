package models

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

type IngestResponse struct {
	SessionID string `json:"session_id"`
	Chunks    int    `json:"chunks"`
	IndexedBM int    `json:"indexed_bm25"`
}
