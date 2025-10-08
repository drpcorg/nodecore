package caches_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
)

func TestPostgresConnectorCantParseUrl(t *testing.T) {
	connector, err := caches.NewPostgresConnector("id", &config.PostgresCacheConnectorConfig{Url: "http://url.com"})

	assert.Nil(t, connector)
	assert.ErrorContains(t, err, "couldn't read the postgres url http://url.com of connector 'id': cannot parse `http://url.com`: failed to parse as keyword/value (invalid keyword/value)")
}

func TestPostgresConnectorCantCreatePool(t *testing.T) {
	connector, err := caches.NewPostgresConnector(
		"id",
		&config.PostgresCacheConnectorConfig{
			Url:          "postgres://user:user@localhost:3000/cache",
			QueryTimeout: lo.ToPtr(1 * time.Second),
		},
	)
	assert.Nil(t, err)

	err = connector.Initialize()
	assert.ErrorContains(t, err, "couldn't connect to postgres")
}
