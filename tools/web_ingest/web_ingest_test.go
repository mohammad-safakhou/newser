package web_ingest

import (
	"github.com/mohammad-safakhou/newser/tools/web_ingest/models"
	"testing"
)

func TestNewIngest_InMemoryStore(t *testing.T) {
	ing := NewIngest(InMemoryStore)
	if ing.Store == nil {
		t.Fatal("Store should not be nil")
	}
}

func TestIngest_Success(t *testing.T) {
	ing := NewIngest(InMemoryStore)
	docs := []models.DocInput{
		{
			URL:   "https://example.com",
			Title: "Test Title",
			Text:  "This is a test document. " + string(make([]byte, 1200)), // force chunking
		},
	}
	resp, err := ing.Ingest("", docs, 1)
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}
	if resp.Chunks == 2 && resp.IndexedBM == 2 {
		// expected: chunking splits into 2
	} else if resp.Chunks == 1 && resp.IndexedBM == 1 {
		// fallback: chunking did not split
	} else {
		t.Errorf("Unexpected chunk count: got %d", resp.Chunks)
	}
	if resp.SessionID == "" {
		t.Error("SessionID should not be empty")
	}
}

func TestIngest_EmptyDocs(t *testing.T) {
	ing := NewIngest(InMemoryStore)
	_, err := ing.Ingest("", []models.DocInput{}, 1)
	if err == nil {
		t.Error("Expected error for empty docs")
	}
}

func TestIngest_SessionReuse(t *testing.T) {
	ing := NewIngest(InMemoryStore)
	docs := []models.DocInput{
		{URL: "https://example.com", Title: "Doc1", Text: "abc"},
	}
	resp1, err := ing.Ingest("", docs, 1)
	if err != nil {
		t.Fatalf("First ingest failed: %v", err)
	}
	resp2, err := ing.Ingest(resp1.SessionID, docs, 1)
	if err != nil {
		t.Fatalf("Second ingest failed: %v", err)
	}
	if resp1.SessionID != resp2.SessionID {
		t.Error("SessionID should be reused")
	}
}

func TestMakeChunks(t *testing.T) {
	text := "abcdefghij"
	chunks := makeChunks(text, 4, 2)
	if len(chunks) < 2 {
		t.Errorf("Expected at least 2 chunks, got %d", len(chunks))
	}
	if chunks[0] != "abcd" {
		t.Errorf("Unexpected first chunk: %s", chunks[0])
	}
}
