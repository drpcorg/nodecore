package celestia_bounds_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/celestia_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const tailHeader = `{"jsonrpc":"2.0","result":{"header":{"chain_id":"mocha-4","height":"13838455"},"commit":{"block_id":{"hash":"AA"}}}}`

func TestCelestiaLowerBoundDetector(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(tailHeader), 200, protocol.JsonRpc)).Once()

	detector := celestia_bounds.NewCelestiaLowerBoundDetector("id", chains.GetChain("celestia").Chain, time.Second, conn)
	bounds, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	conn.AssertExpectations(t)

	require.Len(t, bounds, 1)
	assert.Equal(t, int64(13838455), bounds[0].Bound)
	assert.Equal(t, protocol.BlockBound, bounds[0].Type)
	assert.Equal(t, []protocol.LowerBoundType{protocol.BlockBound}, detector.SupportedTypes())
}

func TestCelestiaLowerBoundDetectorRetriesThenFails(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// pre-v0.28 node: header.Tail is unknown, every attempt fails
	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(-32601, "method 'header.Tail' not found", nil))).Times(3)

	detector := celestia_bounds.NewCelestiaLowerBoundDetector("id", chains.GetChain("celestia").Chain, time.Second, conn)
	bounds, err := detector.DetectLowerBound(context.Background())
	require.Error(t, err)
	assert.Nil(t, bounds)
	conn.AssertExpectations(t)
}
