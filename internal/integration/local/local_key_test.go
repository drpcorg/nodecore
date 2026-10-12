package local_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration/local"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
)

func TestLocalKeyGetValues(t *testing.T) {
	keyCfg := &config.LocalKeyConfig{
		Key: "secret-key",
	}
	key := local.NewLocalKey("key-id", keyCfg)

	assert.Equal(t, "key-id", key.Id())
	assert.Equal(t, "secret-key", key.GetKeyValue())
}

func TestLocalKeyRestrictsUpstreams(t *testing.T) {
	key := local.NewLocalKey("key-id", &config.LocalKeyConfig{
		Key: "secret-key",
		KeySettingsConfig: &config.KeySettingsConfig{
			Upstreams: &config.KeyUpstreams{GroupLabels: []string{"archive", "fast"}},
		},
	})
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth")

	assert.NoError(t, key.PostCheckSetting(context.Background(), request))
	assert.Equal(t, []protocol.RequestSelector{protocol.RequestGroupLabelSelector{Labels: []string{"archive", "fast"}}}, request.Selectors())
}

func TestLocalKeyWithoutUpstreamsAddsNoSelector(t *testing.T) {
	key := local.NewLocalKey("key-id", &config.LocalKeyConfig{
		Key:               "secret-key",
		KeySettingsConfig: &config.KeySettingsConfig{},
	})
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_call"}, false, "eth")

	assert.NoError(t, key.PostCheckSetting(context.Background(), request))
	assert.Empty(t, request.Selectors())
}

// opaqueRequest hides AppendSelectors, standing in for a request type that
// cannot carry the key's restriction.
type opaqueRequest struct{ protocol.RequestHolder }

func TestLocalKeyRejectsRequestThatCannotBeRestricted(t *testing.T) {
	key := local.NewLocalKey("key-id", &config.LocalKeyConfig{
		Key: "secret-key",
		KeySettingsConfig: &config.KeySettingsConfig{
			Upstreams: &config.KeyUpstreams{GroupLabels: []string{"archive"}},
		},
	})
	request := opaqueRequest{protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_call"}, false, "eth")}

	assert.ErrorContains(t, key.PostCheckSetting(context.Background(), request), "cannot be restricted to the key's upstreams")
}
