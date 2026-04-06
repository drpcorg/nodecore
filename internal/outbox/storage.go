package outbox

import (
	"context"
	"fmt"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/rs/zerolog/log"
	"time"
)

type outboxItem = map[string][]byte

type outboxStorer interface {
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, cursor, limit int64) ([]outboxItem, error)
}

type outboxStorage struct {
	storage outboxStorer
}

func NewOutboxStorage(
	conf *config.StatsConfig,
	storageRegistry *storages.StorageRegistry,
) (*outboxStorage, error) {
	storage, ok := storageRegistry.Get(conf.StorageType)
	if !ok {
		return nil, fmt.Errorf("unknown storage type: %s", conf.StorageType)
	}

	switch storage.(type) {
	case *storages.PostgresStorage:
		pg, err := newPostgresClient(storage.(*storages.PostgresStorage))
		return &outboxStorage{storage: pg}, err
	case *storages.RedisStorage:
		r, err := newRedisClient(storage.(*storages.RedisStorage))
		return &outboxStorage{storage: r}, err
	default:
		log.Warn().Msg("no-op stats storage in use")
		return &outboxStorage{storage: newNoopStorage()}, nil
	}
}

func (o *outboxStorage) Set(ctx context.Context, k string, v []byte, ttl time.Duration) error {
	return o.storage.Set(ctx, k, v, ttl)
}

func (o *outboxStorage) Delete(ctx context.Context, k string) error {
	return o.storage.Delete(ctx, k)
}

const defaultLimit = 5

func (o *outboxStorage) List(ctx context.Context, cur, limit int64) ([]outboxItem, error) {
	if limit == 0 {
		limit = defaultLimit
	}
	return o.storage.List(ctx, cur, limit)
}
