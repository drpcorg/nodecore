package cosmos_specific_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/cosmos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/tendermint_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/cosmos_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/cosmos_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func matchCosmosRest(methodTemplate string, pathParams ...string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		if req.Method() != methodTemplate || req.RequestType() != protocol.Rest {
			return false
		}
		restReq, ok := req.(*protocol.UpstreamRestRequest)
		if !ok {
			return false
		}
		var got []string
		if params := restReq.RequestParams(); params != nil {
			got = params.PathParams
		}
		if len(got) != len(pathParams) {
			return false
		}
		for i := range got {
			if got[i] != pathParams[i] {
				return false
			}
		}
		return true
	}
}

func cosmosOK(body string) protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.Rest)
}

func cosmosErr(code int, message string) protocol.ResponseHolder {
	return protocol.NewHttpUpstreamResponse("1", []byte(fmt.Sprintf(`{"code":3,"message":%q}`, message)), code, protocol.Rest)
}

// cosmosBlockJSON renders the LCD block envelope: decimal string heights,
// base64 hashes.
func cosmosBlockJSON(height uint64, hash, parentHash string) string {
	return fmt.Sprintf(
		`{"block_id":{"hash":"%s"},"block":{"header":{"height":"%d","time":"2026-07-27T10:00:00Z","last_block_id":{"hash":"%s"}}}}`,
		hash, height, parentHash,
	)
}

func cosmosNodeInfoJSON(network, cometVersion, appVersion string) string {
	return fmt.Sprintf(
		`{"default_node_info":{"network":"%s","version":"%s"},"application_version":{"name":"gaia","version":"%s"}}`,
		network, cometVersion, appVersion,
	)
}

func cosmosOptions(validateSyncing bool) *chains.Options {
	return &chains.Options{
		InternalTimeout:        time.Second,
		ValidationInterval:     time.Second,
		MinPeers:               1,
		ValidatePeers:          new(false),
		ValidateSyncing:        new(validateSyncing),
		DisableChainValidation: new(false),
	}
}

func freshCosmosRest(t *testing.T, connector *mocks.ConnectorMock, opts *chains.Options) *cosmos_specific.CosmosRestSpecific {
	t.Helper()
	if opts == nil {
		opts = cosmosOptions(false)
	}
	cs, err := cosmos_specific.NewCosmosSpecific(
		context.Background(),
		"upstream-id",
		connector,
		chains.GetChain("cosmos-hub"),
		100*time.Millisecond,
		opts,
	)
	require.NoError(t, err)
	restSpecific, ok := cs.(*cosmos_specific.CosmosRestSpecific)
	require.True(t, ok)
	return restSpecific
}

// ---------- dispatch ----------

func TestNewCosmosSpecificDispatchesOnConnectorType(t *testing.T) {
	chain := chains.GetChain("cosmos-hub")

	tendermintCs, err := cosmos_specific.NewCosmosSpecific(
		context.Background(), "id",
		mocks.NewConnectorMockWithType(specs.TendermintConnector),
		chain, time.Second, cosmosOptions(false),
	)
	require.NoError(t, err)
	assert.IsType(t, &tendermint_specific.TendermintChainSpecific{}, tendermintCs)

	restCs, err := cosmos_specific.NewCosmosSpecific(
		context.Background(), "id",
		mocks.NewConnectorMockWithType(specs.RestConnector),
		chain, time.Second, cosmosOptions(false),
	)
	require.NoError(t, err)
	assert.IsType(t, &cosmos_specific.CosmosRestSpecific{}, restCs)
}

func TestNewCosmosSpecificUnsupportedConnector(t *testing.T) {
	for _, connectorType := range []specs.ApiConnectorType{
		specs.WebsocketConnector, specs.JsonRpcConnector, specs.RestIndexer,
	} {
		cs, err := cosmos_specific.NewCosmosSpecific(
			context.Background(), "id",
			mocks.NewConnectorMockWithType(connectorType),
			chains.GetChain("cosmos-hub"), time.Second, cosmosOptions(false),
		)
		assert.Nil(t, cs)
		assert.ErrorContains(t, err, "cosmos specific supports only tendermint or rest connector")
	}
}

