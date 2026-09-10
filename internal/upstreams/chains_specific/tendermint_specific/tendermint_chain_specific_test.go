package tendermint_specific_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/tendermint_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/tendermint_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/tendermint_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// matchTendermintJsonRpc asserts the probe went out as a JSON-RPC call - the
// tendermint connector picks its wire shape from the request type, and every
// internal probe must use JSON-RPC so the shared response path unwraps
// CometBFT's result envelope.
func matchTendermintJsonRpc(method string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == method && req.RequestType() == protocol.JsonRpc
	}
}

func tendermintOK(result string) protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(result), protocol.JsonRpc)
}

// tendermintBlockJSON renders the shape CometBFT's `block` result has: decimal
// string heights, uppercase hex hashes.
func tendermintBlockJSON(height uint64, hash, parentHash string) string {
	return fmt.Sprintf(
		`{"block_id":{"hash":"%s"},"block":{"header":{"height":"%d","time":"2026-07-27T10:00:00Z","last_block_id":{"hash":"%s"}}}}`,
		hash, height, parentHash,
	)
}

func tendermintStatusJSON(network, version, earliest string, catchingUp bool) string {
	return fmt.Sprintf(
		`{"node_info":{"network":"%s","version":"%s"},"sync_info":{"latest_block_height":"100","earliest_block_height":"%s","catching_up":%t}}`,
		network, version, earliest, catchingUp,
	)
}

func tendermintOptions(validatePeers, validateSyncing bool) *chains.Options {
	return &chains.Options{
		InternalTimeout:        time.Second,
		ValidationInterval:     time.Second,
		MinPeers:               1,
		ValidatePeers:          new(validatePeers),
		ValidateSyncing:        new(validateSyncing),
		DisableChainValidation: new(false),
	}
}

func freshTendermint(t *testing.T, connector *mocks.ConnectorMock, opts *chains.Options) *tendermint_specific.TendermintChainSpecific {
	t.Helper()
	if opts == nil {
		opts = tendermintOptions(false, false)
	}
	chain := chains.GetChain("cosmos-hub")
	require.NotNil(t, chain)
	cs, err := tendermint_specific.NewTendermintSpecific(
		context.Background(),
		"upstream-id",
		connector,
		chain,
		100*time.Millisecond,
		opts,
	)
	require.NoError(t, err)
	require.NotNil(t, cs)
	return cs
}

// ---------- constructor ----------

func TestNewTendermintSpecificRejectsOtherConnectors(t *testing.T) {
	for _, connectorType := range []specs.ApiConnectorType{
		specs.RestConnector, specs.JsonRpcConnector, specs.WebsocketConnector,
	} {
		cs, err := tendermint_specific.NewTendermintSpecific(
			context.Background(),
			"id",
			mocks.NewConnectorMockWithType(connectorType),
			chains.GetChain("cosmos-hub"),
			time.Second,
			tendermintOptions(false, false),
		)
		assert.Nil(t, cs)
		assert.ErrorContains(t, err, "tendermint specific supports only the tendermint connector")
	}
}

func TestNewTendermintSpecificNilConnector(t *testing.T) {
	cs, err := tendermint_specific.NewTendermintSpecific(
		context.Background(),
		"id",
		nil,
		chains.GetChain("cosmos-hub"),
		time.Second,
		tendermintOptions(false, false),
	)
	assert.Nil(t, cs)
	assert.ErrorContains(t, err, "no connector")
}

// ---------- head / blocks ----------

func TestTendermintGetLatestBlock(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTendermintJsonRpc("block"))).
		Return(tendermintOK(tendermintBlockJSON(25000000, "AABBCC", "DDEEFF"))).
		Once()

	cs := freshTendermint(t, connector, nil)
	block, err := cs.GetLatestBlock(context.Background())

	require.NoError(t, err)
	assert.Equal(t, uint64(25000000), block.Height)
	assert.Equal(t, blockchain.NewHashIdFromString("AABBCC"), block.Hash)
	assert.Equal(t, blockchain.NewHashIdFromString("DDEEFF"), block.ParentHash)
	connector.AssertExpectations(t)
}

