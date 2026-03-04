package auth_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestNoopAuthProcessor(t *testing.T) {
	noopProcessor, err := auth.NewAuthProcessor(context.Background(), nil, nil)
	assert.NoError(t, err)

	keyValue := noopProcessor.GetKeyValue(nil)
	assert.Empty(t, keyValue)

	preResult, err := noopProcessor.PreKeyValidate(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, preResult)

	authErr := noopProcessor.Authenticate(context.Background(), nil)
	assert.NoError(t, authErr)

	postErr := noopProcessor.PostKeyValidate(context.Background(), nil, nil)
	assert.NoError(t, postErr)
}

func TestSimpleAuthProcessor(t *testing.T) {
	appCfg := &config.AuthConfig{
		Enabled: true,
		RequestStrategyConfig: &config.RequestStrategyConfig{
			Type:                       config.Token,
			TokenRequestStrategyConfig: &config.TokenRequestStrategyConfig{Value: "token"},
		},
	}
	simpleProcessor, err := auth.NewAuthProcessor(context.Background(), appCfg, nil)
	assert.NoError(t, err)

	keyValue := simpleProcessor.GetKeyValue(nil)
	assert.Empty(t, keyValue)

	preResult, err := simpleProcessor.PreKeyValidate(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, preResult)

	postErr := simpleProcessor.PostKeyValidate(context.Background(), nil, nil)
	assert.NoError(t, postErr)

	payload := newPayload(t, map[string]string{
		auth.XNodecoreToken: "token",
	})

	authErr := simpleProcessor.Authenticate(context.Background(), payload)
	assert.NoError(t, authErr)
}
