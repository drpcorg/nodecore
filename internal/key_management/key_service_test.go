package keymanagement_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/key_management"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestNewLocalKey_AndKeyResolver_Retrieval(t *testing.T) {
	cfg := &config.KeyConfig{
		Id:             "kid2",
		Type:           config.Local,
		LocalKeyConfig: test_utils.BuildLocalKeyConfig("secret-abc", []string{"127.0.0.1"}, nil, nil),
	}
	resolver, err := keymanagement.NewKeyService(context.Background(), []*config.KeyConfig{cfg}, integration.NewIntegrationResolver(nil))
	assert.NoError(t, err)
	time.Sleep(30 * time.Millisecond)

	k, ok := resolver.GetKey("secret-abc")
	assert.True(t, ok, "expected key to be found by resolver")
	assert.Equal(t, "kid2", k.Id())

	_, ok = resolver.GetKey("unknown-secret")
	assert.False(t, ok, "expected not found for unknown key")
}

func TestKeyResolverNoIntegrationThenErr(t *testing.T) {
	resolver := integration.NewNewIntegrationResolverWithClients(
		map[integration.IntegrationType]integration.IntegrationClient{},
	)
	cfg := []*config.KeyConfig{
		{
			Id:   "id",
			Type: config.Drpc,
			DrpcKeyConfig: &config.DrpcKeyConfig{
				Owner: &config.DrpcOwnerConfig{
					Id:       "id",
					ApiToken: "apiToken",
				},
			},
		},
	}

	keyResolver, err := keymanagement.NewKeyService(context.Background(), cfg, resolver)

	assert.Nil(t, keyResolver)
	assert.Error(t, err, "there is no drpc integration config to load drpc keys")
}

func TestKeyResolverDrpcKeysFailedInitKeys(t *testing.T) {
	client := mocks.NewMockIntegrationClient(integration.Drpc)
	resolver := integration.NewNewIntegrationResolverWithClients(
		map[integration.IntegrationType]integration.IntegrationClient{
			integration.Drpc: client,
		},
	)
	cfg := []*config.KeyConfig{
		{
			Id:   "id",
			Type: config.Drpc,
			DrpcKeyConfig: &config.DrpcKeyConfig{
				Owner: &config.DrpcOwnerConfig{
					Id:       "id",
					ApiToken: "apiToken",
				},
			},
		},
	}

	client.On("InitKeys", "id", cfg[0].DrpcKeyConfig).Return(nil, errors.New("some err"))

	_, err := keymanagement.NewKeyServiceWithRetryInterval(context.Background(), cfg, resolver, 5*time.Millisecond)
	assert.NoError(t, err)

	time.Sleep(20 * time.Millisecond)

	client.AssertExpectations(t)
}

func TestKeyResolverDrpcKeysEvents(t *testing.T) {
	client := mocks.NewMockIntegrationClient(integration.Drpc)
	resolver := integration.NewNewIntegrationResolverWithClients(
		map[integration.IntegrationType]integration.IntegrationClient{
			integration.Drpc: client,
		},
	)
	cfg := []*config.KeyConfig{
		{
			Id:   "id",
			Type: config.Drpc,
			DrpcKeyConfig: &config.DrpcKeyConfig{
				Owner: &config.DrpcOwnerConfig{
					Id:       "id",
					ApiToken: "apiToken",
				},
			},
		},
	}

	allKeys := []keydata.Key{
		&drpc.DrpcKey{
			KeyId:  "id",
			ApiKey: "apiKey",
		},
	}
	eventChan := make(chan keydata.KeyEvent, 10)

	client.On("InitKeys", "id", cfg[0].DrpcKeyConfig).Return(eventChan, nil).Once()

	keyResolver, err := keymanagement.NewKeyService(context.Background(), cfg, resolver)
	assert.NoError(t, err)

	eventChan <- keydata.NewUpdatedKeyEvent(allKeys[0])

	time.Sleep(10 * time.Millisecond)

	key, ok := keyResolver.GetKey("apiKey")
	assert.True(t, ok)
	assert.Equal(t, allKeys[0], key)

	updatedKey := &drpc.DrpcKey{
		KeyId:       "id",
		IpWhitelist: []string{"127.0.0.1"},
		ApiKey:      "apiKey",
	}
	// update a key
	eventChan <- keydata.NewUpdatedKeyEvent(updatedKey)

	time.Sleep(10 * time.Millisecond)

	key, ok = keyResolver.GetKey("apiKey")
	assert.True(t, ok)
	assert.Equal(t, updatedKey, key)

	// remove a key
	eventChan <- keydata.NewRemovedKeyEvent(updatedKey)

	time.Sleep(10 * time.Millisecond)

	key, ok = keyResolver.GetKey("apiKey")
	assert.False(t, ok)
	assert.Nil(t, key)

	client.AssertExpectations(t)
}
