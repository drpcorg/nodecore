package tron_specific_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/tron_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/tron_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// matchTronRest builds a mock matcher that asserts the request is a REST
// request whose "VERB#/path" method tag matches and whose body equals the
// expected bytes (use "" for a nil/empty body).
func matchTronRest(method, expectedBody string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		if req.Method() != method {
			return false
		}
		if req.RequestType() != protocol.Rest {
			return false
		}
		body, err := req.Body()
		if err != nil {
			return false
		}
		return string(body) == expectedBody
	}
}

func tronOK(body string) protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.Rest)
}

func tronBlockJSON(height uint64, blockID, parentHash string) string {
	return fmt.Sprintf(
		`{"blockID":"%s","block_header":{"raw_data":{"number":%d,"parentHash":"%s"}}}`,
		blockID, height, parentHash,
	)
}

func tronOptions(validatePeers, validateSyncing bool) *chains.Options {
	return &chains.Options{
		InternalTimeout:    time.Second,
		ValidationInterval: time.Second,
		MinPeers:           1,
		ValidatePeers:      new(validatePeers),
		ValidateSyncing:    new(validateSyncing),
	}
}

// freshTron constructs a TRON ChainSpecific via the public dispatcher with
// a REST-typed mock connector, mirroring how the upstream factory wires it.
func freshTron(t *testing.T, connector *mocks.ConnectorMock, opts *chains.Options) chains_specific.ChainSpecific {
	t.Helper()
	if opts == nil {
		opts = tronOptions(false, false)
	}
	chain := chains.GetChain("tron")
	require.NotNil(t, chain, "tron chain must be registered")
	cs, err := tron_specific.NewTronSpecific(
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

// ---------- Stubs (WebSocket out of scope) ----------

func TestTronSubscribeHeadRequest(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)

	req, err := cs.SubscribeHeadRequest()

	assert.Nil(t, req)
	assert.NoError(t, err)
}

func TestTronParseSubscriptionBlock(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)

	block, err := cs.ParseSubscriptionBlock([]byte(`{}`))

	assert.NoError(t, err)
	assert.True(t, block.IsFullEmpty())
}

// ---------- ParseBlock ----------

func TestTronParseBlock(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)

	block, err := cs.ParseBlock([]byte(tronBlockJSON(12345, "0xabc", "0xdef")))

	require.NoError(t, err)
	expected := protocol.NewBlock(
		12345, 0,
		blockchain.NewHashIdFromString("0xabc"),
		blockchain.NewHashIdFromString("0xdef"),
	)
	assert.Equal(t, expected, block)
}

func TestTronParseBlockInvalidJSON(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)

	block, err := cs.ParseBlock([]byte(`not json`))

	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the tron block")
}

func TestTronParseBlockEmptyObjectYieldsZeroBlock(t *testing.T) {
	// `{}` is the documented "no such block" signal from TRON, but
	// ParseBlock itself doesn't error — Unmarshal succeeds with zero
	// values. Pin that behavior so a future change is noticed.
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)

	block, err := cs.ParseBlock([]byte(`{}`))

	require.NoError(t, err)
	assert.Equal(t, uint64(0), block.Height)
}

// ---------- GetLatestBlock ----------

func TestTronGetLatestBlock(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(
			matchTronRest("POST#/wallet/getblock", `{"detail": false}`),
		)).
		Return(tronOK(tronBlockJSON(777, "0xhash", "0xparent"))).
		Once()

	cs := freshTron(t, connector, nil)
	block, err := cs.GetLatestBlock(context.Background())

	require.NoError(t, err)
	expected := protocol.NewBlock(
		777, 0,
		blockchain.NewHashIdFromString("0xhash"),
		blockchain.NewHashIdFromString("0xparent"),
	)
	assert.Equal(t, expected, block)
	connector.AssertExpectations(t)
}

func TestTronGetLatestBlockConnectorError(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(
			matchTronRest("POST#/wallet/getblock", `{"detail": false}`),
		)).
		Return(protocol.NewHttpUpstreamResponseWithError(
			protocol.ResponseErrorWithData(7, "boom", nil),
		)).
		Once()

	cs := freshTron(t, connector, nil)
	block, err := cs.GetLatestBlock(context.Background())

	assert.True(t, block.IsFullEmpty())
	var respErr *protocol.ResponseError
	require.True(t, errors.As(err, &respErr))
	assert.Equal(t, "boom", respErr.Message)
	connector.AssertExpectations(t)
}

func TestTronGetLatestBlockMalformedBody(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(
			matchTronRest("POST#/wallet/getblock", `{"detail": false}`),
		)).
		Return(tronOK(`not json`)).
		Once()

	cs := freshTron(t, connector, nil)
	block, err := cs.GetLatestBlock(context.Background())

	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the tron block")
	connector.AssertExpectations(t)
}

// ---------- GetFinalizedBlock ----------

