package flow

import (
	"sync"
	"sync/atomic"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/samber/lo"
)

const NoUpstream = "NoUpstream"

type UpstreamStrategy interface {
	SelectUpstream(request protocol.RequestHolder) (string, error)
}

type RatingStrategy struct {
	chainSupervisor    *upstreams.ChainSupervisor
	selectedUpstreams  mapset.Set[string]
	ups                []string
	additionalMatchers []Matcher
	mu                 sync.Mutex
}

func NewRatingStrategy(
	chain chains.Chain,
	method string,
	additionalMatchers []Matcher,
	chainSupervisor *upstreams.ChainSupervisor,
	registry *rating.RatingRegistry,
) *RatingStrategy {
	ups := registry.GetSortedUpstreams(chain, method)
	return &RatingStrategy{
		chainSupervisor:    chainSupervisor,
		ups:                ups,
		additionalMatchers: additionalMatchers,
		selectedUpstreams:  mapset.NewThreadUnsafeSet[string](),
	}
}

func (r *RatingStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	if len(r.ups) == 0 {
		return "", protocol.NoAvailableUpstreamsError()
	}

	selectedUpstream, currentReason := filterUpstreams(&r.mu, request, r.ups, r.chainSupervisor, r.selectedUpstreams, r.additionalMatchers)
	if selectedUpstream != "" {
		return selectedUpstream, nil
	}

	return "", selectionError(currentReason)
}

var _ UpstreamStrategy = (*RatingStrategy)(nil)

var index = atomic.Uint64{}

type BaseStrategy struct {
	selectedUpstreams mapset.Set[string]
	chainSupervisor   *upstreams.ChainSupervisor
	mu                sync.Mutex
}

func NewBaseStrategy(chainSupervisor *upstreams.ChainSupervisor) *BaseStrategy {
	return &BaseStrategy{
		selectedUpstreams: mapset.NewThreadUnsafeSet[string](),
		chainSupervisor:   chainSupervisor,
	}
}

func (b *BaseStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	upstreamIds := b.chainSupervisor.GetUpstreamIds()
	if len(upstreamIds) == 0 {
		return "", protocol.NoAvailableUpstreamsError()
	}

	pos := index.Add(1) % uint64(len(upstreamIds))
	upstreamIds = append(upstreamIds[pos:], upstreamIds[:pos]...)

	selectedUpstream, currentReason := filterUpstreams(&b.mu, request, upstreamIds, b.chainSupervisor, b.selectedUpstreams, nil)
	if selectedUpstream != "" {
		return selectedUpstream, nil
	}

	return "", selectionError(currentReason)
}

func filterUpstreams(
	mu *sync.Mutex,
	request protocol.RequestHolder,
	upstreamIds []string,
	chainSupervisor *upstreams.ChainSupervisor,
	selectedUpstreams mapset.Set[string],
	additionalMatchers []Matcher,
) (string, MatchResponse) {
	var currentReason MatchResponse = AvailabilityResponse{}
	matchers := lo.Ternary(len(additionalMatchers) > 0, additionalMatchers, make([]Matcher, 0))
	matchers = append(matchers, NewStatusMatcher(), NewMethodMatcher(request.Method()))
	if request.IsSubscribe() {
		matchers = append(matchers, NewWsCapMatcher(request.Method()))
	}

	multiMatcher := NewMultiMatcher(matchers...)

	for i := 0; i < len(upstreamIds); i++ {
		upstreamState := chainSupervisor.GetUpstreamState(upstreamIds[i])
		matched := multiMatcher.Match(upstreamIds[i], upstreamState)

		upstreamMatched, newReason := processMatchedResponse(mu, matched, currentReason, selectedUpstreams, upstreamIds[i])
		if upstreamMatched {
			return upstreamIds[i], nil
		} else if newReason != nil {
			currentReason = newReason
		}
	}
	return "", currentReason
}

func processMatchedResponse(
	mu *sync.Mutex,
	matched MatchResponse,
	currentReason MatchResponse,
	selectedUpstreams mapset.Set[string],
	upstreamId string,
) (bool, MatchResponse) {
	mu.Lock()
	defer mu.Unlock()
	if !selectedUpstreams.ContainsOne(upstreamId) {
		if matched.Type() == SuccessType {
			selectedUpstreams.Add(upstreamId)
			return true, nil
		} else {
			if matched.Type() < currentReason.Type() {
				return false, matched
			}
		}
	}
	return false, nil
}

func selectionError(matchResponse MatchResponse) error {
	switch m := matchResponse.(type) {
	case MethodResponse:
		return protocol.NotSupportedMethodError(m.method)
	default:
		return protocol.NoAvailableUpstreamsError()
	}
}

var _ UpstreamStrategy = (*BaseStrategy)(nil)
