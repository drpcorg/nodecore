package aptos_bounds_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/aptos_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestAptosLowerBoundEmitsOldestStateAndBlock(t *testing.T) {
	conn := mocks.NewConnectorMock()
	body := `{"chain_id":1,"block_height":"860298804","oldest_block_height":"700",` +
		`"ledger_version":"5965411071","oldest_ledger_version":"4242"}`
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v1"
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest))

	d := aptos_bounds.NewAptosLowerBoundDetector("id", chains.GetChain("aptos-mainnet").Chain, time.Second, conn)
	bounds, err := d.DetectLowerBound()
	assert.NoError(t, err)

	got := map[protocol.LowerBoundType]int64{}
	for _, b := range bounds {
		got[b.Type] = b.Bound
	}
	assert.Equal(t, int64(4242), got[protocol.StateBound])
	assert.Equal(t, int64(700), got[protocol.BlockBound])
}

func TestAptosLowerBoundFallsBackToUnknownOnError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	d := aptos_bounds.NewAptosLowerBoundDetector("id", chains.GetChain("aptos-mainnet").Chain, time.Second, conn)
	bounds, err := d.DetectLowerBound()
	assert.NoError(t, err)
	assert.Len(t, bounds, 1)
	assert.Equal(t, protocol.UnknownBound, bounds[0].Type)
	assert.Equal(t, int64(0), bounds[0].Bound)
}