func TestNewCosmosSpecificNilConnector(t *testing.T) {
	cs, err := cosmos_specific.NewCosmosSpecific(
		context.Background(), "id", nil,
		chains.GetChain("cosmos-hub"), time.Second, cosmosOptions(false),
	)
	assert.Nil(t, cs)
	assert.ErrorContains(t, err, "no connector")
}

// ---------- head / blocks ----------

func TestCosmosRestGetLatestBlock(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosRest(specific_helpers.CosmosLatestBlockRoute))).
		Return(cosmosOK(cosmosBlockJSON(25000000, base64Hash("aa"), base64Hash("bb")))).
		Once()

	cs := freshCosmosRest(t, connector, nil)
	block, err := cs.GetLatestBlock(context.Background())

	require.NoError(t, err)
	assert.Equal(t, uint64(25000000), block.Height)
	connector.AssertExpectations(t)
}

// The two connectors report the same block hash in different encodings -
// uppercase hex over the CometBFT RPC, base64 over the LCD. Both must reduce
// to the same HashId, otherwise two upstreams of one chain would appear to
// disagree about the head.
func TestCosmosHashEncodingsAgreeAcrossConnectors(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i * 7)
	}
	hexHash := strings.ToUpper(hex.EncodeToString(raw))
	base64Hash := base64.StdEncoding.EncodeToString(raw)
	require.NotEqual(t, hexHash, base64Hash)

	tendermintConnector := mocks.NewConnectorMockWithType(specs.TendermintConnector)
	tendermintSpecific, err := tendermint_specific.NewTendermintSpecific(
		context.Background(), "id", tendermintConnector,
		chains.GetChain("cosmos-hub"), time.Second, cosmosOptions(false),
	)
	require.NoError(t, err)

	fromRpc, err := tendermintSpecific.ParseBlock([]byte(cosmosBlockJSON(100, hexHash, hexHash)))
	require.NoError(t, err)
	fromLcd, err := freshCosmosRest(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil).
		ParseBlock([]byte(cosmosBlockJSON(100, base64Hash, base64Hash)))
	require.NoError(t, err)

	assert.Equal(t, fromRpc.Hash, fromLcd.Hash)
	assert.Equal(t, blockchain.NewHashIdFromBytes(raw), fromLcd.Hash)
	assert.Equal(t, fromRpc, fromLcd)
}

func TestCosmosRestGetFinalizedBlockIsTheHead(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosRest(specific_helpers.CosmosLatestBlockRoute))).
		Return(cosmosOK(cosmosBlockJSON(7, base64Hash("cc"), base64Hash("dd")))).
		Twice()

	cs := freshCosmosRest(t, connector, nil)
	latest, err := cs.GetLatestBlock(context.Background())
	require.NoError(t, err)
	finalized, err := cs.GetFinalizedBlock(context.Background())
	require.NoError(t, err)

	assert.Equal(t, latest, finalized)
	connector.AssertExpectations(t)
}

func TestCosmosRestParseBlockRejectsGarbage(t *testing.T) {
	cs := freshCosmosRest(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)

	for _, payload := range []string{`nope`, `{}`, `{"block":{"header":{"height":"0"}}}`} {
		block, err := cs.ParseBlock([]byte(payload))
		assert.True(t, block.IsFullEmpty(), payload)
		assert.Error(t, err, payload)
	}
}

func TestCosmosRestHeadSubscriptionsUnsupported(t *testing.T) {
	cs := freshCosmosRest(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)

	req, err := cs.SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.ErrorIs(t, err, blocks.ErrUnsupportedHeadSubscriptions)

	block, err := cs.ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorIs(t, err, blocks.ErrUnsupportedHeadSubscriptions)
}

// ---------- validators ----------

// The LCD exposes no peer set, so there is only ever the syncing validator.
func TestCosmosRestHealthValidators(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)

	assert.Empty(t, freshCosmosRest(t, connector, cosmosOptions(false)).HealthValidators())

	enabled := freshCosmosRest(t, connector, cosmosOptions(true)).HealthValidators()
	require.Len(t, enabled, 1)
	assert.IsType(t, &cosmos_validations.CosmosSyncingValidator{}, enabled[0])

	withPeers := cosmosOptions(true)
	withPeers.ValidatePeers = new(true)
	assert.Len(t, freshCosmosRest(t, connector, withPeers).HealthValidators(), 1)
}

