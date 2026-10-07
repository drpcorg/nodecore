package tron_specific_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/tron_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/tron_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
	tronapi "github.com/drpcorg/public/pkg/tron/api"
	troncore "github.com/drpcorg/public/pkg/tron/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func freshTronGrpc(t *testing.T, connector *mocks.ConnectorMock, opts *chains.Options) *tron_specific.TronGrpcSpecific {
	t.Helper()
	if opts == nil {
		opts = tronOptions(false, false)
	}
	cs, err := tron_specific.NewTronSpecific(
		context.Background(), "upstream-id", connector, chains.GetChain("tron"), 100*time.Millisecond, opts, nil,
	)
	require.NoError(t, err)
	grpcSpecific, ok := cs.(*tron_specific.TronGrpcSpecific)
	require.True(t, ok, "grpc connector should yield *TronGrpcSpecific")
	return grpcSpecific
}

func matchTronGrpc(method string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == method && req.RequestType() == protocol.Grpc
	}
}

// grpcHash builds a deterministic 32-byte hash out of a seed, the way the
// gRPC API reports block ids - raw bytes.
func grpcHash(seed byte) []byte {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	return raw
}

func tronGrpcBlockBytes(t *testing.T, height int64, hash, parentHash []byte) []byte {
	t.Helper()
	raw, err := proto.Marshal(&tronapi.BlockExtention{
		Blockid: hash,
		BlockHeader: &troncore.BlockHeader{
			RawData: &troncore.BlockHeaderRaw{Number: height, Timestamp: 1_700_000_000_000, ParentHash: parentHash},
		},
	})
	require.NoError(t, err)
	return raw
}

func tronGrpcNodeInfoBytes(t *testing.T, solidityBlock string) []byte {
	t.Helper()
	raw, err := proto.Marshal(&troncore.NodeInfo{SolidityBlock: solidityBlock})
	require.NoError(t, err)
	return raw
}

func tronGrpcError(method string, code codes.Code) protocol.ResponseHolder {
	return protocol.NewGrpcUpstreamErrorResponse(
		protocol.NewInternalUpstreamGrpcRequest(method, nil, chains.TRON),
		&protocol.GrpcStatus{Code: code, Message: "boom"},
	)
}

// ---------- dispatch ----------

func TestNewTronSpecificDispatchesGrpc(t *testing.T) {
	assert.NotNil(t, freshTronGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil))
}

func TestNewTronSpecificRejectsGrpcAdditional(t *testing.T) {
	cs, err := tron_specific.NewTronSpecific(
		context.Background(), "id", mocks.NewConnectorMockWithType(specs.GrpcAdditional),
		chains.GetChain("tron"), time.Second, tronOptions(false, false), nil,
	)
	assert.Nil(t, cs)
	assert.ErrorContains(t, err, "tron specific supports only")
}

// ---------- head / blocks ----------

func TestTronGrpcGetLatestBlock(t *testing.T) {
	hash, parentHash := grpcHash(1), grpcHash(2)
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpc("/protocol.Wallet/GetBlock"))).
		Return(protocol.NewGrpcUpstreamResponse("1", tronGrpcBlockBytes(t, 75_000_000, hash, parentHash))).
		Once()

	block, err := freshTronGrpc(t, connector, nil).GetLatestBlock(context.Background())

	require.NoError(t, err)
	assert.Equal(t, protocol.NewBlock(75_000_000, 0, blockchain.NewHashIdFromBytes(hash), blockchain.NewHashIdFromBytes(parentHash)), block)
	connector.AssertExpectations(t)
}

func TestTronGrpcGetLatestBlockConnectorError(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpc("/protocol.Wallet/GetBlock"))).
		Return(tronGrpcError("/protocol.Wallet/GetBlock", codes.Unavailable)).
		Once()

	block, err := freshTronGrpc(t, connector, nil).GetLatestBlock(context.Background())

	assert.True(t, block.IsFullEmpty())
	assert.Error(t, err)
}

func TestTronGrpcParseBlockRejectsHeaderless(t *testing.T) {
	cs := freshTronGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil)

	block, err := cs.ParseBlock(nil)
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "no header")

	block, err = cs.ParseBlock([]byte{0xff, 0xff, 0xff})
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "unparseable")
}

func TestTronGrpcGetFinalizedBlock(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpc("/protocol.Wallet/GetNodeInfo"))).
		Return(protocol.NewGrpcUpstreamResponse("1", tronGrpcNodeInfoBytes(t, "Num:74999981,ID:0000000004781f40aabbcc"))).
		Once()

	block, err := freshTronGrpc(t, connector, nil).GetFinalizedBlock(context.Background())

	require.NoError(t, err)
	assert.Equal(t, protocol.NewBlockWithHeight(74_999_981), block)
	connector.AssertExpectations(t)
}

