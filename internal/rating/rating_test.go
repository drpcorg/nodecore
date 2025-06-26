package rating_test

import (
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/internal/rating"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/test_utils"
	"github.com/drpcorg/dsheltie/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestRatingRegistryNoUpstreams(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	tracker := dimensions.NewDimensionTracker()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	ratingRegistry := rating.NewRatingRegistry(upSupervisor, tracker, &config.ScorePolicyConfig{CalculationFunction: config.DefaultLatencyPolicyFunc, CalculationInterval: 1 * time.Minute})

	upSupervisor.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)

	sortedUps := ratingRegistry.GetSortedUpstreams(chains.ARBITRUM, "method")

	upSupervisor.AssertExpectations(t)

	assert.Empty(t, sortedUps)
}
