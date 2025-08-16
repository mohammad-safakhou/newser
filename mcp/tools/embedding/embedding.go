package embedding

import (
	"context"
	"errors"

	"github.com/mohammad-safakhou/newser/provider"
)

// Embedding wraps a provider that can create embeddings.
// It is *stateless*: no caching, no persistence, no global lookups.
type Embedding struct {
	provider provider.Provider
}

// NewEmbedding returns an Embedding bound to the given provider.
func NewEmbedding(p provider.Provider) *Embedding { return &Embedding{provider: p} }

// EmbedMany converts texts into vectors via the provider.
// Returns (nil, nil) if texts is empty. Errors if provider is nil.
func (e Embedding) EmbedMany(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if e.provider == nil {
		return nil, errors.New("embedding: nil provider")
	}
	return e.provider.CreateEmbedding(ctx, texts)
}
