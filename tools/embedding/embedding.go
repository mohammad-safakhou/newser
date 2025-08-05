package embedding

import (
	"context"
	"github.com/mohammad-safakhou/newser/provider"
	"github.com/mohammad-safakhou/newser/session/session_models"
)

type Embedding struct {
	provider provider.Provider
}

type EmbedVec struct {
	DocID string
	Vec   []float32
}

func NewEmbedding(provider provider.Provider) *Embedding {
	return &Embedding{
		provider: provider,
	}
}

func (e Embedding) MapChunksToTexts(chs []session_models.DocChunk) []string {
	out := make([]string, len(chs))
	for i, c := range chs {
		out[i] = c.Text
	}
	return out
}

func (e Embedding) EmbedMany(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	vecs, err := e.provider.CreateEmbedding(ctx, texts)
	if err != nil {
		return nil, err
	}

	return vecs, nil
}
