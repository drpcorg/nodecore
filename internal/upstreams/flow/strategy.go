package flow

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/rating"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"sync/atomic"
)

const NoUpstream = "NoUpstream"

type UpstreamStrategy interface {
	SelectUpstream(request protocol.RequestHolder) (string, error)
}

type RatingStrategy struct {
	chainSupervisor   *upstreams.ChainSupervisor
	selectedUpstreams mapset.Set[string]
	ups               []string
}

func NewRatingStrategy(chain chains.Chain, method string, chainSupervisor *upstreams.ChainSupervisor, registry *rating.RatingRegistry) *RatingStrategy {
	ups := registry.GetSortedUpstreams(chain, method)
	return &RatingStrategy{
		chainSupervisor:   chainSupervisor,
		ups:               ups,
		selectedUpstreams: mapset.NewSet[string](),
	}
}

func (r *RatingStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	if len(r.ups) == 0 {
		return "", protocol.NoAvailableUpstreamsError()
	}

	selectedUpstream, currentReason := filterUpstreams(request, r.ups, r.chainSupervisor, r.selectedUpstreams)
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
}

func NewBaseStrategy(chainSupervisor *upstreams.ChainSupervisor) *BaseStrategy {
	return &BaseStrategy{
		selectedUpstreams: mapset.NewSet[string](),
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

	selectedUpstream, currentReason := filterUpstreams(request, upstreamIds, b.chainSupervisor, b.selectedUpstreams)
	if selectedUpstream != "" {
		return selectedUpstream, nil
	}

	return "", selectionError(currentReason)
}

func filterUpstreams(
	request protocol.RequestHolder,
	upstreamIds []string,
	chainSupervisor *upstreams.ChainSupervisor,
	selectedUpstreams mapset.Set[string],
) (string, MatchResponse) {
	var currentReason MatchResponse = AvailabilityResponse{}
	matchers := append(make([]Matcher, 0), NewStatusMatcher(), NewMethodMatcher(request.Method()))
	if request.IsSubscribe() {
		matchers = append(matchers, NewWsCapMatcher(request.Method()))
	}

	multiMatcher := NewMultiMatcher(matchers...)

	for i := 0; i < len(upstreamIds); i++ {
		upstreamState := chainSupervisor.GetUpstreamState(upstreamIds[i])
		matched := multiMatcher.Match(upstreamIds[i], upstreamState)

		if !selectedUpstreams.ContainsOne(upstreamIds[i]) {
			if matched.Type() == SuccessType {
				selectedUpstreams.Add(upstreamIds[i])
				return upstreamIds[i], nil
			} else {
				if matched.Type() < currentReason.Type() {
					currentReason = matched
				}
			}
		}
	}
	return "", currentReason
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
