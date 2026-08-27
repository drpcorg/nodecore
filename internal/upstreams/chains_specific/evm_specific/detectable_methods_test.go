package evm_specific

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectableConnectorTypesKeepsOnlyJsonRpcSpeakingConnectors(t *testing.T) {
	apiConnectors := []connectors.ApiConnector{
		newTypedConnector(specs.JsonRpcConnector),
		newTypedConnector(specs.RestAdditional),
		newTypedConnector(specs.WebsocketConnector),
		newTypedConnector(specs.RestConnector),
		newTypedConnector(specs.TendermintConnector),
	}

	assert.Equal(
		t,
		[]specs.ApiConnectorType{specs.JsonRpcConnector, specs.WebsocketConnector},
		detectableConnectorTypes(apiConnectors),
	)
}

// A rest-additional connector must not widen the detectable set: its methods are REST paths
// that no JSON-RPC evidence can speak to, and moduleOf would read the segment before the
// first underscore of one as a module and strip the method for want of a node reporting it.
func TestDetectableMethodsExcludeRestAdditionalMethods(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	chainSpecific := newHyperliquidChainSpecific(
		newTypedConnector(specs.JsonRpcConnector),
		newTypedConnector(specs.RestAdditional),
	)

	detectable := chainSpecific.detectableMethods()

	assert.True(t, detectable.ContainsOne("eth_getBalance"), "a JSON-RPC method must be detectable")
	for _, restMethod := range []string{"POST#/info", "POST#/exchange"} {
		assert.False(t, detectable.ContainsOne(restMethod), "%s is served by another connector", restMethod)
	}
}

func TestDetectableMethodsAreEmptyWithoutJsonRpcSpeakingConnectors(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	chainSpecific := newHyperliquidChainSpecific(newTypedConnector(specs.RestAdditional))

	assert.True(t, chainSpecific.detectableMethods().IsEmpty())
}

func newHyperliquidChainSpecific(apiConnectors ...connectors.ApiConnector) *EvmChainSpecificObject {
	return NewEvmChainSpecific(
		context.Background(),
		"id",
		apiConnectors[0],
		apiConnectors,
		chains.GetChain("hyperliquid"),
		time.Second,
		&chains.Options{InternalTimeout: time.Second},
		nil,
	)
}

func newTypedConnector(connectorType specs.ApiConnectorType) *typedConnector {
	return &typedConnector{connectorType: connectorType}
}

// typedConnector is an ApiConnector that carries nothing but its type - the only thing
// detectableConnectorTypes asks about. The package's other tests reach for
// pkg/test_utils/mocks, which an in-package test cannot: that package imports
// internal/upstreams, which imports this one.
type typedConnector struct {
	connectorType specs.ApiConnectorType
}

func (c *typedConnector) Start() {}

func (c *typedConnector) Stop() {}

func (c *typedConnector) Running() bool { return true }

func (c *typedConnector) SendRequest(_ context.Context, _ protocol.RequestHolder) protocol.ResponseHolder {
	return nil
}

func (c *typedConnector) Subscribe(_ context.Context, _ protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}

func (c *typedConnector) Unsubscribe(_ string) {}

func (c *typedConnector) GetType() specs.ApiConnectorType { return c.connectorType }

func (c *typedConnector) GetUrl() string { return "" }

func (c *typedConnector) SubscribeStates(_ string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return nil
}

var _ connectors.ApiConnector = (*typedConnector)(nil)
