package storages

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
)

type Storage interface {
	storage() string
}

type StorageRegistry struct {
	storages map[string]Storage
}

func NewStorageRegistry(storages []config.AppStorageConfig) (*StorageRegistry, error) {
	storageMap := make(map[string]Storage)
	for _, storage := range storages {
		if storage.Redis != nil {
			redisStorage, err := NewRedisStorage(storage.Name, storage.Redis)
			if err != nil {
				return nil, fmt.Errorf("couldn't create a redis storage with name %s, reason - %s", storage.Name, err.Error())
			}
			storageMap[storage.Name] = redisStorage
		} else if storage.Postgres != nil {
			postgresStorage, err := NewPostgresStorage(storage.Name, storage.Postgres)
			if err != nil {
				return nil, fmt.Errorf("couldn't create a postgres storage with name %s, reason - %s", storage.Name, err.Error())
			}
			storageMap[storage.Name] = postgresStorage
		}
	}
	return &StorageRegistry{
		storages: storageMap,
	}, nil
}

func (s *StorageRegistry) Get(name string) (Storage, bool) {
	storage, ok := s.storages[name]
	return storage, ok
}
