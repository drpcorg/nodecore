package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestDrpcIntegrationClientReturnType(t *testing.T) {
	client := integration.NewDrpcIntegrationClient(&config.DrpcIntegrationConfig{Url: "http://localhost:8080"})

	assert.Equal(t, integration.Drpc, client.Type())
}

func TestDrpcIntegrationClientNotDrpcCfgThenErr(t *testing.T) {
	client := integration.NewDrpcIntegrationClient(&config.DrpcIntegrationConfig{Url: "http://localhost:8080"})

	events, err := client.InitKeys("id", &config.ExternalKeyConfig{})

	assert.Nil(t, events)
	assert.ErrorContains(t, err, "drpc init keys expects drpc key config")
}

func TestDrpcIntegrationClientNoOwnerThenErr(t *testing.T) {
	client := integration.NewDrpcIntegrationClient(&config.DrpcIntegrationConfig{Url: "http://localhost:8080"})

	events, err := client.InitKeys("id", &config.DrpcKeyConfig{})

	assert.Nil(t, events)
	assert.ErrorContains(t, err, "there must be drpc owner config to init drpc keys")
}

func TestDrpcIntegrationClientInitKeys(t *testing.T) {
	connector := mocks.NewMockDrpcHttpcConnector()
	client := integration.NewDrpcIntegrationClientWithConnector(context.Background(), connector, 1*time.Minute)
	cfg := &config.DrpcKeyConfig{Owner: &config.DrpcOwnerConfig{Id: "owner", ApiToken: "owner-token"}}

	drpcKeys := []*drpc.DrpcKey{
		{
			KeyId:             "id",
			IpWhitelist:       []string{"1.1.1.1"},
			MethodsBlacklist:  []string{"method"},
			MethodsWhitelist:  []string{"method1"},
			ContractWhitelist: []string{"contract"},
			CorsOrigins:       []string{"http://localhost:8080"},
			ApiKey:            "api-key",
		},
		{
			KeyId:             "id2",
			IpWhitelist:       []string{"2.2.2.2"},
			MethodsBlacklist:  []string{"method2"},
			MethodsWhitelist:  []string{"method3"},
			ContractWhitelist: []string{"contract2"},
		},
	}

	connector.On("OwnerExists", cfg.Owner.Id, cfg.Owner.ApiToken).Return(nil)
	connector.On("LoadOwnerKeys", cfg.Owner.Id, cfg.Owner.ApiToken).Return(drpcKeys, nil)

	events, err := client.InitKeys("id", cfg)

	assert.NoError(t, err)

	key := <-events
	assert.Equal(t, drpcKeys[0], key.(*keydata.UpdatedKeyEvent).NewKey)

	key = <-events
	assert.Equal(t, drpcKeys[1], key.(*keydata.UpdatedKeyEvent).NewKey)

	connector.AssertExpectations(t)
}

func TestDrpcIntegrationClientPollKeys(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	connector := mocks.NewMockDrpcHttpcConnector()
	client := integration.NewDrpcIntegrationClientWithConnector(ctx, connector, 10*time.Millisecond)
	cfg := &config.DrpcKeyConfig{Owner: &config.DrpcOwnerConfig{Id: "owner", ApiToken: "owner-token"}}

	initialKeys := []*drpc.DrpcKey{
		{
			KeyId:  "id",
			ApiKey: "api-key",
		},
	}
	moreKeys := []*drpc.DrpcKey{
		{
			KeyId:  "id",
			ApiKey: "api-key",
		},
		{
			KeyId:  "id1",
			ApiKey: "another-api-key",
		},
	}
	changedKeys := []*drpc.DrpcKey{
		{
			KeyId:       "id",
			IpWhitelist: []string{"1.1.1.1"},
			ApiKey:      "api-key",
		},
		{
			KeyId:  "id1",
			ApiKey: "another-api-key",
		},
	}
	removedAndAddedKeys := []*drpc.DrpcKey{
		{
			KeyId:  "another-id",
			ApiKey: "super-key",
		},
		{
			KeyId:  "id1",
			ApiKey: "another-api-key",
		},
	}

	connector.On("OwnerExists", cfg.Owner.Id, cfg.Owner.ApiToken).Return(nil)
	connector.On("LoadOwnerKeys", cfg.Owner.Id, cfg.Owner.ApiToken).Return(initialKeys, nil).Once()
	connector.On("LoadOwnerKeys", cfg.Owner.Id, cfg.Owner.ApiToken).Return(moreKeys, nil).Once()
	connector.On("LoadOwnerKeys", cfg.Owner.Id, cfg.Owner.ApiToken).Return(changedKeys, nil).Once()
	connector.On("LoadOwnerKeys", cfg.Owner.Id, cfg.Owner.ApiToken).Return(removedAndAddedKeys, nil).Once()

	events, err := client.InitKeys("id", cfg)
	assert.NoError(t, err)

	key := <-events
	assert.Equal(t, initialKeys[0], key.(*keydata.UpdatedKeyEvent).NewKey)

	// get the same key
	var event keydata.KeyEvent
	select {
	case <-time.After(5 * time.Millisecond):
	case event = <-events:
	}
	assert.Nil(t, event)

	// add a new key
	event = <-events
	ev, ok := event.(*keydata.UpdatedKeyEvent)
	assert.True(t, ok)
	drpcKey, ok := ev.NewKey.(*drpc.DrpcKey)
	assert.True(t, ok)
	assert.Equal(t, moreKeys[1], drpcKey)

	// change a key
	event = <-events
	ev, ok = event.(*keydata.UpdatedKeyEvent)
	assert.True(t, ok)
	drpcKey, ok = ev.NewKey.(*drpc.DrpcKey)
	assert.True(t, ok)
	assert.Equal(t, changedKeys[0], drpcKey)

	// add a new key
	event = <-events
	ev, ok = event.(*keydata.UpdatedKeyEvent)
	assert.True(t, ok)
	drpcKey, ok = ev.NewKey.(*drpc.DrpcKey)
	assert.True(t, ok)
	assert.Equal(t, removedAndAddedKeys[0], drpcKey)

	// remove a key
	event = <-events
	removedEv, ok := event.(*keydata.RemovedKeyEvent)
	assert.True(t, ok)
	drpcKey, ok = removedEv.RemovedKey.(*drpc.DrpcKey)
	assert.True(t, ok)
	assert.Equal(t, changedKeys[0], drpcKey)

	cancel()

	connector.AssertExpectations(t)
}
