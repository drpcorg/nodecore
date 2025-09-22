package rating

import (
	"fmt"
	"reflect"
	"time"

	"github.com/dop251/goja"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo/mutable"
	"github.com/spf13/cast"
)

var rating = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "rating",
		Help:      "The current rating score of an upstream for a specific chain and method",
	},
	[]string{"chain", "method", "upstream"},
)

func init() {
	prometheus.MustRegister(rating)
}

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
				resultValue, err := r.scoreFunc(nil, r.runtime.ToValue(upDataArr)) // can't be executed in parallel due to a limitation of the goja lib
				if err != nil {
					log.Error().Err(err).Msg("couldn't execute the score function")
					return
				}
				result := resultValue.Export()
				sortResponse, ok := result.(map[string]interface{})
				if !ok {
					log.Error().Msgf("unexpected return value %s from the score function, must be an object", reflect.TypeOf(result))
					return
				}
				sortedUpstreamsAsObjects, ok := sortResponse["sortedUpstreams"]
				if !ok {
					log.Error().Msg("there must be 'sortedUpstreams' field in the return value from the score function")
					return
				}
				sortedUpstreamsArray, ok := sortedUpstreamsAsObjects.([]interface{})
				if !ok {
					log.Error().Msgf("unexpected return value %s from the score function, 'sortedUpstreams' must be an array", reflect.TypeOf(sortedUpstreamsAsObjects))
					return
				}
				scoresAsObjects, ok := sortResponse["scores"]
				if !ok {
					log.Error().Msg("there must be 'scores' field in the return value from the score function")
					return
				}
				err = r.processScores(scoresAsObjects, chSupervisor.Chain, method)
				if err != nil {
					log.Error().Msg(err.Error())
					return
				}

				sortedUpstreams := make([]string, 0)
				for _, upstreamAsObject := range sortedUpstreamsArray {
					upstream, ok := upstreamAsObject.(string)
					if !ok {
						log.Error().Msgf("unexpected value %s in the array 'sortedUpstreams' from the score function, must be string", reflect.TypeOf(upstreamAsObject))
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

func (r *RatingRegistry) processScores(scoresAsObjects interface{}, chain chains.Chain, method string) error {
	scoresAsArray, ok := scoresAsObjects.([]interface{})
	if !ok {
		return fmt.Errorf("unexpected return value %s from the score function, 'scores' must be an array", reflect.TypeOf(scoresAsObjects))
	}
	for _, scoreObject := range scoresAsArray {
		score, ok := scoreObject.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unexpected return value %s from the score function, score element must be an object", reflect.TypeOf(scoreObject))
		}
		idValue, ok := score["id"]
		if !ok {
			log.Error().Msg("there must be 'id' field in a score element")
		}
		idString, ok := idValue.(string)
		if !ok {
			return fmt.Errorf("unexpected return value %s from the score function, 'id' must be string", reflect.TypeOf(idValue))
		}
		scoreValue, ok := score["score"]
		if !ok {
			log.Error().Msg("there must be 'score' field in a score element")
		}
		scoreNum, err := cast.ToFloat64E(scoreValue)
		if err != nil {
			return fmt.Errorf("unexpected return value %s from the score function, 'score' must be number", reflect.TypeOf(scoreValue))
		}
		rating.WithLabelValues(chain.String(), method, idString).Set(scoreNum)
	}
	return nil
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
