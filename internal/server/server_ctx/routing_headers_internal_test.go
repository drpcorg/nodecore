package server_ctx

import (
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// a holder that cannot carry selectors: embedding the interface promotes only
// the RequestHolder methods, not the concrete type's AppendSelectors
type holderWithoutSelectors struct{ protocol.RequestHolder }

// a holder that cannot carry selectors must fail the request, not be served unrestricted
func TestAppendRoutingSelectors(t *testing.T) {
	selectors := []protocol.RequestSelector{protocol.RequestUpstreamIdSelector{Ids: mapset.NewThreadUnsafeSet("a")}}

	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_call"}, false, "eth")
	require.NoError(t, appendRoutingSelectors(request, selectors))
	assert.Equal(t, selectors, request.Selectors())

	untouched := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_call"}, false, "eth")
	require.NoError(t, appendRoutingSelectors(untouched, nil))
	assert.Empty(t, untouched.Selectors())

	assert.Error(t, appendRoutingSelectors(holderWithoutSelectors{request}, selectors))
	assert.NoError(t, appendRoutingSelectors(holderWithoutSelectors{request}, nil))
}

func TestRoutingHeadersEnabled(t *testing.T) {
	tests := []struct {
		name      string
		appConfig *config.AppConfig
		expected  bool
	}{
		{"no app config", nil, false},
		{"no server config", &config.AppConfig{}, false},
		{"option left out", &config.AppConfig{ServerConfig: &config.ServerConfig{}}, false},
		{"option on", &config.AppConfig{ServerConfig: &config.ServerConfig{RoutingHeaders: true}}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			appCtx := &ApplicationServerContext{AppConfig: test.appConfig}
			assert.Equal(te, test.expected, appCtx.routingHeadersEnabled())
		})
	}
}