// CometBFT commits are final on production, so the finalized block is the head.
func TestTendermintGetFinalizedBlockIsTheHead(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTendermintJsonRpc("block"))).
		Return(tendermintOK(tendermintBlockJSON(42, "AA", "BB"))).
		Twice()

	cs := freshTendermint(t, connector, nil)
	latest, err := cs.GetLatestBlock(context.Background())
	require.NoError(t, err)
	finalized, err := cs.GetFinalizedBlock(context.Background())
	require.NoError(t, err)

	assert.Equal(t, latest, finalized)
	connector.AssertExpectations(t)
}

func TestTendermintGetLatestBlockPropagatesError(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
	connector.
		On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(-32603, "boom", nil))).
		Once()

	cs := freshTendermint(t, connector, nil)
	block, err := cs.GetLatestBlock(context.Background())

	assert.True(t, block.IsFullEmpty())
	assert.Error(t, err)
	connector.AssertExpectations(t)
}

func TestTendermintParseBlockRejectsGarbage(t *testing.T) {
	cs := freshTendermint(t, mocks.NewConnectorMockWithType(specs.TendermintConnector), nil)

	for _, payload := range []string{
		`not json`,
		`{}`,
		`{"block":{"header":{"height":"0"}}}`,
		`{"block":{"header":{"height":"abc"}}}`,
	} {
		block, err := cs.ParseBlock([]byte(payload))
		assert.True(t, block.IsFullEmpty(), payload)
		assert.Error(t, err, payload)
	}
}

// ---------- subscriptions ----------

func TestTendermintHeadSubscriptionsUnsupported(t *testing.T) {
	cs := freshTendermint(t, mocks.NewConnectorMockWithType(specs.TendermintConnector), nil)

	req, err := cs.SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.ErrorIs(t, err, blocks.ErrUnsupportedHeadSubscriptions)

	block, err := cs.ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorIs(t, err, blocks.ErrUnsupportedHeadSubscriptions)
}

// ---------- validators ----------

func TestTendermintHealthValidatorsFollowOptions(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)

	assert.Empty(t, freshTendermint(t, connector, tendermintOptions(false, false)).HealthValidators())

	syncingOnly := freshTendermint(t, connector, tendermintOptions(false, true)).HealthValidators()
	require.Len(t, syncingOnly, 1)
	assert.IsType(t, &tendermint_validations.TendermintSyncingValidator{}, syncingOnly[0])

	peersOnly := freshTendermint(t, connector, tendermintOptions(true, false)).HealthValidators()
	require.Len(t, peersOnly, 1)
	assert.IsType(t, &tendermint_validations.TendermintPeersValidator{}, peersOnly[0])

	both := freshTendermint(t, connector, tendermintOptions(true, true)).HealthValidators()
	assert.Len(t, both, 2)
}

func TestTendermintSyncingValidatorReadsCatchingUp(t *testing.T) {
	cases := []struct {
		catchingUp bool
		want       protocol.AvailabilityStatus
	}{
		{catchingUp: false, want: protocol.Available},
		{catchingUp: true, want: protocol.Syncing},
	}
	for _, c := range cases {
		connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
		connector.
			On("SendRequest", mock.Anything, mock.MatchedBy(matchTendermintJsonRpc("status"))).
			Return(tendermintOK(tendermintStatusJSON("cosmoshub-4", "0.38.17", "1", c.catchingUp))).
			Once()

		validator := tendermint_validations.NewTendermintSyncingValidator(
			"id", chains.GetChain("cosmos-hub").Chain, connector, time.Second,
		)
		assert.Equal(t, c.want, validator.Validate())
		connector.AssertExpectations(t)
	}
}

func TestTendermintPeersValidatorParsesStringPeerCount(t *testing.T) {
	// CometBFT renders n_peers as a decimal string, not a number.
	connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTendermintJsonRpc("net_info"))).
		Return(tendermintOK(`{"listening":true,"n_peers":"0"}`)).
		Once()

	options := tendermintOptions(true, false)
	validator := tendermint_validations.NewTendermintPeersValidator(
		"id", chains.GetChain("cosmos-hub").Chain, connector, options,
	)
	assert.Equal(t, protocol.Immature, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTendermintChainValidator(t *testing.T) {
	chain := chains.GetChain("cosmos-hub")
	require.Equal(t, "cosmoshub-4", chain.ChainId)

	cases := []struct {
		name    string
		network string
		want    validations.ValidationSettingResult
	}{
		{name: "match", network: "cosmoshub-4", want: validations.Valid},
		{name: "case insensitive", network: "CosmosHub-4", want: validations.Valid},
		{name: "wrong network", network: "osmosis-1", want: validations.FatalSettingError},
		// An upstream that answers `status` but reports no network cannot prove
		// which chain it serves, so it is refused outright rather than retried.
		{name: "empty network", network: "", want: validations.FatalSettingError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(matchTendermintJsonRpc("status"))).
				Return(tendermintOK(tendermintStatusJSON(c.network, "0.38.17", "1", false))).
				Once()

			validator := tendermint_validations.NewTendermintChainValidator("id", connector, chain, time.Second)
			assert.Equal(t, c.want, validator.Validate())
			connector.AssertExpectations(t)
		})
	}
}

