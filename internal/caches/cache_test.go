package caches_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/dsheltie/internal/caches"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestInMemoryCacheNotFoundThenError(t *testing.T) {
	inMemory := caches.NewInMemoryConnector("id", &config.MemoryCacheConnectorConfig{MaxItems: 1000, ExpiredRemoveInterval: 1 * time.Minute})

	object, err := inMemory.Receive(context.Background(), "key")

	assert.Nil(t, object)
	assert.True(t, errors.Is(err, caches.ErrCacheNotFound))
}

func TestInMemoryMaxItems(t *testing.T) {
	inMemory := caches.NewInMemoryConnector("id", &config.MemoryCacheConnectorConfig{MaxItems: 3, ExpiredRemoveInterval: 1 * time.Minute})
	key1 := "key1"
	key2 := "key2"
	key3 := "key3"
	key4 := "key4"
	storedObject := "object"

	for _, key := range []string{key1, key2, key3} {
		err := inMemory.Store(context.Background(), key, storedObject, 0)
		assert.Nil(t, err)

		object, err := inMemory.Receive(context.Background(), key)
		assert.Nil(t, err)
		assert.True(t, bytes.Equal([]byte(storedObject), object))
	}

	err := inMemory.Store(context.Background(), key4, storedObject, 0)
	assert.Nil(t, err)
	object, err := inMemory.Receive(context.Background(), key4)
	assert.Nil(t, err)
	assert.True(t, bytes.Equal([]byte(storedObject), object))

	object, err = inMemory.Receive(context.Background(), key1)
	assert.Nil(t, object)
	assert.True(t, errors.Is(err, caches.ErrCacheNotFound))

	for _, key := range []string{key2, key3} {
		object, err = inMemory.Receive(context.Background(), key)
		assert.Nil(t, err)
		assert.True(t, bytes.Equal([]byte(storedObject), object))
	}
}

func TestInMemoryCacheId(t *testing.T) {
	inMemory := caches.NewInMemoryConnector("cacheId", &config.MemoryCacheConnectorConfig{MaxItems: 1000, ExpiredRemoveInterval: 1 * time.Minute})

	assert.Equal(t, "cacheId", inMemory.Id())
}

func TestInMemoryCacheStoreThenReceiveWithoutTtl(t *testing.T) {
	inMemory := caches.NewInMemoryConnector("id", &config.MemoryCacheConnectorConfig{MaxItems: 1000, ExpiredRemoveInterval: 10 * time.Millisecond})
	key := "key"
	storedObject := "object"

	err := inMemory.Store(context.Background(), key, storedObject, 0)
	assert.Nil(t, err)

	object, err := inMemory.Receive(context.Background(), "key")
	assert.Nil(t, err)
	assert.True(t, bytes.Equal([]byte(storedObject), object))

	time.Sleep(30 * time.Millisecond)

	object, err = inMemory.Receive(context.Background(), "key")
	assert.Nil(t, err)
	assert.True(t, bytes.Equal([]byte(storedObject), object))
}

func TestInMemoryCacheStoreThenReceiveWithTtl(t *testing.T) {
	inMemory := caches.NewInMemoryConnector("id", &config.MemoryCacheConnectorConfig{MaxItems: 1000, ExpiredRemoveInterval: 10 * time.Millisecond})
	key := "key"
	storedObject := "object"

	err := inMemory.Store(context.Background(), key, storedObject, 10*time.Millisecond)
	assert.Nil(t, err)

	object, err := inMemory.Receive(context.Background(), "key")
	assert.Nil(t, err)
	assert.True(t, bytes.Equal([]byte(storedObject), object))

	time.Sleep(30 * time.Millisecond)

	object, err = inMemory.Receive(context.Background(), "key")
	assert.Nil(t, object)
	assert.True(t, errors.Is(err, caches.ErrCacheNotFound))
}
