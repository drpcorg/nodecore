package upstreams

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/aptos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/cosmos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/polkadot_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubConnector is a do-nothing ApiConnector for factory tests: the factory
// only stores the connector, so no methods are ever called. It cannot use
// pkg/test_utils/mocks because that package imports internal/upstreams, which
// would create an import cycle from an internal (white-box) test.
type stubConnector struct {
	connectors.ApiConnector
	connectorType specs.ApiConnectorType
}

func (s *stubConnector) GetType() specs.ApiConnectorType {
	return s.connectorType
}

func TestGetChainSpecificReturnsAptos(t *testing.T) {
	ctx := context.Background()
	conf := &config.Upstream{Id: "u1", Options: newAptosTestOptions()}
	cs, err := getChainSpecific(ctx, conf, &stubConnector{}, nil, chains.GetChain("aptos-mainnet"))
	assert.NoError(t, err)
	assert.IsType(t, &aptos_specific.AptosChainSpecificObject{}, cs)
}

func newAptosTestOptions() *chains.Options {
	disabled := false
	return &chains.Options{
		InternalTimeout:         time.Second,
		ValidationInterval:      time.Second,
		DisableChainValidation:  &disabled,
		DisableHealthValidation: &disabled,
	}
}

func TestGetChainSpecificReturnsPolkadot(t *testing.T) {
	ctx := context.Background()
	conf := &config.Upstream{Id: "u1", PollInterval: time.Second, Options: newPolkadotTestOptions()}
	cs, err := getChainSpecific(ctx, conf, &stubConnector{}, nil, chains.GetChain("polkadot"))
	assert.NoError(t, err)
	assert.IsType(t, &polkadot_specific.PolkadotChainSpecificObject{}, cs)
}

func newPolkadotTestOptions() *chains.Options {
	disabled := false
	return &chains.Options{
		InternalTimeout:         time.Second,
		ValidationInterval:      time.Second,
		DisableChainValidation:  &disabled,
		DisableHealthValidation: &disabled,
		ValidateSyncing:         &disabled,
		ValidatePeers:           &disabled,
	}
}

// The probe specific is built from the internal-request connector, the head
// specific from the head connector. On an Injective node with json-rpc probes
// and a grpc head they belong to different API families, and each must be the
// one that speaks its connector's protocol - a head built from the probe
// specific would try to open eth_subscribe over gRPC.
func TestUpstreamSpecificsFollowTheirConnectors(t *testing.T) {
	ctx := context.Background()
	conf := &config.Upstream{Id: "u1", PollInterval: time.Second, Options: newPolkadotTestOptions()}
	jsonRpc := &stubConnector{connectorType: specs.JsonRpcConnector}
	grpc := &stubConnector{connectorType: specs.GrpcConnector}
	info := &connectorsInfo{
		internalRequestConnector: jsonRpc,
		headConnector:            grpc,
		allConnectors:            []connectors.ApiConnector{jsonRpc, grpc},
	}

	specifics, err := getUpstreamSpecifics(ctx, conf, info, chains.GetChain("injective-testnet"))
	require.NoError(t, err)

	assert.IsType(t, &evm_specific.EvmChainSpecificObject{}, specifics.probe)
	assert.IsType(t, &cosmos_specific.CosmosGrpcSpecific{}, specifics.head)
}

// One connector for both roles means one specific for both roles.
func TestUpstreamSpecificsShareTheObjectOnASingleConnector(t *testing.T) {
	ctx := context.Background()
	conf := &config.Upstream{Id: "u1", PollInterval: time.Second, Options: newPolkadotTestOptions()}
	connector := &stubConnector{connectorType: specs.TendermintConnector}
	info := &connectorsInfo{
		internalRequestConnector: connector,
		headConnector:            connector,
		allConnectors:            []connectors.ApiConnector{connector},
	}

	specifics, err := getUpstreamSpecifics(ctx, conf, info, chains.GetChain("injective-testnet"))
	require.NoError(t, err)

	assert.Same(t, specifics.probe, specifics.head)
}
