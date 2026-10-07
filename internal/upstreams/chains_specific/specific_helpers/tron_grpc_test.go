package specific_helpers_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
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

func tronGrpcBlockBytes(t *testing.T, height, timestamp int64, hash, parentHash []byte) []byte {
	t.Helper()
	data, err := proto.Marshal(&tronapi.BlockExtention{
		Blockid: hash,
		BlockHeader: &troncore.BlockHeader{
			RawData: &troncore.BlockHeaderRaw{Number: height, Timestamp: timestamp, ParentHash: parentHash},
		},
	})
	require.NoError(t, err)
	return data
}

func matchTronGrpc(method string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == method && req.RequestType() == protocol.Grpc
	}
}

// The head poll is the gRPC twin of REST's POST /wallet/getblock {"detail": false}:
// no id_or_num means "latest", detail false drops the transaction list.
func TestTronGrpcNowBlockRequest(t *testing.T) {
	request, err := specific_helpers.TronGrpcNowBlockRequest(chains.TRON)
	require.NoError(t, err)

	assert.Equal(t, "/protocol.Wallet/GetBlock", request.Method())
	assert.Equal(t, protocol.Grpc, request.RequestType())
	body, err := request.Body()
	require.NoError(t, err)
	var msg tronapi.BlockReq
	require.NoError(t, proto.Unmarshal(body, &msg))
	assert.Equal(t, "", msg.GetIdOrNum())
	assert.False(t, msg.GetDetail())
}

func TestTronGrpcBlockByNumRequestEncodesTheHeight(t *testing.T) {
	request, err := specific_helpers.TronGrpcBlockByNumRequest(chains.TRON, 4242)
	require.NoError(t, err)

	assert.Equal(t, "/protocol.Wallet/GetBlock", request.Method())
	body, err := request.Body()
	require.NoError(t, err)
	var msg tronapi.BlockReq
	require.NoError(t, proto.Unmarshal(body, &msg))
	assert.Equal(t, "4242", msg.GetIdOrNum())
	assert.False(t, msg.GetDetail())
}

func TestFetchTronGrpcNowBlockReturnsRawBytes(t *testing.T) {
	raw := tronGrpcBlockBytes(t, 100, 1_700_000_000_000, []byte{1, 2}, []byte{3, 4})
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpc("/protocol.Wallet/GetBlock"))).
		Return(protocol.NewGrpcUpstreamResponse("1", raw)).
		Once()

	got, err := specific_helpers.FetchTronGrpcNowBlock(context.Background(), connector, chains.TRON)

	require.NoError(t, err)
	assert.Equal(t, raw, got)
	connector.AssertExpectations(t)
}

func TestFetchTronGrpcNowBlockPropagatesErrors(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewGrpcUpstreamErrorResponse(
			protocol.NewInternalUpstreamGrpcRequest("/protocol.Wallet/GetBlock", nil, chains.TRON),
			&protocol.GrpcStatus{Code: codes.Unavailable, Message: "unavailable"},
		)).
		Once()

	_, err := specific_helpers.FetchTronGrpcNowBlock(context.Background(), connector, chains.TRON)

	assert.Error(t, err)
}

func TestParseTronGrpcBlock(t *testing.T) {
	block, err := specific_helpers.ParseTronGrpcBlock(tronGrpcBlockBytes(t, 7, 9, []byte{1}, []byte{2}))
	require.NoError(t, err)
	assert.Equal(t, int64(7), block.GetBlockHeader().GetRawData().GetNumber())
	assert.Equal(t, []byte{1}, block.GetBlockid())

	_, err = specific_helpers.ParseTronGrpcBlock([]byte{0xff, 0xff, 0xff})
	assert.ErrorContains(t, err, "tron grpc block payload unparseable")
}

func TestFetchTronGrpcNodeInfo(t *testing.T) {
	raw, err := proto.Marshal(&troncore.NodeInfo{
		SolidityBlock:  "Num:99,ID:00",
		PeerInfoList:   []*troncore.NodeInfo_PeerInfo{{Host: "a"}, {Host: "b"}},
		ConfigNodeInfo: &troncore.NodeInfo_ConfigNodeInfo{CodeVersion: "4.8.2"},
	})
	require.NoError(t, err)
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpc("/protocol.Wallet/GetNodeInfo"))).
		Return(protocol.NewGrpcUpstreamResponse("1", raw)).
		Once()

	nodeInfo, err := specific_helpers.FetchTronGrpcNodeInfo(context.Background(), connector, chains.TRON)

	require.NoError(t, err)
	assert.Equal(t, "Num:99,ID:00", nodeInfo.GetSolidityBlock())
	assert.Len(t, nodeInfo.GetPeerInfoList(), 2)
	assert.Equal(t, "4.8.2", nodeInfo.GetConfigNodeInfo().GetCodeVersion())
	connector.AssertExpectations(t)
}

func TestParseTronGrpcNodeInfoRejectsGarbage(t *testing.T) {
	_, err := specific_helpers.ParseTronGrpcNodeInfo([]byte{0xff, 0xff, 0xff})
	assert.ErrorContains(t, err, "tron grpc node info payload unparseable")
}
