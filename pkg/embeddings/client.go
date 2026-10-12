package embeddings

import "context"

// Client is the common interface for generating vector embeddings.
type Client interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	IsHealthy(ctx context.Context) bool
}
