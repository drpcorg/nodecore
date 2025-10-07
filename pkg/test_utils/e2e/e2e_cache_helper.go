package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/stretchr/testify/assert"
)

func TestConnectorInitialize(t *testing.T, connector caches.CacheConnector) {
	err := connector.Initialize()
	assert.Nil(t, err)
}

func TestConnectorNoItemThenErrCacheNotFound(t *testing.T, connector caches.CacheConnector) {
	err := connector.Initialize()
	assert.Nil(t, err)

	value, err := connector.Receive(context.Background(), "key")

	assert.Nil(t, value)
	assert.ErrorIs(t, err, caches.ErrCacheNotFound)
}

func TestConnectorStoreThenReceive(t *testing.T, connector caches.CacheConnector) {
	key := "super-key"
	item := "my-item"

	err := connector.Initialize()
	assert.Nil(t, err)

	err = connector.Store(context.Background(), key, item, 5*time.Second)
	assert.Nil(t, err)

	value, err := connector.Receive(context.Background(), key)

	assert.Nil(t, err)
	assert.Equal(t, []byte(item), value)
}

func TestConnectorStoreAndRemoveExpired(t *testing.T, connector caches.CacheConnector) {
	key := "other-super-key"

	err := connector.Initialize()
	assert.Nil(t, err)

	err = connector.Store(context.Background(), key, "my-item", 5*time.Millisecond)
	assert.Nil(t, err)

	time.Sleep(15 * time.Millisecond)

	value, err := connector.Receive(context.Background(), key)

	assert.Nil(t, value)
	assert.ErrorIs(t, err, caches.ErrCacheNotFound)
}
