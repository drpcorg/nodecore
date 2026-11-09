package tron_validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/tron_validations"
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

func newTronGrpcConnectorMock(t *testing.T, method string, response protocol.ResponseHolder) *mocks.ConnectorMock {
	t.Helper()
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
			return req.Method() == method && req.RequestType() == protocol.Grpc
		})).
		Return(response).
		Once()
	return connector
}

func tronGrpcError(method string, code codes.Code) protocol.ResponseHolder {
	return protocol.NewGrpcUpstreamErrorResponse(
		protocol.NewInternalUpstreamGrpcRequest(method, nil, chains.TRON),
		&protocol.GrpcStatus{Code: code, Message: "boom"},
	)
}

func tronGrpcNodeInfoBytes(t *testing.T, peers int) []byte {
	t.Helper()
	nodeInfo := &troncore.NodeInfo{}
	for range peers {
		nodeInfo.PeerInfoList = append(nodeInfo.PeerInfoList, &troncore.NodeInfo_PeerInfo{})
	}
	raw, err := proto.Marshal(nodeInfo)
	require.NoError(t, err)
	return raw
}

func tronGrpcHeadBytes(t *testing.T, number, timestamp int64) []byte {
	t.Helper()
	raw, err := proto.Marshal(&tronapi.BlockExtention{
		Blockid:     []byte{1},
		BlockHeader: &troncore.BlockHeader{RawData: &troncore.BlockHeaderRaw{Number: number, Timestamp: timestamp}},
	})
	require.NoError(t, err)
	return raw
}

func grpcPeersOptions() *chains.Options {
	return &chains.Options{InternalTimeout: time.Second, MinPeers: 2}
}

// ---------- TronGrpcPeersValidator ----------

func TestTronGrpcPeersValidatorUnavailableOnError(t *testing.T) {
	connector := newTronGrpcConnectorMock(t, "/protocol.Wallet/GetNodeInfo",
		tronGrpcError("/protocol.Wallet/GetNodeInfo", codes.Unavailable))
	validator := tron_validations.NewTronGrpcPeersValidator("up", chains.TRON, connector, grpcPeersOptions())

	assert.Equal(t, protocol.Unavailable, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTronGrpcPeersValidatorImmatureBelowMinPeers(t *testing.T) {
	connector := newTronGrpcConnectorMock(t, "/protocol.Wallet/GetNodeInfo",
		protocol.NewGrpcUpstreamResponse("1", tronGrpcNodeInfoBytes(t, 1)))
	validator := tron_validations.NewTronGrpcPeersValidator("up", chains.TRON, connector, grpcPeersOptions())

	assert.Equal(t, protocol.Immature, validator.Validate())
}

func TestTronGrpcPeersValidatorImmatureOnEmptyReply(t *testing.T) {
	connector := newTronGrpcConnectorMock(t, "/protocol.Wallet/GetNodeInfo",
		protocol.NewGrpcUpstreamResponse("1", nil))
	validator := tron_validations.NewTronGrpcPeersValidator("up", chains.TRON, connector, grpcPeersOptions())

	assert.Equal(t, protocol.Immature, validator.Validate())
}

func TestTronGrpcPeersValidatorAvailableAtMinPeers(t *testing.T) {
	connector := newTronGrpcConnectorMock(t, "/protocol.Wallet/GetNodeInfo",
		protocol.NewGrpcUpstreamResponse("1", tronGrpcNodeInfoBytes(t, 2)))
	validator := tron_validations.NewTronGrpcPeersValidator("up", chains.TRON, connector, grpcPeersOptions())

	assert.Equal(t, protocol.Available, validator.Validate())
}

// ---------- TronGrpcSyncingValidator ----------

func TestTronGrpcSyncingValidatorUnavailableOnError(t *testing.T) {
	connector := newTronGrpcConnectorMock(t, "/protocol.Wallet/GetBlock",
		tronGrpcError("/protocol.Wallet/GetBlock", codes.Unavailable))
	validator := tron_validations.NewTronGrpcSyncingValidator("up", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Unavailable, validator.Validate())
}

func TestTronGrpcSyncingValidatorUnavailableOnHeaderlessBlock(t *testing.T) {
	connector := newTronGrpcConnectorMock(t, "/protocol.Wallet/GetBlock",
		protocol.NewGrpcUpstreamResponse("1", nil))
	validator := tron_validations.NewTronGrpcSyncingValidator("up", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Unavailable, validator.Validate())
}

func TestTronGrpcSyncingValidatorAvailableForFreshBlock(t *testing.T) {
	connector := newTronGrpcConnectorMock(t, "/protocol.Wallet/GetBlock",
		protocol.NewGrpcUpstreamResponse("1", tronGrpcHeadBytes(t, 100, time.Now().UnixMilli())))
	validator := tron_validations.NewTronGrpcSyncingValidator("up", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Available, validator.Validate())
}

func TestTronGrpcSyncingValidatorSyncingWhenProjectedLagExceedsThreshold(t *testing.T) {
	// 5 blocks tolerance, drift back 6 blocks worth of time (6 * 3000ms).
	connector := newTronGrpcConnectorMock(t, "/protocol.Wallet/GetBlock",
		protocol.NewGrpcUpstreamResponse("1", tronGrpcHeadBytes(t, 100, time.Now().UnixMilli()-18_000)))
	validator := tron_validations.NewTronGrpcSyncingValidator("up", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Syncing, validator.Validate())
}

func TestTronGrpcSyncingValidatorAvailableForFutureTimestamp(t *testing.T) {
	connector := newTronGrpcConnectorMock(t, "/protocol.Wallet/GetBlock",
		protocol.NewGrpcUpstreamResponse("1", tronGrpcHeadBytes(t, 100, time.Now().UnixMilli()+60_000)))
	validator := tron_validations.NewTronGrpcSyncingValidator("up", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Available, validator.Validate())
}
