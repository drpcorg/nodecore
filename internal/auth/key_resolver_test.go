package auth_test

import (
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	keymanagement "github.com/drpcorg/nodecore/internal/key_management"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestNewLocalKey_AndKeyResolver_Retrieval(t *testing.T) {
	cfg := test_utils.BuildLocalKeyConfig("kid2", "secret-abc", []string{"127.0.0.1"}, nil, nil)
	resolver, err := auth.NewKeyResolver([]*config.KeyConfig{cfg}, nil)
	assert.NoError(t, err)

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
			Type: config.DrpcKey,
			DrpcKeyConfig: &config.DrpcKeyConfig{
				Owner: &config.DrpcOwnerConfig{
					Id:       "id",
					ApiToken: "apiToken",
				},
			},
		},
	}

	keyResolver, err := auth.NewKeyResolver(cfg, resolver)

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
			Type: config.DrpcKey,
			DrpcKeyConfig: &config.DrpcKeyConfig{
				Owner: &config.DrpcOwnerConfig{
					Id:       "id",
					ApiToken: "apiToken",
				},
			},
		},
	}

	client.On("InitKeys", cfg[0].DrpcKeyConfig).Return(nil, errors.New("some err"))

	_, err := auth.NewKeyResolverWithRetryInterval(cfg, resolver, 5*time.Millisecond)
	assert.NoError(t, err)

	time.Sleep(20 * time.Millisecond)

	client.AssertExpectations(t)
}

func TestKeyResolverDrpcKeysFailedInitKeysButThenRetryAndEvents(t *testing.T) {
	client := mocks.NewMockIntegrationClient(integration.Drpc)
	resolver := integration.NewNewIntegrationResolverWithClients(
		map[integration.IntegrationType]integration.IntegrationClient{
			integration.Drpc: client,
		},
	)
	cfg := []*config.KeyConfig{
		{
			Id:   "id",
			Type: config.DrpcKey,
			DrpcKeyConfig: &config.DrpcKeyConfig{
				Owner: &config.DrpcOwnerConfig{
					Id:       "id",
					ApiToken: "apiToken",
				},
			},
		},
	}

	keys := []keymanagement.Key{
		&drpc.DrpcKey{
			KeyId:  "id",
			ApiKey: "apiKey",
		},
	}
	eventChan := make(chan integration.KeyEvent, 10)
	initData := integration.NewInitKeysData(keys, eventChan)

	client.On("InitKeys", cfg[0].DrpcKeyConfig).Return(nil, protocol.NewClientRetryableError(errors.New("some err"))).Once()
	client.On("InitKeys", cfg[0].DrpcKeyConfig).Return(initData, nil).Once()

	keyResolver, err := auth.NewKeyResolverWithRetryInterval(cfg, resolver, 10*time.Millisecond)
	assert.NoError(t, err)

	time.Sleep(15 * time.Millisecond)

	client.AssertExpectations(t)

	key, ok := keyResolver.GetKey("apiKey")

	assert.True(t, ok)
	assert.Equal(t, keys[0], key)

	updatedKey := &drpc.DrpcKey{
		KeyId:       "id",
		IpWhitelist: []string{"127.0.0.1"},
		ApiKey:      "apiKey",
	}
	// update a key
	eventChan <- integration.NewUpdatedKeyEvent(updatedKey)

	time.Sleep(10 * time.Millisecond)

	key, ok = keyResolver.GetKey("apiKey")
	assert.True(t, ok)
	assert.Equal(t, updatedKey, key)

	// remove a key
	eventChan <- integration.NewRemovedKeyEvent(updatedKey)

	time.Sleep(10 * time.Millisecond)

	key, ok = keyResolver.GetKey("apiKey")
	assert.False(t, ok)
	assert.Nil(t, key)
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
			Type: config.DrpcKey,
			DrpcKeyConfig: &config.DrpcKeyConfig{
				Owner: &config.DrpcOwnerConfig{
					Id:       "id",
					ApiToken: "apiToken",
				},
			},
		},
	}

	keys := []keymanagement.Key{
		&drpc.DrpcKey{
			KeyId:  "id",
			ApiKey: "apiKey",
		},
	}
	eventChan := make(chan integration.KeyEvent, 10)
	initData := integration.NewInitKeysData(keys, eventChan)

	client.On("InitKeys", cfg[0].DrpcKeyConfig).Return(initData, nil).Once()

	keyResolver, err := auth.NewKeyResolver(cfg, resolver)
	assert.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	key, ok := keyResolver.GetKey("apiKey")
	assert.True(t, ok)
	assert.Equal(t, keys[0], key)

	updatedKey := &drpc.DrpcKey{
		KeyId:       "id",
		IpWhitelist: []string{"127.0.0.1"},
		ApiKey:      "apiKey",
	}
	// update a key
	eventChan <- integration.NewUpdatedKeyEvent(updatedKey)

	time.Sleep(10 * time.Millisecond)

	key, ok = keyResolver.GetKey("apiKey")
	assert.True(t, ok)
	assert.Equal(t, updatedKey, key)

	// remove a key
	eventChan <- integration.NewRemovedKeyEvent(updatedKey)

	time.Sleep(10 * time.Millisecond)

	key, ok = keyResolver.GetKey("apiKey")
	assert.False(t, ok)
	assert.Nil(t, key)

	client.AssertExpectations(t)
}