func TestTronGrpcGetFinalizedBlockBadSolidityString(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpc("/protocol.Wallet/GetNodeInfo"))).
		Return(protocol.NewGrpcUpstreamResponse("1", tronGrpcNodeInfoBytes(t, "garbage"))).
		Once()

	block, err := freshTronGrpc(t, connector, nil).GetFinalizedBlock(context.Background())

	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "invalid solidity block")
}

func TestTronGrpcHeadSubscriptionsUnsupported(t *testing.T) {
	cs := freshTronGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil)

	req, err := cs.SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.ErrorIs(t, err, blocks.ErrUnsupportedHeadSubscriptions)

	block, err := cs.ParseSubscriptionBlock(nil)
	assert.True(t, block.IsFullEmpty())
	assert.ErrorIs(t, err, blocks.ErrUnsupportedHeadSubscriptions)
}

// ---------- validators / processors ----------

func TestTronGrpcHealthValidatorsFollowOptions(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)

	assert.Empty(t, freshTronGrpc(t, connector, tronOptions(false, false)).HealthValidators())

	validators := freshTronGrpc(t, connector, tronOptions(true, true)).HealthValidators()
	require.Len(t, validators, 2)
	assert.IsType(t, &tron_validations.TronGrpcPeersValidator{}, validators[0])
	assert.IsType(t, &tron_validations.TronGrpcSyncingValidator{}, validators[1])
}

func TestTronGrpcNoSettingsValidatorsNoMethodsProcessor(t *testing.T) {
	cs := freshTronGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil)

	assert.Nil(t, cs.SettingsValidators())
	assert.Nil(t, cs.MethodsProcessor())
	assert.Nil(t, cs.CapDetectors(caps.DetectorInput{}), "tron has no websocket connector, nothing to detect")
	assert.False(t, cs.PauseHeadWhileSyncing())
	assert.NotNil(t, cs.LowerBoundProcessor())
	assert.NotNil(t, cs.LabelsProcessor())
	assert.NotNil(t, cs.BlockProcessor())
}

// ---------- hash parity ----------

// The same tron block id reaches nodecore three ways: 0x-prefixed hex from
// the json-rpc eth_getBlockByNumber (EVM specific), bare hex from the REST
// blockID, raw bytes from the gRPC blockid. All three must reduce to the same
// HashId - for the parent hash too - or the fork choice would see three
// different blocks for one height.
func TestTronHashEncodingsAgreeAcrossConnectors(t *testing.T) {
	pattern := func(prefix ...byte) []byte {
		raw := make([]byte, 32)
		for i := range raw {
			raw[i] = byte(i * 7)
		}
		copy(raw, prefix)
		return raw
	}
	cases := map[string][]byte{
		"random":          pattern(),
		"leading zeros":   pattern(0, 0, 0, 0),
		"ascii 0x prefix": pattern('0', 'x'),
		"ascii 0X prefix": pattern('0', 'X'),
	}

	evmSpecific, err := tron_specific.NewTronSpecific(
		context.Background(), "id", mocks.NewConnectorMockWithType(specs.JsonRpcConnector),
		chains.GetChain("tron"), time.Second, tronOptions(false, false), nil,
	)
	require.NoError(t, err)
	require.IsType(t, &evm_specific.EvmChainSpecificObject{}, evmSpecific)
	restSpecific := freshTron(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil)
	grpcSpecific := freshTronGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil)

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			parentRaw := pattern(9, 9, 9)
			bareHex, bareParentHex := hex.EncodeToString(raw), hex.EncodeToString(parentRaw)
			evmJSON := fmt.Sprintf(`{"hash":"0x%s","parentHash":"0x%s","number":"0x64"}`, bareHex, bareParentHex)

			fromJsonRpc, err := evmSpecific.ParseBlock([]byte(evmJSON))
			require.NoError(t, err)
			fromRest, err := restSpecific.ParseBlock([]byte(tronBlockJSON(100, bareHex, bareParentHex)))
			require.NoError(t, err)
			fromGrpc, err := grpcSpecific.ParseBlock(tronGrpcBlockBytes(t, 100, raw, parentRaw))
			require.NoError(t, err)

			assert.Equal(t, blockchain.HashId(raw), fromJsonRpc.Hash)
			assert.Equal(t, blockchain.HashId(parentRaw), fromJsonRpc.ParentHash)
			assert.Equal(t, fromJsonRpc, fromRest)
			assert.Equal(t, fromJsonRpc, fromGrpc)
		})
	}
}