func TestTendermintSettingsValidators(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)

	enabled := freshTendermint(t, connector, tendermintOptions(false, false)).SettingsValidators()
	require.Len(t, enabled, 1)
	assert.IsType(t, &tendermint_validations.TendermintChainValidator{}, enabled[0])

	options := tendermintOptions(false, false)
	options.DisableChainValidation = new(true)
	assert.Empty(t, freshTendermint(t, connector, options).SettingsValidators())
}

// ---------- lower bounds / labels / processors ----------

func TestTendermintLowerBoundFromEarliestBlockHeight(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTendermintJsonRpc("status"))).
		Return(tendermintOK(tendermintStatusJSON("cosmoshub-4", "0.38.17", "17654321", false))).
		Once()

	detector := tendermint_bounds.NewTendermintLowerBoundDetector(
		"id", chains.GetChain("cosmos-hub").Chain, time.Second, connector,
	)
	bounds, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, bounds, 1)
	assert.Equal(t, int64(17654321), bounds[0].Bound)
	assert.Equal(t, protocol.StateBound, bounds[0].Type)
	connector.AssertExpectations(t)
}

// earliest_block_height is reported verbatim, including 0 - the detector
// trusts what the node says rather than second-guessing it.
func TestTendermintLowerBoundReportsZeroVerbatim(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
	connector.
		On("SendRequest", mock.Anything, mock.Anything).
		Return(tendermintOK(tendermintStatusJSON("cosmoshub-4", "0.38.17", "0", false))).
		Once()

	detector := tendermint_bounds.NewTendermintLowerBoundDetector(
		"id", chains.GetChain("cosmos-hub").Chain, time.Second, connector,
	)
	bounds, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, bounds, 1)
	assert.Equal(t, int64(0), bounds[0].Bound)
	assert.Equal(t, protocol.StateBound, bounds[0].Type)
}

// A height that isn't a decimal number is a genuine protocol violation and
// still fails, so the router keeps the previous value instead of taking a
// garbage bound.
func TestTendermintLowerBoundRejectsUnparseableHeight(t *testing.T) {
	for _, earliest := range []string{"", "abc", "0x10"} {
		connector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
		connector.
			On("SendRequest", mock.Anything, mock.Anything).
			Return(tendermintOK(tendermintStatusJSON("cosmoshub-4", "0.38.17", earliest, false))).
			Once()

		detector := tendermint_bounds.NewTendermintLowerBoundDetector(
			"id", chains.GetChain("cosmos-hub").Chain, time.Second, connector,
		)
		bounds, err := detector.DetectLowerBound(context.Background())

		assert.Nil(t, bounds, earliest)
		assert.ErrorContains(t, err, "earliest_block_height", earliest)
	}
}

// The tendermint detector only claims STATE: the earliest retained height is
// exactly what `status` reports, with no inference about other bound types.
func TestTendermintLowerBoundSupportedTypes(t *testing.T) {
	detector := tendermint_bounds.NewTendermintLowerBoundDetector(
		"id", chains.GetChain("cosmos-hub").Chain, time.Second,
		mocks.NewConnectorMockWithType(specs.TendermintConnector),
	)
	assert.Equal(t, []protocol.LowerBoundType{protocol.StateBound}, detector.SupportedTypes())
}

func TestTendermintProcessorsArePresent(t *testing.T) {
	cs := freshTendermint(t, mocks.NewConnectorMockWithType(specs.TendermintConnector), nil)

	assert.NotNil(t, cs.LowerBoundProcessor())
	assert.NotNil(t, cs.LabelsProcessor())
	assert.NotNil(t, cs.BlockProcessor())
}
