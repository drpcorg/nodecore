package caches

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/hashicorp/golang-lru/v2"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

var (
	ErrCacheNotFound  = errors.New("not found in cache")
	ErrNotImplemented = errors.New("not implemented")
)

type CacheConnector interface {
	Id() string
	Store(ctx context.Context, key string, object string, ttl time.Duration) error
	Receive(ctx context.Context, key string) ([]byte, error)
	Initialize() error
	OutboxStore(ctx context.Context, key string, value []byte, ttl time.Duration) error
	OutboxRemove(ctx context.Context, key string) error
	OutboxList(ctx context.Context, cursor, limit int64) ([]outboxItem, error)
}

type cacheItem struct {
	object   string
	expireAt *time.Time
}

type InMemoryConnector struct {
	id                    string
	cache                 *lru.Cache[string, cacheItem]
	expiredRemoveInterval time.Duration
}

type outboxItem = map[string][]byte

func (i *InMemoryConnector) Initialize() error {
	go i.removeExpired()
	return nil
}

func NewInMemoryConnector(id string, config *config.MemoryCacheConnectorConfig) (*InMemoryConnector, error) {
	cache, err := lru.New[string, cacheItem](config.MaxItems)
	if err != nil {
		log.Error().Err(err).Msgf("couldn't create a memory cache connector with id %s", id)
		return nil, fmt.Errorf("couldn't create a memory cache connector with id %s, reason - %s", id, err.Error())
	}

	connector := &InMemoryConnector{
		id:                    id,
		cache:                 cache,
		expiredRemoveInterval: config.ExpiredRemoveInterval,
	}

	return connector, nil
}

func (i *InMemoryConnector) Id() string {
	return i.id
}

func (i *InMemoryConnector) Store(_ context.Context, key string, object string, ttl time.Duration) error {
	var expiredAt *time.Time
	if ttl > 0 {
		expiredAt = lo.ToPtr(time.Now().Add(ttl))
	}

	i.cache.Add(key, cacheItem{object: object, expireAt: expiredAt})

	return nil
}

func (i *InMemoryConnector) Receive(_ context.Context, key string) ([]byte, error) {
	item, ok := i.cache.Get(key)
	if !ok {
		return nil, ErrCacheNotFound
	}
	return []byte(item.object), nil
}

func (i *InMemoryConnector) OutboxStore(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return ErrNotImplemented
}

func (i *InMemoryConnector) OutboxRemove(_ context.Context, _ string) error {
	return ErrNotImplemented
}

func (i *InMemoryConnector) OutboxList(_ context.Context, _, _ int64) ([]outboxItem, error) {
	return nil, ErrNotImplemented
}

func (i *InMemoryConnector) removeExpired() {
	for {
		<-time.After(i.expiredRemoveInterval)

		for _, key := range i.cache.Keys() {
			if item, ok := i.cache.Peek(key); ok {
				if item.expireAt != nil && time.Now().After(*item.expireAt) {
					i.cache.Remove(key)
				}
			}
		}
	}
}

var _ CacheConnector = (*InMemoryConnector)(nil)
