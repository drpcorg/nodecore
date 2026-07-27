package rating

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

// TestCalculateRatingSortedUpstreamsSize checks that calculateRating stores a
// sorted-upstreams entry only for chains with more than one upstream, and that
// each stored method holds every upstream of that chain.
func TestCalculateRatingSortedUpstreamsSize(t *testing.T) {
	// A chain with two upstreams supporting two methods -> must be calculated.
	multiMethods := mocks.NewMethodsMock()
	multiMethods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_test1", "eth_test2"))
	multiChain := newChainSupervisorWithUpstreams(t, chains.ARBITRUM, multiMethods, "id1", "id2")

	// A chain with a single upstream -> must be skipped (trivial order).
	singleMethods := mocks.NewMethodsMock()
	singleMethods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_test1"))
	singleChain := newChainSupervisorWithUpstreams(t, chains.POLYGON, singleMethods, "id1")

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{multiChain, singleChain})

	registry := NewRatingRegistry(upSupervisor, dimensions.NewBaseDimensionTracker(), &config.ScorePolicyConfig{
		CalculationFunctionName: config.DefaultLatencyPolicyFuncName,
		CalculationInterval:     1 * time.Minute,
	})

	registry.calculateRating()

	sorted := registry.sortedUpstreams.Load()

	// Only the multi-upstream chain is stored; the single-upstream chain is skipped.
	assert.Equal(t, 1, cMapSize(sorted))

	methodUpstreams, ok := sorted.Load(chains.ARBITRUM)
	assert.True(t, ok)

	_, ok = sorted.Load(chains.POLYGON)
	assert.False(t, ok)

	// Both supported methods are scored, each ranking all upstreams of the chain.
	assert.Equal(t, 2, cMapSize(methodUpstreams))
	for _, method := range []string{"eth_test1", "eth_test2"} {
		ups, methodOk := methodUpstreams.Load(method)
		assert.True(t, methodOk)
		assert.ElementsMatch(t, []string{"id1", "id2"}, ups)
	}

	// The single-upstream chain is skipped for sorting but still publishes a fixed rating gauge.
	assert.Equal(t, float64(singleUpstreamRating), gaugeValue(t, chains.POLYGON, "eth_test1", "id1"))
}

func gaugeValue(t *testing.T, chain chains.Chain, method, upstreamId string) float64 {
	t.Helper()
	var m dto.Metric
	assert.NoError(t, rating.WithLabelValues(chain.String(), method, upstreamId).Write(&m))
	return m.GetGauge().GetValue()
}

func newChainSupervisorWithUpstreams(t *testing.T, chain chains.Chain, methods *mocks.MethodsMock, ids ...string) upstreams.ChainSupervisor {
	t.Helper()
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chain, fork_choice.NewHeightForkChoice(), nil, false, nil)
	go chainSupervisor.Start()

	for _, id := range ids {
		chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent(id, protocol.Available, protocol.NewBlockWithHeight(100), methods))
	}

	// Ingestion is async - poll until all upstreams are registered instead of sleeping.
	assert.Eventually(t, func() bool {
		return len(chainSupervisor.GetUpstreamIds()) == len(ids)
	}, time.Second, 5*time.Millisecond)
	assert.ElementsMatch(t, ids, chainSupervisor.GetUpstreamIds())
	return chainSupervisor
}

func cMapSize[K comparable, V any](m *utils.CMap[K, V]) int {
	size := 0
	m.Range(func(K, V) bool {
		size++
		return true
	})
	return size
}
