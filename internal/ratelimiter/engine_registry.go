package ratelimiter

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
)

type RateLimitEngineRegistry struct {
	engines map[string]RateLimitEngine
}

func NewRateLimitEngineRegistry(storageRegistry *storages.StorageRegistry, engines []config.RateLimitEngine) (*RateLimitEngineRegistry, error) {
	engineMap := make(map[string]RateLimitEngine)
	for _, engine := range engines {
		switch engine.Type {
		case "redis":
			storage, ok := storageRegistry.Get(engine.Redis.StorageName)
			if !ok {
				return nil, fmt.Errorf("redis storage with name %s not found", engine.Redis.StorageName)
			}
			redisStorage, ok := storage.(*storages.RedisStorage)
			if !ok {
				return nil, fmt.Errorf("redis storage with name %s is not a redis storage", engine.Redis.StorageName)
			}
			engineMap[engine.Name] = NewRateLimitRedisEngine(engine.Name, redisStorage.Redis)
		case "memory":
			engineMap[engine.Name] = NewRateLimitMemoryEngine()
		default:
			return nil, fmt.Errorf("unknown engine type %s", engine.Type)
		}

	}
	return &RateLimitEngineRegistry{
		engines: engineMap,
	}, nil
}

func (r *RateLimitEngineRegistry) Get(name string) (RateLimitEngine, bool) {
	engine, ok := r.engines[name]
	return engine, ok
}
