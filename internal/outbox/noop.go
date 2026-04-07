package outbox

import (
	"context"
	"time"
)

type noopStorage struct{}

func newNoopStorage() *noopStorage {
	return &noopStorage{}
}

func (n *noopStorage) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}

func (n *noopStorage) Delete(_ context.Context, _ string) error {
	return nil
}

func (n *noopStorage) List(_ context.Context, _, _ int64) ([]Item, error) {
	return []Item{}, nil
}
