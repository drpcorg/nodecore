package outbox

import (
	"context"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/rs/zerolog/log"
	"time"
)

type Storer interface {
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, cursor, limit int64) ([]Item, error)
}

type Item struct {
	Key   string
	Value []byte
}

type outboxStorer interface {
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, cursor, limit int64) ([]Item, error)
}

type outboxStorage struct {
	storage outboxStorer
}

func NewOutboxStorage(
	conf *config.StatsConfig,
	storageRegistry *storages.StorageRegistry,
) (*outboxStorage, error) {
	if conf == nil {
		return &outboxStorage{storage: newNoopStorage()}, nil
	}
	if !conf.Enabled {
		return &outboxStorage{storage: newNoopStorage()}, nil
	}
	storage, _ := storageRegistry.Get(conf.StorageType)
	switch storage := storage.(type) {
	case *storages.PostgresStorage:
		pg, err := newPostgresClient(storage.Postgres)
		return &outboxStorage{storage: pg}, err
	case *storages.RedisStorage:
		r, err := newRedisClient(storage.Redis)
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

func (o *outboxStorage) List(ctx context.Context, cur, limit int64) ([]Item, error) {
	if limit == 0 {
		limit = defaultLimit
	}
	return o.storage.List(ctx, cur, limit)
}
