package cosmos_validations_test

import (
	"testing"
	"time"

	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
	"cosmossdk.io/api/tendermint/p2p"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/cosmos_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func matchCosmosGrpc(method string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == method && req.RequestType() == protocol.Grpc
	}
}

func cosmosGrpcNodeInfoBytes(t *testing.T, network, cometVersion, appVersion string) []byte {
	t.Helper()
	data, err := proto.Marshal(&tendermintv1beta1.GetNodeInfoResponse{
		DefaultNodeInfo:    &p2p.DefaultNodeInfo{Network: network, Version: cometVersion},
		ApplicationVersion: &tendermintv1beta1.VersionInfo{Name: "gaia", Version: appVersion},
	})
	require.NoError(t, err)
	return data
}

func TestCosmosGrpcChainValidator(t *testing.T) {
	chain := chains.GetChain("cosmos-hub")

	cases := []struct {
		name    string
		network string
		want    validations.ValidationSettingResult
	}{
		{name: "match", network: "cosmoshub-4", want: validations.Valid},
		{name: "case insensitive", network: "COSMOSHUB-4", want: validations.Valid},
		{name: "wrong network", network: "osmosis-1", want: validations.FatalSettingError},
		// An upstream that answers GetNodeInfo but reports no network cannot
		// prove which chain it serves, so it is refused outright rather than
		// retried.
		{name: "empty network", network: "", want: validations.FatalSettingError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosGrpc("/cosmos.base.tendermint.v1beta1.Service/GetNodeInfo"))).
				Return(protocol.NewGrpcUpstreamResponse("1", cosmosGrpcNodeInfoBytes(t, c.network, "0.38.17", "v21.0.0"))).
				Once()

			validator := cosmos_validations.NewCosmosGrpcChainValidator("id", connector, chain, time.Second)
			assert.Equal(t, c.want, validator.Validate())
			connector.AssertExpectations(t)
		})
	}
}
