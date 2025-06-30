package rating

import (
	"github.com/dop251/goja"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo/mutable"
	"reflect"
	"time"
)

type RatingRegistry struct {
	upstreamSupervisor  upstreams.UpstreamSupervisor
	tracker             *dimensions.DimensionTracker
	calculationInterval time.Duration
	scoreFunc           goja.Callable
	runtime             *goja.Runtime
	sortedUpstreams     *utils.Atomic[*utils.CMap[chains.Chain, utils.CMap[string, []string]]]
}

func NewRatingRegistry(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	tracker *dimensions.DimensionTracker,
	scorePolicyConfig *config.ScorePolicyConfig,
) *RatingRegistry {
	scoreFunc, _ := scorePolicyConfig.GetScoreFunc()
	sortedUpstreams := utils.NewAtomic[*utils.CMap[chains.Chain, utils.CMap[string, []string]]]()
	sortedUpstreams.Store(utils.NewCMap[chains.Chain, utils.CMap[string, []string]]())

	return &RatingRegistry{
		scoreFunc:           scoreFunc,
		upstreamSupervisor:  upstreamSupervisor,
		tracker:             tracker,
		runtime:             goja.New(),
		calculationInterval: scorePolicyConfig.CalculationInterval,
		sortedUpstreams:     sortedUpstreams,
	}
}

func (r *RatingRegistry) GetSortedUpstreams(chain chains.Chain, method string) []string {
	methods, ok := r.sortedUpstreams.Load().Load(chain)
	if !ok {
		return r.getShuffledUpstreamIds(chain)
	}
	ups, ok := methods.Load(method)
	if !ok {
		return r.getShuffledUpstreamIds(chain)
	}

	return *ups
}

func (r *RatingRegistry) Start() {
	log.Info().Msgf("rating will be calculated every %s", r.calculationInterval)
	for {
		r.calculateRating()
		time.Sleep(r.calculationInterval)
	}
}

func (r *RatingRegistry) getShuffledUpstreamIds(chain chains.Chain) []string {
	upstreamIds := r.upstreamSupervisor.GetChainSupervisor(chain).GetUpstreamIds()
	mutable.Shuffle(upstreamIds)
	return upstreamIds
}

func (r *RatingRegistry) calculateRating() {
	newSortedUpstreams := utils.NewCMap[chains.Chain, utils.CMap[string, []string]]()

	for _, chSupervisor := range r.upstreamSupervisor.GetChainSupervisors() {
		methodUpstreams := utils.NewCMap[string, []string]()
		methods := chSupervisor.GetMethods()
		for _, method := range methods {
			upDataArr := make([]map[string]interface{}, 0)

			for _, upstreamId := range chSupervisor.GetUpstreamIds() {
				dims := r.tracker.GetAllDimensions(chSupervisor.Chain, upstreamId, method)
				upDataArr = append(upDataArr, getUpstreamData(upstreamId, method, dims))
			}

			if len(upDataArr) > 0 {
				sortedUpstreams := make([]string, 0)
				resultValue, err := r.scoreFunc(nil, r.runtime.ToValue(upDataArr)) // can't be executed in parallel due to a limitation of the goja lib
				if err != nil {
					log.Error().Err(err).Msg("couldn't execute the score function")
					return
				}
				result := resultValue.Export()
				sortedUpstreamAsObjects, ok := result.([]interface{})
				if !ok {
					log.Error().Msgf("unexpected return value %s from the score function, must be an array of objects", reflect.TypeOf(result))
					return
				}
				for _, upstreamAsObject := range sortedUpstreamAsObjects {
					upstream, ok := upstreamAsObject.(string)
					if !ok {
						log.Error().Msgf("unexpected value %s in the array from the score function, must be string", reflect.TypeOf(upstreamAsObject))
						return
					}
					sortedUpstreams = append(sortedUpstreams, upstream)
				}

				methodUpstreams.Store(method, &sortedUpstreams)
			} else {
				log.Debug().Msgf("no upstreams to calculate their rating, chain %s", chSupervisor.Chain)
			}
		}

		newSortedUpstreams.Store(chSupervisor.Chain, methodUpstreams)
	}

	r.sortedUpstreams.Store(newSortedUpstreams)
}

func getUpstreamData(upstreamId, method string, fullDims *dimensions.FullDimensions) map[string]interface{} {
	upData := map[string]interface{}{
		"id":     upstreamId,
		"method": method,
		"metrics": map[string]interface{}{
			"latencyP90":        fullDims.UpstreamDimensions.GetValueAtQuantile(0.9),
			"latencyP95":        fullDims.UpstreamDimensions.GetValueAtQuantile(0.95),
			"latencyP99":        fullDims.UpstreamDimensions.GetValueAtQuantile(0.99),
			"totalRequests":     fullDims.UpstreamDimensions.GetTotalRequests(),
			"totalErrors":       fullDims.UpstreamDimensions.GetTotalErrors(),
			"errorRate":         fullDims.UpstreamDimensions.GetErrorRate(),
			"headLag":           fullDims.ChainDimensions.GetHeadLag(),
			"finalizationLag":   fullDims.ChainDimensions.GetFinalizationLag(),
			"successfulRetries": fullDims.UpstreamDimensions.GetSuccessfulRetries(),
		},
	}

	return upData
}
