package dimensions_test

import (
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestTrackerAllDimensions(t *testing.T) {
	tracker := dimensions.NewDimensionTracker()
	chain := chains.POLYGON
	upId := "id1"
	method := "method"
	upDims := tracker.GetUpstreamDimensions(chain, upId, method)

	tracker.TrackLags(chain, upId, uint64(5), uint64(10))
	upDims.TrackTotalRequests()
	upDims.TrackTotalRequests()
	upDims.TrackRequestDuration(100000)
	upDims.TrackSuccessfulRetries()
	upDims.TrackTotalErrors()

	fullDims := tracker.GetAllDimensions(chain, upId, method)

	assert.Equal(t, uint64(5), fullDims.ChainDimensions.GetHeadLag())
	assert.Equal(t, uint64(10), fullDims.ChainDimensions.GetFinalizationLag())
	assert.Equal(t, uint64(1), fullDims.UpstreamDimensions.GetSuccessfulRetries())
	assert.Equal(t, uint64(2), fullDims.UpstreamDimensions.GetTotalRequests())
	assert.Equal(t, uint64(1), fullDims.UpstreamDimensions.GetTotalErrors())
	assert.Equal(t, 0.5, fullDims.UpstreamDimensions.GetErrorRate())
	assert.True(t, fullDims.UpstreamDimensions.GetValueAtQuantile(0.9) > 95000)
}
