package cosmos_labels_test

import (
	"testing"

	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
	"cosmossdk.io/api/tendermint/p2p"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/cosmos_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/tendermint_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func cosmosGrpcNodeInfoBytes(t *testing.T, network, cometVersion, appVersion string) []byte {
	t.Helper()
	data, err := proto.Marshal(&tendermintv1beta1.GetNodeInfoResponse{
		DefaultNodeInfo:    &p2p.DefaultNodeInfo{Network: network, Version: cometVersion},
		ApplicationVersion: &tendermintv1beta1.VersionInfo{Name: "gaia", Version: appVersion},
	})
	require.NoError(t, err)
	return data
}

func TestCosmosGrpcClientLabelsDetector(t *testing.T) {
	detector := cosmos_labels.NewCosmosGrpcClientLabelsDetector(chains.GetChain("cosmos-hub").Chain)

	req, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	assert.Equal(t, "/cosmos.base.tendermint.v1beta1.Service/GetNodeInfo", req.Method())
	assert.Equal(t, protocol.Grpc, req.RequestType())

	version, clientType, err := detector.ClientVersionAndType(cosmosGrpcNodeInfoBytes(t, "cosmoshub-4", "0.38.17", "v21.0.0"))
	require.NoError(t, err)
	assert.Equal(t, "v21.0.0", version)
	assert.Equal(t, tendermint_labels.CosmosClient, clientType)
}

func TestCosmosGrpcClientLabelsDetectorFallsBackToNodeVersion(t *testing.T) {
	detector := cosmos_labels.NewCosmosGrpcClientLabelsDetector(chains.GetChain("cosmos-hub").Chain)

	version, clientType, err := detector.ClientVersionAndType(cosmosGrpcNodeInfoBytes(t, "cosmoshub-4", "0.38.17", ""))
	require.NoError(t, err)
	assert.Equal(t, "0.38.17", version)
	assert.Equal(t, tendermint_labels.CosmosClient, clientType)
}
