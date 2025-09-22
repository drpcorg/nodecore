package rating_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestRatingRegistryNoUpstreams(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	tracker := dimensions.NewDimensionTracker()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	ratingRegistry := rating.NewRatingRegistry(upSupervisor, tracker, &config.ScorePolicyConfig{CalculationFunctionName: config.DefaultLatencyPolicyFuncName, CalculationInterval: 1 * time.Minute})

	upSupervisor.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)

	sortedUps := ratingRegistry.GetSortedUpstreams(chains.ARBITRUM, "method")

	upSupervisor.AssertExpectations(t)

	assert.Empty(t, sortedUps)
}
