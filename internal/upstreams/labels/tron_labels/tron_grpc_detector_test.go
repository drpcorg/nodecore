package tron_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/tron_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	troncore "github.com/drpcorg/public/pkg/tron/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestTronGrpcClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := tron_labels.NewTronGrpcClientLabelsDetector(chains.TRON)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)

	assert.Equal(t, "/protocol.Wallet/GetNodeInfo", request.Method())
	assert.Equal(t, protocol.Grpc, request.RequestType())
	assert.Equal(t, protocol.InternalUnary, request.RequestObserver().GetRequestKind())
	body, err := request.Body()
	require.NoError(t, err)
	assert.Empty(t, body)
}

func TestTronGrpcClientLabelsDetectorClientVersionAndType(t *testing.T) {
	detector := tron_labels.NewTronGrpcClientLabelsDetector(chains.TRON)

	raw, err := proto.Marshal(&troncore.NodeInfo{
		ConfigNodeInfo: &troncore.NodeInfo_ConfigNodeInfo{CodeVersion: "4.8.2.3"},
	})
	require.NoError(t, err)

	version, clientType, err := detector.ClientVersionAndType(raw)
	require.NoError(t, err)
	assert.Equal(t, "4.8.2.3", version)
	assert.Equal(t, tron_labels.TronClient, clientType)
}

func TestTronGrpcClientLabelsDetectorEmptyNodeInfo(t *testing.T) {
	detector := tron_labels.NewTronGrpcClientLabelsDetector(chains.TRON)

	version, clientType, err := detector.ClientVersionAndType(nil)
	require.NoError(t, err)
	assert.Equal(t, "", version)
	assert.Equal(t, tron_labels.TronClient, clientType)
}

func TestTronGrpcClientLabelsDetectorGarbage(t *testing.T) {
	detector := tron_labels.NewTronGrpcClientLabelsDetector(chains.TRON)

	_, _, err := detector.ClientVersionAndType([]byte{0xff, 0xff, 0xff})
	assert.ErrorContains(t, err, "tron grpc node info payload unparseable")
}