func TestTronGetFinalizedBlock(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(
			matchTronRest("POST#/wallet/getnodeinfo", ""),
		)).
		Return(tronOK(`{"solidityBlock":"Num:1234,ID:0xdeadbeef"}`)).
		Once()

	cs := freshTron(t, connector, nil)
	block, err := cs.GetFinalizedBlock(context.Background())

	require.NoError(t, err)
	assert.Equal(t, uint64(1234), block.Height)
	connector.AssertExpectations(t)
}

func TestTronGetFinalizedBlockConnectorError(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(
			matchTronRest("POST#/wallet/getnodeinfo", ""),
		)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).
		Once()

	cs := freshTron(t, connector, nil)
	block, err := cs.GetFinalizedBlock(context.Background())

	assert.True(t, block.IsFullEmpty())
	assert.Error(t, err)
	connector.AssertExpectations(t)
}

func TestTronGetFinalizedBlockEmptySolidityBlock(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(
			matchTronRest("POST#/wallet/getnodeinfo", ""),
		)).
		Return(tronOK(`{"solidityBlock":""}`)).
		Once()

	cs := freshTron(t, connector, nil)
	block, err := cs.GetFinalizedBlock(context.Background())

	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "invalid solidity block")
	connector.AssertExpectations(t)
}

func TestTronGetFinalizedBlockMalformedNumber(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(
			matchTronRest("POST#/wallet/getnodeinfo", ""),
		)).
		Return(tronOK(`{"solidityBlock":"Num:notanumber,ID:0xff"}`)).
		Once()

	cs := freshTron(t, connector, nil)
	block, err := cs.GetFinalizedBlock(context.Background())

	assert.True(t, block.IsFullEmpty())
	assert.Error(t, err)
	connector.AssertExpectations(t)
}

// ---------- HealthValidators ----------

func TestTronHealthValidatorsEmptyWhenBothDisabled(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), tronOptions(false, false))
	assert.Empty(t, cs.HealthValidators())
}

func TestTronHealthValidatorsPeersOnly(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), tronOptions(true, false))
	got := cs.HealthValidators()
	require.Len(t, got, 1)
	_, ok := got[0].(*tron_validations.TronPeersValidator)
	assert.True(t, ok, "single validator should be *TronPeersValidator")
}

func TestTronHealthValidatorsSyncingOnly(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), tronOptions(false, true))
	got := cs.HealthValidators()
	require.Len(t, got, 1)
	_, ok := got[0].(*tron_validations.TronSyncingValidator)
	assert.True(t, ok, "single validator should be *TronSyncingValidator")
}

func TestTronHealthValidatorsBothEnabled(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), tronOptions(true, true))
	got := cs.HealthValidators()
	require.Len(t, got, 2)
	_, peersOk := got[0].(*tron_validations.TronPeersValidator)
	_, syncOk := got[1].(*tron_validations.TronSyncingValidator)
	assert.True(t, peersOk)
	assert.True(t, syncOk)
}

// ---------- SettingsValidators / LowerBoundProcessor / LabelsProcessor ----------

func TestTronSettingsValidatorsIsNil(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)
	assert.Nil(t, cs.SettingsValidators())
}

func TestTronLowerBoundProcessorIsNonNil(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)
	assert.NotNil(t, cs.LowerBoundProcessor())
}

func TestTronLabelsProcessorIsNonNil(t *testing.T) {
	cs := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)
	assert.NotNil(t, cs.LabelsProcessor())
}

// ---------- NewTronSpecific dispatch ----------

func TestNewTronSpecificDispatchesRest(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	cs, err := tron_specific.NewTronSpecific(
		context.Background(),
		"id",
		connector,
		chains.GetChain("tron"),
		time.Second,
		tronOptions(false, false),
	)
	require.NoError(t, err)
	_, ok := cs.(*tron_specific.TronRestSpecific)
	assert.True(t, ok, "REST connector should yield *TronRestSpecific")
}

func TestNewTronSpecificDispatchesJsonRpcToEvm(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.JsonRpcConnector)
	cs, err := tron_specific.NewTronSpecific(
		context.Background(),
		"id",
		connector,
		chains.GetChain("tron"),
		time.Second,
		tronOptions(false, false),
	)
	require.NoError(t, err)
	_, ok := cs.(*evm_specific.EvmChainSpecificObject)
	assert.True(t, ok, "JSON-RPC connector should delegate to *EvmChainSpecificObject")
}

func TestNewTronSpecificUnsupportedConnector(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.WebsocketConnector)
	cs, err := tron_specific.NewTronSpecific(
		context.Background(),
		"id",
		connector,
		chains.GetChain("tron"),
		time.Second,
		tronOptions(false, false),
	)
	assert.Nil(t, cs)
	assert.ErrorContains(t, err, "tron specific supports only")
}

func TestNewTronSpecificNilConnector(t *testing.T) {
	cs, err := tron_specific.NewTronSpecific(
		context.Background(),
		"id",
		nil,
		chains.GetChain("tron"),
		time.Second,
		tronOptions(false, false),
	)
	assert.Nil(t, cs)
	assert.ErrorContains(t, err, "no connector")
}
