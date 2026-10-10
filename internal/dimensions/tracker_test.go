package dimensions_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestTrackerAllDimensions(t *testing.T) {
	tracker := dimensions.NewGenericDimensionTracker()
	chain := chains.POLYGON
	upId := "id1"
	method := "method"
	upDims := tracker.GetUpstreamDimensions(chain, upId, method)

	tracker.GetChainDimensions(chain, upId).TrackLags(uint64(5), uint64(10))
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

	tracker.GetChainDimensions(chain, upId).TrackHeadLag(15)
	tracker.GetChainDimensions(chain, upId).TrackFinalizationLag(53)

	assert.Equal(t, uint64(15), fullDims.ChainDimensions.GetHeadLag())
	assert.Equal(t, uint64(53), fullDims.ChainDimensions.GetFinalizationLag())
}

func TestTrackerRemoveUpstream(t *testing.T) {
	tracker := dimensions.NewGenericDimensionTracker()
	chain := chains.POLYGON

	for _, upId := range []string{"removed", "kept"} {
		tracker.GetChainDimensions(chain, upId).TrackLags(uint64(5), uint64(10))
		for _, method := range []string{"method1", "method2"} {
			tracker.GetUpstreamDimensions(chain, upId, method).TrackTotalRequests()
		}
	}
	// the same id on another chain is another upstream
	tracker.GetUpstreamDimensions(chains.ETHEREUM, "removed", "method1").TrackTotalRequests()

	tracker.RemoveUpstream(chain, "removed")

	for _, method := range []string{"method1", "method2"} {
		removed := tracker.GetAllDimensions(chain, "removed", method)
		assert.Zero(t, removed.UpstreamDimensions.GetTotalRequests())
		assert.Zero(t, removed.ChainDimensions.GetHeadLag())
		assert.Zero(t, removed.ChainDimensions.GetFinalizationLag())

		kept := tracker.GetAllDimensions(chain, "kept", method)
		assert.Equal(t, uint64(1), kept.UpstreamDimensions.GetTotalRequests())
		assert.Equal(t, uint64(5), kept.ChainDimensions.GetHeadLag())
	}
	assert.Equal(t, uint64(1), tracker.GetUpstreamDimensions(chains.ETHEREUM, "removed", "method1").GetTotalRequests())
}