func TestCosmosSyncingValidator(t *testing.T) {
	cases := []struct {
		body string
		want protocol.AvailabilityStatus
	}{
		{body: `{"syncing":false}`, want: protocol.Available},
		{body: `{"syncing":true}`, want: protocol.Syncing},
	}
	for _, c := range cases {
		connector := mocks.NewConnectorMockWithType(specs.RestConnector)
		connector.
			On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosRest(specific_helpers.CosmosSyncingRoute))).
			Return(cosmosOK(c.body)).
			Once()

		validator := cosmos_validations.NewCosmosSyncingValidator(
			"id", chains.GetChain("cosmos-hub").Chain, connector, time.Second,
		)
		assert.Equal(t, c.want, validator.Validate())
		connector.AssertExpectations(t)
	}
}

func TestCosmosChainValidator(t *testing.T) {
	chain := chains.GetChain("cosmos-hub")

	cases := []struct {
		name    string
		network string
		want    validations.ValidationSettingResult
	}{
		{name: "match", network: "cosmoshub-4", want: validations.Valid},
		{name: "case insensitive", network: "COSMOSHUB-4", want: validations.Valid},
		{name: "wrong network", network: "osmosis-1", want: validations.FatalSettingError},
		// An upstream that answers node_info but reports no network cannot prove
		// which chain it serves, so it is refused outright rather than retried.
		{name: "empty network", network: "", want: validations.FatalSettingError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			connector := mocks.NewConnectorMockWithType(specs.RestConnector)
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosRest(specific_helpers.CosmosNodeInfoRoute))).
				Return(cosmosOK(cosmosNodeInfoJSON(c.network, "0.38.17", "v21.0.0"))).
				Once()

			validator := cosmos_validations.NewCosmosChainValidator("id", connector, chain, time.Second)
			assert.Equal(t, c.want, validator.Validate())
			connector.AssertExpectations(t)
		})
	}
}

// ---------- lower bounds ----------

// The steady-state path: the previously found bound is re-confirmed with a
// single probe.
func TestCosmosLowerBoundSearchFindsFirstRetainedHeight(t *testing.T) {
	const retainedFrom = 24000000
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosRest(specific_helpers.CosmosLatestBlockRoute))).
		Return(cosmosOK(cosmosBlockJSON(25000000, base64Hash("aa"), base64Hash("bb"))))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(probedHeightMatches(func(h int64) bool { return h < retainedFrom }))).
		Return(cosmosErr(400, "could not find results for height #1 (lowest height is 24000000)"))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(probedHeightMatches(func(h int64) bool { return h >= retainedFrom }))).
		Return(cosmosOK(cosmosBlockJSON(retainedFrom, base64Hash("aa"), base64Hash("bb"))))

	detector := cosmos_bounds.NewCosmosLowerBoundDetector(
		"id", chains.GetChain("cosmos-hub").Chain, time.Second, connector,
	)
	detector.SetSearchRetryPolicy(1, time.Millisecond, time.Millisecond)

	bounds, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, bounds)
	assert.Equal(t, int64(retainedFrom), bounds[0].Bound)
	assert.Equal(t, protocol.StateBound, bounds[0].Type)
}

func TestCosmosRestProcessorsArePresent(t *testing.T) {
	cs := freshCosmosRest(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)

	assert.NotNil(t, cs.LowerBoundProcessor())
	assert.NotNil(t, cs.LabelsProcessor())
	assert.NotNil(t, cs.BlockProcessor())
}

// base64Hash builds a deterministic 32-byte base64 hash out of a seed, the way
// the LCD renders block ids.
func base64Hash(seed string) string {
	raw := make([]byte, 32)
	copy(raw, seed)
	return base64.StdEncoding.EncodeToString(raw)
}

// probedHeightMatches builds a mock matcher for a block-by-height probe whose
// captured height satisfies the predicate. Matchers must never assert, only
// report, so a non-probe request simply doesn't match.
func probedHeightMatches(predicate func(int64) bool) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		if req.Method() != specific_helpers.CosmosBlockByHeightRoute {
			return false
		}
		restReq, ok := req.(*protocol.UpstreamRestRequest)
		if !ok {
			return false
		}
		params := restReq.RequestParams()
		if params == nil || len(params.PathParams) != 1 {
			return false
		}
		height, err := strconv.ParseInt(params.PathParams[0], 10, 64)
		if err != nil {
			return false
		}
		return predicate(height)
	}
}
