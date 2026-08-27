package sui_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/labels/sui_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/sui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func serviceInfoWithServer(t *testing.T, server *string) []byte {
	t.Helper()
	data, err := proto.Marshal(&sui.GetServiceInfoResponse{Server: server})
	require.NoError(t, err)
	return data
}

func TestSuiClientLabelsDetectorRequestsGetServiceInfo(t *testing.T) {
	d := sui_labels.NewSuiClientLabelsDetector(chains.GetChain("sui").Chain)

	req, err := d.NodeTypeRequest()
	require.NoError(t, err)
	assert.Equal(t, "/sui.rpc.v2.LedgerService/GetServiceInfo", req.Method())
}

func TestSuiClientLabelsDetectorSplitsServerAndTrimsTheCommitSuffix(t *testing.T) {
	d := sui_labels.NewSuiClientLabelsDetector(chains.GetChain("sui").Chain)

	version, clientType, err := d.ClientVersionAndType(
		serviceInfoWithServer(t, new("sui-node/1.78.0-03113679fb97")))
	require.NoError(t, err)
	assert.Equal(t, "sui-node", clientType)
	assert.Equal(t, "1.78.0", version)
}

func TestSuiClientLabelsDetectorKeepsABareSemver(t *testing.T) {
	d := sui_labels.NewSuiClientLabelsDetector(chains.GetChain("sui").Chain)

	version, clientType, err := d.ClientVersionAndType(serviceInfoWithServer(t, new("sui-node/1.78.0")))
	require.NoError(t, err)
	assert.Equal(t, "sui-node", clientType)
	assert.Equal(t, "1.78.0", version)
}

func TestSuiClientLabelsDetectorHandlesAServerWithoutAVersion(t *testing.T) {
	d := sui_labels.NewSuiClientLabelsDetector(chains.GetChain("sui").Chain)

	version, clientType, err := d.ClientVersionAndType(serviceInfoWithServer(t, new("sui-node")))
	require.NoError(t, err)
	assert.Equal(t, "sui-node", clientType)
	assert.Empty(t, version)
}

func TestSuiClientLabelsDetectorHandlesAnAbsentServerField(t *testing.T) {
	d := sui_labels.NewSuiClientLabelsDetector(chains.GetChain("sui").Chain)

	version, clientType, err := d.ClientVersionAndType(serviceInfoWithServer(t, nil))
	require.NoError(t, err)
	assert.Empty(t, clientType)
	assert.Empty(t, version)
}

func TestSuiClientLabelsDetectorErrorsOnUnparseablePayload(t *testing.T) {
	d := sui_labels.NewSuiClientLabelsDetector(chains.GetChain("sui").Chain)

	_, _, err := d.ClientVersionAndType([]byte("not a protobuf"))
	assert.Error(t, err)
}
