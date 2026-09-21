package cosmos_validations_test

import (
	"testing"
	"time"

	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/cosmos_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func TestCosmosGrpcSyncingValidator(t *testing.T) {
	cases := []struct {
		syncing bool
		want    protocol.AvailabilityStatus
	}{
		{syncing: false, want: protocol.Available},
		{syncing: true, want: protocol.Syncing},
	}
	for _, c := range cases {
		body, err := proto.Marshal(&tendermintv1beta1.GetSyncingResponse{Syncing: c.syncing})
		require.NoError(t, err)
		connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
		connector.
			On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosGrpc("/cosmos.base.tendermint.v1beta1.Service/GetSyncing"))).
			Return(protocol.NewGrpcUpstreamResponse("1", body)).
			Once()

		validator := cosmos_validations.NewCosmosGrpcSyncingValidator(
			"id", chains.GetChain("cosmos-hub").Chain, connector, time.Second,
		)
		assert.Equal(t, c.want, validator.Validate())
		connector.AssertExpectations(t)
	}
}

func TestCosmosGrpcSyncingValidatorUnavailableOnError(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosGrpc("/cosmos.base.tendermint.v1beta1.Service/GetSyncing"))).
		Return(protocol.NewGrpcUpstreamErrorResponse(
			protocol.NewInternalUpstreamGrpcRequest("/cosmos.base.tendermint.v1beta1.Service/GetSyncing", nil, chains.GetChain("cosmos-hub").Chain),
			&protocol.GrpcStatus{Code: codes.Unavailable, Message: "node down"},
		)).
		Once()

	validator := cosmos_validations.NewCosmosGrpcSyncingValidator(
		"id", chains.GetChain("cosmos-hub").Chain, connector, time.Second,
	)
	assert.Equal(t, protocol.Unavailable, validator.Validate())
	connector.AssertExpectations(t)
}
