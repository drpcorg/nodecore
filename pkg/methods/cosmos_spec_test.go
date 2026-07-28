package specs_test

import (
	"strings"
	"testing"

	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTendermintApiConnectorType(t *testing.T) {
	assert.Equal(t, specs.TendermintConnector, specs.GetApiConnectorType("tendermint"))
	assert.Equal(t, "tendermint", specs.TendermintConnector.String())
	assert.NoError(t, specs.ValidateApiConnectorType("tendermint"))
	assert.False(t, specs.IsAdditionalApiConnectorType(specs.TendermintConnector))
	assert.Contains(t, specs.GetPlainApiConnectorType(), specs.TendermintConnector)
}

// The tendermint connector must win over rest when both are configured, so it
// is the one that drives head/health/labels/lower-bound accounting.
func TestTendermintIsPreferredOverRest(t *testing.T) {
	assert.Less(t, specs.TendermintConnector, specs.RestConnector)
	assert.Less(t, specs.JsonRpcConnector, specs.TendermintConnector)
}

func TestCosmosBundleCarriesBothConnectorFamilies(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	assert.ElementsMatch(t,
		[]specs.ApiConnectorType{specs.TendermintConnector, specs.RestConnector},
		specs.GetSpecConnectors("cosmos"),
	)

	tendermintOnly := specs.GetSpecMethodsByConnectors("cosmos", []specs.ApiConnectorType{specs.TendermintConnector})
	require.NotNil(t, tendermintOnly)
	tendermintMethods := tendermintOnly[specs.DefaultMethodGroup]
	assert.Contains(t, tendermintMethods, "status")
	assert.Contains(t, tendermintMethods, "GET#/status")
	assert.NotContains(t, tendermintMethods, "GET#/cosmos/bank/v1beta1/params")

	restOnly := specs.GetSpecMethodsByConnectors("cosmos", []specs.ApiConnectorType{specs.RestConnector})
	require.NotNil(t, restOnly)
	restMethods := restOnly[specs.DefaultMethodGroup]
	assert.Contains(t, restMethods, "GET#/cosmos/bank/v1beta1/params")
	assert.NotContains(t, restMethods, "status")
}

// Every tendermint method is reachable both as a JSON-RPC name and as a URI
// call, because CometBFT serves both shapes on the same port.
func TestTendermintMethodsAreDeclaredInBothShapes(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	groups := specs.GetSpecMethodsByConnectors("cosmos-tendermint", []specs.ApiConnectorType{specs.TendermintConnector})
	require.NotNil(t, groups)
	methods := groups[specs.DefaultMethodGroup]
	require.NotEmpty(t, methods)

	for name := range methods {
		if strings.HasPrefix(name, "GET#/") {
			assert.Contains(t, methods, strings.TrimPrefix(name, "GET#/"), "json-rpc twin of %s", name)
			continue
		}
		assert.Contains(t, methods, "GET#/"+name, "rest twin of %s", name)
	}
}

func TestCosmosMethodsAreNotCacheable(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	for _, connectorType := range []specs.ApiConnectorType{specs.TendermintConnector, specs.RestConnector} {
		groups := specs.GetSpecMethodsByConnectors("cosmos", []specs.ApiConnectorType{connectorType})
		require.NotEmpty(t, groups[specs.DefaultMethodGroup], "methods for %s", connectorType)
		for name, method := range groups[specs.DefaultMethodGroup] {
			assert.False(t, method.IsCacheable(), "%s must not be cacheable", name)
		}
	}
}

// The broadcast group exists so an operator can disable transaction
// submission as a set. Fan-out is deliberately not declared on the tendermint
// side yet, so those methods must carry no dispatch policy.
func TestCosmosBroadcastGroup(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	broadcastGroup := specs.GetSpecMethodsByConnectors("cosmos", nil)["broadcast"]
	assert.Len(t, broadcastGroup, 9)

	for _, name := range []string{
		"broadcast_tx_sync", "broadcast_tx_async", "broadcast_tx_commit", "broadcast_evidence",
		"GET#/broadcast_tx_sync", "GET#/broadcast_tx_async", "GET#/broadcast_tx_commit", "GET#/broadcast_evidence",
	} {
		method := specs.GetSpecMethod("cosmos", name)
		require.NotNil(t, method, name)
		assert.Contains(t, broadcastGroup, name)
		assert.False(t, method.IsBroadcastDispatch(), "%s must not declare a dispatch policy", name)
	}
}

// dump_consensus_state / consensus_state leak validator-level detail, so they
// ship declared-but-disabled (dshackle marks them "not safe").
func TestCosmosUnsafeMethodsAreDisabled(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	for _, name := range []string{
		"dump_consensus_state", "consensus_state",
		"GET#/dump_consensus_state", "GET#/consensus_state",
	} {
		assert.Nil(t, specs.GetSpecMethod("cosmos", name), "%s must not be enabled by default", name)
	}
}

func TestCosmosRestPathMatching(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	cases := []struct {
		path         string
		wantTemplate string
		wantParams   []string
	}{
		// tendermint URI calls sit at the root
		{"GET#/status", "GET#/status", nil},
		{"GET#/block", "GET#/block", nil},
		{"GET#/abci_query", "GET#/abci_query", nil},
		// cosmos LCD routes
		{"GET#/cosmos/base/tendermint/v1beta1/blocks/latest", "GET#/cosmos/base/tendermint/v1beta1/blocks/latest", nil},
		{"GET#/cosmos/base/tendermint/v1beta1/blocks/25000000", "GET#/cosmos/base/tendermint/v1beta1/blocks/*", []string{"25000000"}},
		{"GET#/cosmos/bank/v1beta1/balances/cosmos1abc", "GET#/cosmos/bank/v1beta1/balances/*", []string{"cosmos1abc"}},
		{"GET#/cosmos/bank/v1beta1/balances/cosmos1abc/by_denom", "GET#/cosmos/bank/v1beta1/balances/*/by_denom", []string{"cosmos1abc"}},
		{"GET#/cosmos/staking/v1beta1/validators/cosmosvaloper1x/delegations/cosmos1y/unbonding_delegation", "GET#/cosmos/staking/v1beta1/validators/*/delegations/*/unbonding_delegation", []string{"cosmosvaloper1x", "cosmos1y"}},
		{"POST#/cosmos/tx/v1beta1/txs", "POST#/cosmos/tx/v1beta1/txs", nil},
		{"GET#/cosmos/tx/v1beta1/txs/ABCDEF", "GET#/cosmos/tx/v1beta1/txs/*", []string{"ABCDEF"}},
		{"GET#/cosmos/tx/v1beta1/txs/block/123", "GET#/cosmos/tx/v1beta1/txs/block/*", []string{"123"}},
		// literal segments win over the wildcard sibling
		{"GET#/cosmwasm/wasm/v1/contract/build_address", "GET#/cosmwasm/wasm/v1/contract/build_address", nil},
		{"GET#/cosmwasm/wasm/v1/contract/cosmos1c", "GET#/cosmwasm/wasm/v1/contract/*", []string{"cosmos1c"}},
		{"GET#/cosmwasm/wasm/v1/contract/cosmos1c/smart/eyJhIjoxfQ==", "GET#/cosmwasm/wasm/v1/contract/*/smart/*", []string{"cosmos1c", "eyJhIjoxfQ=="}},
		{"GET#/ibc/core/channel/v1/channels/channel-0/ports/transfer/packet_commitments/7/unreceived_acks", "GET#/ibc/core/channel/v1/channels/*/ports/*/packet_commitments/*/unreceived_acks", []string{"channel-0", "transfer", "7"}},
	}
	for _, c := range cases {
		template, params, ok := specs.MatchRestMethod("cosmos", c.path)
		assert.True(t, ok, "expected %s to match", c.path)
		assert.Equal(t, c.wantTemplate, template, "template for %s", c.path)
		assert.Equal(t, c.wantParams, params, "params for %s", c.path)
	}
}
