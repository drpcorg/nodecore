package stellar_bounds_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/stellar_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func isGetHealth(r protocol.RequestHolder) bool   { return r.Method() == "getHealth" }
func isHorizonRoot(r protocol.RequestHolder) bool { return r.Method() == "GET#/" }

func TestStellarLowerBoundPublishesOldestLedgerAsStateBound(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1",
			[]byte(`{"status":"healthy","latestLedger":63525714,"oldestLedger":63404755,`+
				`"ledgerRetentionWindow":120960}`), protocol.JsonRpc))

	d := stellar_bounds.NewStellarLowerBoundDetector("id", chains.GetChain("stellar").Chain, time.Second, conn)
	bounds, err := d.DetectLowerBound(context.Background())
	require.NoError(t, err)

	require.Len(t, bounds, 1)
	assert.Equal(t, protocol.StateBound, bounds[0].Type)
	assert.Equal(t, int64(63404755), bounds[0].Bound)
	assert.Equal(t, []protocol.LowerBoundType{protocol.StateBound}, d.SupportedTypes())
	assert.Equal(t, 2*time.Minute, d.Period())
}

// Zero or absent means the node did not report its boundary, not that the full
// history is available - fail the tick instead of publishing an archive claim.
func TestStellarLowerBoundErrorsOnMissingOldestLedger(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"status":"healthy"}`), protocol.JsonRpc))

	d := stellar_bounds.NewStellarLowerBoundDetector("id", chains.GetChain("stellar").Chain, time.Second, conn)
	_, err := d.DetectLowerBound(context.Background())
	assert.Error(t, err)
}

func TestStellarLowerBoundErrorsOnFetchFailure(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(-32603, "boom", nil)))

	d := stellar_bounds.NewStellarLowerBoundDetector("id", chains.GetChain("stellar").Chain, time.Second, conn)
	_, err := d.DetectLowerBound(context.Background())
	assert.Error(t, err)
}

func TestStellarHorizonLowerBoundPublishesElderLedgerAsStateBound(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponse("1",
			[]byte(`{"history_latest_ledger":63563999,"history_elder_ledger":63563520}`), 200, protocol.Rest))

	d := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.GetChain("stellar").Chain, time.Second, conn)
	bounds, err := d.DetectLowerBound(context.Background())
	require.NoError(t, err)

	require.Len(t, bounds, 1)
	assert.Equal(t, protocol.StateBound, bounds[0].Type)
	assert.Equal(t, int64(63563520), bounds[0].Bound)
	assert.Equal(t, []protocol.LowerBoundType{protocol.StateBound}, d.SupportedTypes())
	assert.Equal(t, 2*time.Minute, d.Period())
}

func TestStellarHorizonLowerBoundErrorsOnMissingElderLedger(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"history_latest_ledger":63563999}`), 200, protocol.Rest))

	d := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.GetChain("stellar").Chain, time.Second, conn)
	_, err := d.DetectLowerBound(context.Background())
	assert.Error(t, err)
}

func TestStellarHorizonLowerBoundErrorsOnFetchFailure(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(503, "boom", nil)))

	d := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.GetChain("stellar").Chain, time.Second, conn)
	_, err := d.DetectLowerBound(context.Background())
	assert.Error(t, err)
}
