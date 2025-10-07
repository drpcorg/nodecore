package caches_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestRedisConnectorCantParseUrl(t *testing.T) {
	connector, err := caches.NewRedisConnector("id", &config.RedisCacheConnectorConfig{FullUrl: "http://url.com"})

	assert.Nil(t, connector)
	assert.ErrorContains(t, err, "couldn't read the full redis url http://url.com of connector 'id': redis: invalid URL scheme: http")
}

func TestRedisConnectorCantPing(t *testing.T) {
	connector, err := caches.NewRedisConnector(
		"id",
		&config.RedisCacheConnectorConfig{
			FullUrl: "redis://:testPass@localhost:9000/0?read_timeout=3000ms",
		},
	)
	assert.Nil(t, err)

	err = connector.Initialize()
	assert.ErrorContains(t, err, "could not connect to redis, dial tcp [::1]:9000: connect: connection refused")
}
